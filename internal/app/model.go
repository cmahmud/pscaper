package app

import (
	"fmt"
	"math"
	"math/bits"
	"net"
	"sort"
	"strings"
	"time"
)

type Candidate struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Source   string `json:"source"`
}

func (c Candidate) Key() string {
	return fmt.Sprintf("%s://%s", c.Protocol, net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port)))
}

type ProxyRecord struct {
	Key                 string    `json:"key"`
	Host                string    `json:"host"`
	Port                int       `json:"port"`
	Protocol            string    `json:"protocol"`
	Sources             []string  `json:"sources"`
	OriginSource        string    `json:"origin_source,omitempty"`
	FirstSeen           time.Time `json:"first_seen"`
	LastSeen            time.Time `json:"last_seen"`
	LastChecked         time.Time `json:"last_checked"`
	LastSuccess         time.Time `json:"last_success"`
	Successes           int       `json:"successes"`
	Failures            int       `json:"failures"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	RecentOutcomes      uint16    `json:"recent_outcomes,omitempty"`
	RecentCount         uint8     `json:"recent_count,omitempty"`
	LatencyMS           int64     `json:"latency_ms"`
	ExitIP              string    `json:"exit_ip,omitempty"`
	Alive               bool      `json:"alive"`
	EverAlive           bool      `json:"ever_alive"`
	EverGold            bool      `json:"ever_gold"`
	Score               float64   `json:"score"`
	PeakScore           float64   `json:"peak_score"`
	LastError           string    `json:"last_error,omitempty"`
	// v1.5 cohorts start on an actually successful signed check.
	// Historic proxies are not retroactively credited with survival.
	Quality *ProxySurvival `json:"quality,omitempty"`
}

func (p ProxyRecord) Address() string { return net.JoinHostPort(p.Host, fmt.Sprintf("%d", p.Port)) }
func (p ProxyRecord) URI() string     { return p.Protocol + "://" + p.Address() }
func (p ProxyRecord) Gold(threshold float64) bool {
	if !p.Alive || p.Successes < 2 || p.Score < threshold {
		return false
	}
	// Stale successes do not qualify for the current Gold tier.
	if p.LastChecked.IsZero() || time.Since(p.LastChecked) > 30*time.Minute {
		return false
	}
	// After collecting five recent results require at least 4 of the last 5.
	if p.RecentCount >= 5 && bits.OnesCount16(p.RecentOutcomes&0x1f) < 4 {
		return false
	}
	return true
}

func scoreRecord(p *ProxyRecord, now time.Time) float64 {
	total := p.Successes + p.Failures
	if total == 0 {
		return 0
	}
	successRate := float64(p.Successes) / float64(total)
	if p.RecentCount >= 4 {
		recent := float64(bits.OnesCount16(p.RecentOutcomes)) / float64(p.RecentCount)
		successRate = .4*successRate + .6*recent
	}
	reliability := successRate * 45

	lat := float64(p.LatencyMS)
	latency := 0.0
	if p.Alive && lat > 0 {
		switch {
		case lat <= 150:
			latency = 25
		case lat <= 300:
			latency = 22
		case lat <= 600:
			latency = 18
		case lat <= 1000:
			latency = 13
		case lat <= 2000:
			latency = 8
		default:
			latency = 3
		}
	}

	stability := math.Min(15, float64(p.Successes)*2.2)
	age := now.Sub(p.FirstSeen)
	longevity := math.Min(10, age.Hours()/6.0)
	recency := 0.0
	if p.Alive {
		recency = 5
	} else if !p.LastSuccess.IsZero() && now.Sub(p.LastSuccess) < 15*time.Minute {
		recency = 2
	}
	penalty := math.Min(25, float64(p.ConsecutiveFailures)*6)
	s := reliability + latency + stability + longevity + recency - penalty
	if s < 0 {
		s = 0
	}
	if s > 100 {
		s = 100
	}
	return math.Round(s*10) / 10
}

type Stats struct {
	Total                     int               `json:"total"`
	Alive                     int               `json:"alive"`
	Gold                      int               `json:"gold"`
	EverAlive                 int               `json:"ever_alive"`
	EverGold                  int               `json:"ever_gold"`
	DeadNow                   int               `json:"dead_now"`
	NeverAlive                int               `json:"never_alive"`
	Checked                   int               `json:"checked"`
	Successes                 int64             `json:"successes"`
	Failures                  int64             `json:"failures"`
	Protocols                 map[string]int    `json:"protocols"`
	AliveProtocols            map[string]int    `json:"alive_protocols"`
	AvgLatencyMS              int64             `json:"avg_latency_ms"`
	FreshAlive                int               `json:"fresh_alive"`
	GoldProtocols             map[string]int    `json:"gold_protocols"`
	ValidationAttempts        int64             `json:"validation_attempts"`
	Queue                     int               `json:"queue"`
	UrgentQueue               int               `json:"urgent_queue"`
	DiscoveryQueue            int               `json:"discovery_queue"`
	Confirming                int64             `json:"confirming"`
	TransientRecoveries       int64             `json:"transient_recoveries"`
	ConfirmedLosses           int64             `json:"confirmed_losses"`
	InFlight                  int64             `json:"in_flight"`
	Workers                   int               `json:"workers"`
	Harvests                  int64             `json:"harvests"`
	Cycles                    int64             `json:"cycles"`
	CyclePhase                string            `json:"cycle_phase"`
	NewThisHarvest            int64             `json:"new_this_harvest"`
	QueuedThisHarvest         int64             `json:"queued_this_harvest"`
	SourcesFetchedThisHarvest int64             `json:"sources_fetched_this_harvest"`
	SourceFeeds               int               `json:"source_feeds"`
	SourceErrors              int               `json:"source_errors"`
	SourcesDue                int               `json:"sources_due"`
	LastHarvest               time.Time         `json:"last_harvest"`
	LastExport                time.Time         `json:"last_export"`
	LastGitPush               time.Time         `json:"last_git_push"`
	LastArchive               time.Time         `json:"last_archive"`
	LastCycleEnd              time.Time         `json:"last_cycle_end"`
	GitStatus                 string            `json:"git_status"`
	StartedAt                 time.Time         `json:"started_at"`
	QualityFirstAt            time.Time         `json:"quality_first_at,omitempty"`
	SurvivalQuality           [3]SurvivalCohort `json:"survival_quality"`
	SourceStatus              []SourceStatus    `json:"sources"`
}

type SourceStatus struct {
	Name             string    `json:"name"`
	LastFetch        time.Time `json:"last_fetch"`
	Found            int       `json:"found"`
	Unique           int       `json:"unique"`
	NewCandidates    int       `json:"new_candidates"`
	NovelSeen        int64     `json:"novel_seen,omitempty"`
	NovelConfirmed   int64     `json:"novel_confirmed,omitempty"`
	VerifiedYieldPct float64   `json:"verified_yield_pct,omitempty"`
	KnownAlive       int       `json:"known_alive"`
	Fetches          int       `json:"fetches,omitempty"`
	YieldPct         float64   `json:"yield_pct,omitempty"`
	Error            string    `json:"error,omitempty"`
	DurationMS       int64     `json:"duration_ms"`
	RefreshSeconds   int       `json:"refresh_seconds,omitempty"`
	NextFetch        time.Time `json:"next_fetch,omitempty"`
}

func SortRecords(records []ProxyRecord) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].Score == records[j].Score {
			if records[i].LatencyMS == records[j].LatencyMS {
				return strings.Compare(records[i].URI(), records[j].URI()) < 0
			}
			return records[i].LatencyMS < records[j].LatencyMS
		}
		return records[i].Score > records[j].Score
	})
}
