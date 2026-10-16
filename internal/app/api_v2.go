package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type v2Filter struct {
	tier, protocol, country string
	asn                     uint32
	limit                   int
	minScore                float64
	maxLatency              int64
	freshMinutes            int
}

func parseV2Filter(q url.Values) (v2Filter, error) {
	f := v2Filter{tier: "alive", limit: 100}
	allowed := map[string]bool{"tier": true, "protocol": true, "min_score": true, "max_latency_ms": true,
		"fresh_minutes": true, "as_country": true, "asn": true, "limit": true, "cursor": true}
	for key, vals := range q {
		if !allowed[key] || len(vals) != 1 {
			return f, fmt.Errorf("unsupported or duplicated filter: %s", key)
		}
	}
	if x := strings.ToLower(q.Get("tier")); x != "" {
		if x != "alive" && x != "gold" {
			return f, fmt.Errorf("invalid tier")
		}
		f.tier = x
	}
	f.protocol = strings.ToLower(q.Get("protocol"))
	if f.protocol == "all" {
		f.protocol = ""
	}
	if f.protocol != "" && normalizeProtocol(f.protocol) == "" {
		return f, fmt.Errorf("invalid protocol")
	}
	if s := q.Get("limit"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 1 || n > 250 {
			return f, fmt.Errorf("limit must be 1..250")
		}
		f.limit = n
	}
	if s := q.Get("min_score"); s != "" {
		n, e := strconv.ParseFloat(s, 64)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
			return f, fmt.Errorf("invalid min_score")
		}
		f.minScore = n
	}
	if s := q.Get("max_latency_ms"); s != "" {
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil || n < 0 || n > 600000 {
			return f, fmt.Errorf("invalid max_latency_ms")
		}
		f.maxLatency = n
	}
	if s := q.Get("fresh_minutes"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 0 || n > 10080 {
			return f, fmt.Errorf("invalid fresh_minutes")
		}
		f.freshMinutes = n
	}
	f.country = strings.ToUpper(q.Get("as_country"))
	if f.country != "" {
		if len(f.country) != 2 {
			return f, fmt.Errorf("invalid as_country")
		}
		for _, c := range f.country {
			if c < 'A' || c > 'Z' {
				return f, fmt.Errorf("invalid as_country")
			}
		}
	}
	if s := q.Get("asn"); s != "" {
		n, e := strconv.ParseUint(s, 10, 32)
		if e != nil || n == 0 {
			return f, fmt.Errorf("invalid asn")
		}
		f.asn = uint32(n)
	}
	return f, nil
}
func v2QueryHash(q url.Values) [8]byte {
	clone := url.Values{}
	for k, v := range q {
		if k != "cursor" {
			clone[k] = v
		}
	}
	sum := sha256.Sum256([]byte(clone.Encode()))
	var res [8]byte
	copy(res[:], sum[:8])
	return res
}
func encodeV2Cursor(stamp int64, offset int, qhash [8]byte) string {
	var buf [20]byte
	binary.BigEndian.PutUint64(buf[:8], uint64(stamp))
	binary.BigEndian.PutUint32(buf[8:12], uint32(offset))
	copy(buf[12:], qhash[:])
	return base64.RawURLEncoding.EncodeToString(buf[:])
}
func decodeV2Cursor(value string, hash [8]byte) (int64, int, error) {
	b, e := base64.RawURLEncoding.DecodeString(value)
	if e != nil || len(b) != 20 {
		return 0, 0, fmt.Errorf("invalid cursor")
	}
	var received [8]byte
	copy(received[:], b[12:20])
	if received != hash {
		return 0, 0, fmt.Errorf("cursor filters changed")
	}
	return int64(binary.BigEndian.Uint64(b[:8])), int(binary.BigEndian.Uint32(b[8:12])), nil
}
func matchesV2Proxy(p ProxyRecord, f v2Filter, intel *ASNDatabase, now time.Time, goldThreshold float64) bool {
	if !p.Alive || (f.tier == "gold" && !p.Gold(goldThreshold)) {
		return false
	}
	if f.protocol != "" && p.Protocol != f.protocol {
		return false
	}
	if p.Score < f.minScore {
		return false
	}
	if f.maxLatency > 0 && (p.LatencyMS < 1 || p.LatencyMS > f.maxLatency) {
		return false
	}
	if f.freshMinutes > 0 && (p.LastChecked.IsZero() || now.Sub(p.LastChecked) > time.Duration(f.freshMinutes)*time.Minute) {
		return false
	}
	if f.country != "" || f.asn != 0 {
		info, ok := intel.Lookup(p.Host)
		if !ok || (f.country != "" && info.ASCountry != f.country) || (f.asn != 0 && info.ASN != f.asn) {
			return false
		}
	}
	return true
}

