package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func StartAPIServer(ctx context.Context, addr string, e *Engine) error {
	mux := http.NewServeMux()
	intel := NewASNDatabase("data/intel/ip2asn-v4-u32.tsv.gz")
	if err := intel.Reload(); err != nil {
		log.Printf("ASN database unavailable: %v", err)
	}
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := intel.Reload(); err != nil {
					log.Printf("ASN refresh: %v", err)
				}
			}
		}
	}()
	// All public routes below are explicitly allowlisted again by Nginx.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, "site/index.html")
	})
	mux.HandleFunc("GET /.well-known/acme-challenge/{token}", func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		if token == "" || strings.ContainsAny(token, "./\\") {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join("site", ".well-known", "acme-challenge", token))
	})
	mux.HandleFunc("GET /pool/stats.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.ServeFile(w, r, filepath.Join(e.cfg.ExportDir, "stats.json"))
	})
	mux.HandleFunc("GET /pool/{category}/{filename}", func(w http.ResponseWriter, r *http.Request) {
		cat, filename := r.PathValue("category"), r.PathValue("filename")
		allowed := map[string]map[string]bool{
			"alive":      {"all.txt": true, "http.txt": true, "https.txt": true, "socks4.txt": true, "socks5.txt": true},
			"gold":       {"all.txt": true, "http.txt": true, "https.txt": true, "socks4.txt": true, "socks5.txt": true},
			"historical": {"all-discovered.txt": true, "ever-alive.txt": true, "ever-gold.txt": true},
		}
		if !allowed[cat][filename] {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(e.cfg.ExportDir, cat, filename)
		if _, err := os.Stat(full); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		http.ServeFile(w, r, full)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "app": "pscaper", "time": time.Now().UTC()})
	})
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, e.Stats()) })
	// Cache costly whole-pool statistics across visitors (2-core VPS).
	var liveMu sync.Mutex
	var liveAt time.Time
	var liveCache map[string]any
	live := func() map[string]any {
		liveMu.Lock()
		defer liveMu.Unlock()
		if liveCache != nil && time.Since(liveAt) < 4*time.Second {
			return liveCache
		}
		s := e.Stats()
		status := map[string]any{"state": "unavailable"}
		if b, err := os.ReadFile("data/publisher/status.json"); err == nil {
			var val struct {
				State       string    `json:"state"`
				LastSuccess time.Time `json:"last_success"`
				Commit      string    `json:"commit"`
				LastAttempt time.Time `json:"last_attempt"`
			}
			if json.Unmarshal(b, &val) == nil {
				age := int64(-1)
				if !val.LastSuccess.IsZero() {
					age = int64(time.Since(val.LastSuccess).Seconds())
				}
				health := "ok"
				if val.State != "ok" || age > 120 {
					health = "stale"
				}
				status = map[string]any{"state": health, "age_seconds": age, "last_success": val.LastSuccess, "last_attempt": val.LastAttempt, "commit": val.Commit}
			}
		}
		current := map[string]any{"publishing": status,
			"time": time.Now().UTC(), "total": s.Total, "alive": s.Alive,
			"gold": s.Gold, "checked": s.Checked, "ever_alive": s.EverAlive,
			"ever_gold": s.EverGold, "alive_protocols": s.AliveProtocols,
			"avg_latency_ms": s.AvgLatencyMS, "fresh_alive": s.FreshAlive,
			"queue": s.Queue, "urgent_queue": s.UrgentQueue, "discovery_queue": s.DiscoveryQueue,
			"validation_attempts": s.ValidationAttempts,
			"confirming":          s.Confirming, "transient_recoveries": s.TransientRecoveries,
			"confirmed_losses": s.ConfirmedLosses,
			"in_flight":        s.InFlight, "workers": s.Workers,
			"source_feeds": s.SourceFeeds, "source_errors": s.SourceErrors,
			"last_harvest": s.LastHarvest, "last_export": s.LastExport,
			"harvests": s.Harvests, "uptime_seconds": int64(time.Since(s.StartedAt).Seconds()),
			"newly_alive": e.newlyAlive.Load(), "lost_alive": e.lostAlive.Load(),
			"newly_discovered": e.newlyDiscovered.Load(),
		}
		liveCache = current
		liveAt = time.Now()
		return current
	}
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		after, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		if err != nil {
			after = 0
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		records, last := e.events.Since(after, limit)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, map[string]any{"events": records, "cursor": last, "generated_at": time.Now().UTC()})
	})
	mux.HandleFunc("GET /api/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, live())
	})
	// Five-second in-memory samples plus persistent 30-second snapshots for 7 days.
	hist := NewHistoryLog("data/telemetry/history.jsonl")
	var hmu sync.RWMutex
	recent := make([]HistoryPoint, 0, 720)
	sample := func() {
		v := live()
		a, _ := v["alive"].(int)
		g, _ := v["gold"].(int)
		q, _ := v["queue"].(int)
		p := HistoryPoint{Time: time.Now().UTC(), Alive: a, Gold: g, Queue: q}
		hmu.Lock()
		recent = append(recent, p)
		if len(recent) > 720 {
			copy(recent, recent[len(recent)-720:])
			recent = recent[:720]
		}
		hmu.Unlock()
		if err := hist.Append(p); err != nil {
			log.Printf("history append: %v", err)
		}
	}
	sample()
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sample()
			}
		}
	}()
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		period := r.URL.Query().Get("range")
		switch period {
		case "", "1h":
			hmu.RLock()
			points := append([]HistoryPoint(nil), recent...)
			hmu.RUnlock()
			// Merge persisted previous-hour samples after a process restart.
			disk := hist.Range(time.Now().Add(-time.Hour), 480)
			if len(points) > 0 {
				edge := points[0].Time
				for _, p := range disk {
					if p.Time.Before(edge) {
						points = append([]HistoryPoint{p}, points...)
					}
				}
			}
			writeJSON(w, points)
		case "24h":
			writeJSON(w, hist.Range(time.Now().Add(-24*time.Hour), 480))
		case "7d":
			writeJSON(w, hist.Range(time.Now().Add(-7*24*time.Hour), 480))
		default:
			http.Error(w, "invalid range", 400)
		}
	})
	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, r *http.Request) {
		s := e.Stats()
		type source struct {
			Name             string  `json:"name"`
			Found            int     `json:"found"`
			New              int     `json:"new"`
			KnownAlive       int     `json:"known_alive"`
			YieldPct         float64 `json:"yield_pct"`
			VerifiedYieldPct float64 `json:"verified_yield_pct"`
			NovelSeen        int64   `json:"novel_seen"`
			NovelConfirmed   int64   `json:"novel_confirmed"`
			Error            bool    `json:"error"`
		}
		entries := make([]source, 0, len(s.SourceStatus))
		for _, v := range s.SourceStatus {
			entries = append(entries, source{Name: v.Name, Found: v.Unique, New: v.NewCandidates, KnownAlive: v.KnownAlive, YieldPct: v.YieldPct, VerifiedYieldPct: v.VerifiedYieldPct,
				NovelSeen: v.NovelSeen, NovelConfirmed: v.NovelConfirmed, Error: v.Error != ""})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].NovelConfirmed != entries[j].NovelConfirmed {
				return entries[i].NovelConfirmed > entries[j].NovelConfirmed
			}
			if entries[i].KnownAlive != entries[j].KnownAlive {
				return entries[i].KnownAlive > entries[j].KnownAlive
			}
			return entries[i].New > entries[j].New
		})
		if len(entries) > 20 {
			entries = entries[:20]
		}
		writeJSON(w, entries)
	})
	var geoMu sync.Mutex
	var geoAt time.Time
	var geoCache map[string]any
	mux.HandleFunc("GET /api/asn", func(w http.ResponseWriter, r *http.Request) {
		geoMu.Lock()
		if geoCache == nil || time.Since(geoAt) > 90*time.Second {
			c := map[string]int{}
			a := map[uint32]int{}
			found := 0
			for _, p := range e.store.Snapshot() {
				if !p.Alive {
					continue
				}
				v, ok := intel.Lookup(p.Host)
				if !ok {
					continue
				}
				found++
				if v.ASCountry != "" {
					c[v.ASCountry]++
				}
				if v.ASN != 0 {
					a[v.ASN]++
				}
			}
			type group struct {
				Code  string `json:"code"`
				Count int    `json:"count"`
			}
			regions := make([]group, 0, len(c))
			for name, n := range c {
				regions = append(regions, group{Code: name, Count: n})
			}
			sort.Slice(regions, func(i, j int) bool { return regions[i].Count > regions[j].Count })
			if len(regions) > 8 {
				regions = regions[:8]
			}
			networks := make([]group, 0, len(a))
			for code, n := range a {
				networks = append(networks, group{Code: strconv.FormatUint(uint64(code), 10), Count: n})
			}
			sort.Slice(networks, func(i, j int) bool { return networks[i].Count > networks[j].Count })
			if len(networks) > 8 {
				networks = networks[:8]
			}
			geoCache = map[string]any{"as_countries": regions, "asns": networks, "identified": found,
				"notice": "AS registration country is not a verified physical proxy location"}
			geoAt = time.Now()
		}
		data := geoCache
		geoMu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, data)
	})
	mux.HandleFunc("GET /api/v1/proxies", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tier := strings.ToLower(q.Get("tier"))
		if tier != "" && tier != "alive" && tier != "gold" {
			http.Error(w, "invalid tier", 400)
			return
		}
		proto := strings.ToLower(q.Get("protocol"))
		if proto != "" && proto != "all" && normalizeProtocol(proto) == "" {
			http.Error(w, "invalid protocol", 400)
			return
		}
		minScore, _ := strconv.ParseFloat(q.Get("min_score"), 64)
		maxLatency, _ := strconv.Atoi(q.Get("max_latency_ms"))
		freshMinutes, _ := strconv.Atoi(q.Get("fresh_minutes"))
		asCountry := strings.ToUpper(strings.TrimSpace(q.Get("as_country")))
		asNumber, _ := strconv.Atoi(q.Get("asn"))
		if len(asCountry) > 2 || asNumber < 0 {
			http.Error(w, "invalid ASN filter", 400)
			return
		}
		if minScore < 0 || minScore > 100 || maxLatency < 0 || freshMinutes < 0 || freshMinutes > 10080 {
			http.Error(w, "invalid filter", 400)
			return
		}
		if mode := q.Get("format"); mode != "" && mode != "json" && mode != "txt" {
			http.Error(w, "invalid format", 400)
			return
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 {
			limit = 1000
		}
		if limit > 5000 {
			limit = 5000
		}
		records := e.store.Snapshot()
		selected := make([]ProxyRecord, 0, limit)
		for _, p := range records {
			if !p.Alive || (tier == "gold" && !p.Gold(e.cfg.GoldScore)) {
				continue
			}
			if proto != "" && proto != "all" && p.Protocol != proto {
				continue
			}
			if p.Score < minScore {
				continue
			}
			if maxLatency > 0 && (p.LatencyMS <= 0 || p.LatencyMS > int64(maxLatency)) {
				continue
			}
			if freshMinutes > 0 && (p.LastChecked.IsZero() || time.Since(p.LastChecked) > time.Duration(freshMinutes)*time.Minute) {
				continue
			}
			if asCountry != "" || asNumber > 0 {
				meta, ok := intel.Lookup(p.Host)
				if !ok || (asCountry != "" && meta.ASCountry != asCountry) ||
					(asNumber > 0 && meta.ASN != uint32(asNumber)) {
					continue
				}
			}
			selected = append(selected, p)
		}
		SortRecords(selected)
		total := len(selected)
		if len(selected) > limit {
			selected = selected[:limit]
		}
		proxies := make([]map[string]any, 0, len(selected))
		for _, p := range selected {
			meta, _ := intel.Lookup(p.Host)
			proxies = append(proxies, map[string]any{
				"url": p.URI(), "protocol": p.Protocol, "host": p.Host, "port": p.Port,
				"score": p.Score, "latency_ms": p.LatencyMS, "last_checked": p.LastChecked,
				"asn": meta.ASN, "as_country": meta.ASCountry, "as_description": meta.Description,
			})
		}
		if q.Get("format") == "txt" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			for _, p := range selected {
				fmt.Fprintln(w, p.URI())
			}
			return
		}
		writeJSON(w, map[string]any{"generated_at": time.Now().UTC(), "tier": tier, "protocol": proto, "total": total, "limit": limit, "proxies": proxies})
	})
	mux.HandleFunc("GET /api/top", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit == 0 {
			limit = 30
		}
		gold := r.URL.Query().Get("gold") == "1" || r.URL.Query().Get("gold") == "true"
		writeJSON(w, e.Top(limit, r.URL.Query().Get("protocol"), gold))
	})
	mux.HandleFunc("POST /api/action/harvest", func(w http.ResponseWriter, r *http.Request) {
		if e.cfg.Continuous {
			e.ForceHarvest()
			writeJSON(w, map[string]any{"ok": true, "queued": "forced source refresh"})
			return
		}
		go func() { _ = e.Harvest(context.Background()) }()
		writeJSON(w, map[string]any{"ok": true, "queued": "harvest"})
	})
	mux.HandleFunc("POST /api/action/export", func(w http.ResponseWriter, r *http.Request) {
		go func() { _ = e.ExportAndPush(context.Background()) }()
		writeJSON(w, map[string]any{"ok": true, "queued": "export+push"})
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return nil
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
