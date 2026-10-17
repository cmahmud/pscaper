package app

import (
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

var survivalHorizons = [3]time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour}

// A resolved cohort must either have passed a successful check at or beyond
// the horizon without failing, or have definitively failed before that check.
// Unchecked milestones remain pending (we never infer an unobserved success).
type ProxySurvival struct {
	FirstAt    time.Time `json:"first_at"`
	EpochStart time.Time `json:"epoch_start"`
	Resolved   uint8     `json:"resolved"`
	Observed   [3]uint32 `json:"observed"`
	Passed     [3]uint32 `json:"passed"`
}

func updateQualityCohorts(p *ProxyRecord, passed bool, now time.Time) {
	if p.Quality == nil {
		if passed {
			p.Quality = &ProxySurvival{FirstAt: now, EpochStart: now}
		}
		return
	}
	q := p.Quality
	if q.EpochStart.IsZero() {
		if passed {
			q.EpochStart = now
			q.Resolved = 0
		}
		return
	}
	age := now.Sub(q.EpochStart)
	if age < 0 {
		return
	}
	for i, horizon := range survivalHorizons {
		bit := uint8(1 << i)
		if q.Resolved&bit != 0 {
			continue
		}
		if passed && age < horizon {
			continue
		}
		q.Observed[i]++
		if passed {
			q.Passed[i]++
		}
		q.Resolved |= bit
	}
	if !passed {
		q.EpochStart = time.Time{}
		q.Resolved = 0
	}
}

// SurvivalCohort counts evaluated activation episodes, not unique proxies.
// The epoch sampling starts with the v1.5 release.
type SurvivalCohort struct {
	Minutes  int      `json:"minutes"`
	Samples  uint64   `json:"samples"`
	Mature   bool     `json:"mature"`
	Survived uint64   `json:"survived"`
	RatePct  *float64 `json:"rate_pct"`
}

func buildSurvivalCohorts(observed, passed [3]uint64) [3]SurvivalCohort {
	horizons := [3]int{5, 15, 60}
	var out [3]SurvivalCohort
	for i := range out {
		out[i] = SurvivalCohort{Minutes: horizons[i], Samples: observed[i], Survived: passed[i]}
		if observed[i] > 0 {
			v := math.Round(1000*float64(passed[i])/float64(observed[i])) / 10
			out[i].RatePct = &v
		}
	}
	return out
}

type QualityDiversity struct {
	Endpoints      int `json:"endpoints"`
	UniqueIPs      int `json:"unique_ips"`
	UniqueNetworks int `json:"unique_networks"`
	UniqueASNs     int `json:"unique_asns"`
	ASNIdentified  int `json:"asn_identified"`
}
type qualityOverview struct {
	Built     time.Time
	Diversity QualityDiversity
}
type QualityCache struct {
	mu   sync.Mutex
	last qualityOverview
}

func (q *QualityCache) Diversity(index *ProxyIndex, intel *ASNDatabase) QualityDiversity {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.last.Built.IsZero() && time.Since(q.last.Built) < 12*time.Second {
		return q.last.Diversity
	}
	_, items := index.Current()
	ips := make(map[string]struct{}, len(items))
	subnets := make(map[string]struct{}, len(items))
	asns := make(map[uint32]struct{})
	identified := 0
	for _, p := range items {
		if !p.Alive {
			continue
		}
		ips[p.Host] = struct{}{}
		if ip, err := netip.ParseAddr(p.Host); err == nil {
			bits := 24
			if ip.Is6() {
				bits = 48
			}
			subnets[netip.PrefixFrom(ip, bits).Masked().String()] = struct{}{}
		}
		if v, ok := intel.Lookup(p.Host); ok && v.ASN != 0 {
			asns[v.ASN] = struct{}{}
			identified++
		}
	}
	ret := QualityDiversity{
		Endpoints: len(items), UniqueIPs: len(ips), UniqueNetworks: len(subnets),
		UniqueASNs: len(asns), ASNIdentified: identified,
	}
	q.last = qualityOverview{Built: time.Now(), Diversity: ret}
	return ret
}

// This only returns bounded, well-supported refresh recommendations.
// Existing source cadence is never modified until the confidence gate
// has been met; no source is permanently excluded.
type SourceConfidence struct {
	Recommendation string `json:"recommendation"`
	Evidence       string `json:"evidence"`
}

func sourceConfidence(st SourceStatus) SourceConfidence {
	if st.Fetches < 5 || st.NovelSeen < 300 {
		return SourceConfidence{Recommendation: "learning", Evidence: "at least 5 fetches and 300 novel endpoints required"}
	}
	if st.NovelConfirmed >= 8 && st.VerifiedYieldPct >= 0.8 &&
		st.NovelAlive >= 4 && st.NovelSurvived15 >= 3 {
		return SourceConfidence{Recommendation: "prioritize", Evidence: strconv.FormatInt(st.NovelConfirmed, 10) + " distinct verified discoveries"}
	}
	if st.NovelSeen >= 800 && st.NovelConfirmed <= 2 &&
		st.VerifiedYieldPct < 0.05 && st.NovelAlive <= 1 {
		return SourceConfidence{Recommendation: "deprioritize", Evidence: "low verified-novel yield after 800 new endpoints"}
	}
	return SourceConfidence{Recommendation: "normal", Evidence: "insufficient signal for cadence adjustment"}
}

// registerQualityAPI exposes measured historical activation cohorts and live
// pool diversity. No unverifiable forecast or synthetic survival claim.
func registerQualityAPI(mux *http.ServeMux, e *Engine, index *ProxyIndex, intel *ASNDatabase) {
	cache := &QualityCache{}
	mux.HandleFunc("GET /api/v2/quality", func(w http.ResponseWriter, r *http.Request) {
		st := e.Stats()
		diver := cache.Diversity(index, intel)
		counts := map[string]int{"learning": 0, "normal": 0, "prioritize": 0, "deprioritize": 0}
		for _, src := range st.SourceStatus {
			counts[sourceConfidence(src).Recommendation]++
		}
		stage := "collecting"
		if st.SurvivalQuality[0].Mature && st.SurvivalQuality[1].Mature && st.SurvivalQuality[2].Mature &&
			st.SurvivalQuality[0].Samples >= 30 && st.SurvivalQuality[1].Samples >= 30 &&
			st.SurvivalQuality[2].Samples >= 30 {
			stage = "measured"
		}
		writeCachedJSON(w, r, map[string]any{
			"status":                  stage,
			"survival":                st.SurvivalQuality,
			"diversity":               diver,
			"source_decisions":        counts,
			"cohort_definition":       "Each signed-success activation starts a cohort. A later signed-success check at/after a milestone passes it; any earlier failed check resolves the pending milestone as not survived. Unobserved checkpoints are pending.",
			"cohort_since":            "v1.5",
			"sample_threshold":        30,
			"gold_criteria_unchanged": true,
		})
	})
}