func writeCachedJSON(w http.ResponseWriter, r *http.Request, payload any) {
	raw, e := json.Marshal(payload)
	if e != nil {
		http.Error(w, "response encoding failed", 500)
		return
	}
	sum := sha256.Sum256(raw)
	etag := "\"" + hex.EncodeToString(sum[:12]) + "\""
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=5, must-revalidate")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(raw, '\n'))
}
func registerAPIV2(mux *http.ServeMux, e *Engine, intel *ASNDatabase, index *ProxyIndex) {
	mux.HandleFunc("GET /api/v2/proxies", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter, err := parseV2Filter(q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		queryHash := v2QueryHash(q)
		var stamp time.Time
		var recs []ProxyRecord
		offset := 0
		if token := q.Get("cursor"); token != "" {
			timestamp, off, e := decodeV2Cursor(token, queryHash)
			if e != nil {
				http.Error(w, e.Error(), http.StatusBadRequest)
				return
			}
			var valid bool
			stamp, recs, valid = index.ForCursor(timestamp)
			if !valid {
				http.Error(w, "cursor expired; restart from the first page", http.StatusConflict)
				return
			}
			offset = off
		} else {
			stamp, recs = index.Current()
		}
		// Freshness filters use snapshot time to keep cursor pages stable.
		now := stamp
		selected := make([]ProxyRecord, 0, len(recs))
		for _, p := range recs {
			if matchesV2Proxy(p, filter, intel, now, e.cfg.GoldScore) {
				selected = append(selected, p)
			}
		}
		if offset > len(selected) {
			http.Error(w, "invalid cursor offset", 400)
			return
		}
		end := offset + filter.limit
		if end > len(selected) {
			end = len(selected)
		}
		page := selected[offset:end]
		rows := make([]map[string]any, 0, len(page))
		for _, p := range page {
			data, _ := intel.Lookup(p.Host)
			recentRate := 0.0
			if p.RecentCount > 0 {
				recentRate = 100 * float64(bits.OnesCount16(p.RecentOutcomes)) / float64(p.RecentCount)
			}
			rows = append(rows, map[string]any{
				"url": p.URI(), "protocol": p.Protocol, "host": p.Host, "port": p.Port,
				"score": p.Score, "latency_ms": p.LatencyMS,
				"last_checked": p.LastChecked, "last_success": p.LastSuccess,
				"successes": p.Successes, "failures": p.Failures, "recent_success_pct": math.Round(recentRate*10) / 10,
				"asn": data.ASN, "as_country": data.ASCountry, "as_description": data.Description,
			})
		}
		var cursor any = nil
		if end < len(selected) {
			cursor = encodeV2Cursor(stamp.UnixNano(), end, queryHash)
		}
		writeCachedJSON(w, r, map[string]any{
			"api_version": "2", "generated_at": stamp, "total": len(selected),
			"count": len(rows), "limit": filter.limit, "next_cursor": cursor,
			"tier": filter.tier, "protocol": filter.protocol, "proxies": rows,
		})
	})
	mux.HandleFunc("GET /api/v2/health", func(w http.ResponseWriter, r *http.Request) {
		st := e.Stats()
		state := "healthy"
		if st.SourceErrors > 0 {
			state = "degraded"
		}
		writeCachedJSON(w, r, map[string]any{
			"status": state, "service": "pscaper", "updated_at": time.Now().UTC().Truncate(5 * time.Second),
			"alive": st.Alive, "gold": st.Gold, "feed_count": st.SourceFeeds,
			"source_errors": st.SourceErrors, "queue": st.Queue,
		})
	})
}
