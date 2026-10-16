package app

import (
	"context"
	"math"
	"net/http"
	"sync"
	"time"
)

type CounterSample struct {
	at        time.Time
	checks    int64
	passed    int64
	recovered int64
	lost      int64
}
type ActivityWindow struct {
	mu      sync.RWMutex
	samples []CounterSample
}

func (a *ActivityWindow) Capture(e *Engine, now time.Time) {
	s := CounterSample{at: now, checks: e.successes.Load() + e.failures.Load(),
		passed: e.successes.Load(), recovered: e.transientRecoveries.Load(),
		lost: e.confirmedLosses.Load()}
	a.mu.Lock()
	a.samples = append(a.samples, s)
	edge := now.Add(-6 * time.Minute)
	n := 0
	for n < len(a.samples)-1 && a.samples[n].at.Before(edge) {
		n++
	}
	if n > 0 {
		copy(a.samples, a.samples[n:])
		a.samples = a.samples[:len(a.samples)-n]
	}
	a.mu.Unlock()
}
func (a *ActivityWindow) Summary(now time.Time) map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.samples) < 2 {
		return map[string]any{"status": "warming_up", "window_seconds": 0}
	}
	newest := a.samples[len(a.samples)-1]
	// Approximate rolling 60s checks/min and 5-minute success ratio. Preserve
	// actual measurement windows without extrapolating a freshly restarted process.
	minute := a.samples[0]
	five := a.samples[0]
	for _, s := range a.samples {
		if !s.at.After(now.Add(-time.Minute)) {
			minute = s
		}
		if !s.at.After(now.Add(-5 * time.Minute)) {
			five = s
		}
	}
	minutes := newest.at.Sub(minute.at).Minutes()
	fiveDuration := newest.at.Sub(five.at).Seconds()
	if minutes <= 0 {
		return map[string]any{"status": "warming_up", "window_seconds": 0}
	}
	rate := float64(newest.checks-minute.checks) / minutes
	checks5 := newest.checks - five.checks
	passes5 := newest.passed - five.passed
	passPct := 0.0
	if checks5 > 0 {
		passPct = 100 * float64(passes5) / float64(checks5)
	}
	return map[string]any{
		"status": "ok", "window_seconds": int64(fiveDuration),
		"validation_per_min":   int64(math.Round(rate)),
		"success_pct":          math.Round(passPct*10) / 10,
		"transient_recoveries": newest.recovered - five.recovered,
		"confirmed_losses":     newest.lost - five.lost,
		"attempts":             checks5, "passed": passes5,
	}
}
func registerIntelligenceAPI(ctx context.Context, mux *http.ServeMux, e *Engine) {
	window := &ActivityWindow{samples: make([]CounterSample, 0, 26)}
	window.Capture(e, time.Now())
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				window.Capture(e, t)
			}
		}
	}()
	mux.HandleFunc("GET /api/v2/intelligence", func(w http.ResponseWriter, r *http.Request) {
		report := window.Summary(time.Now())
		report["source_count"] = e.Stats().SourceFeeds
		report["queue"] = len(e.queue) + len(e.urgent)
		writeCachedJSON(w, r, report)
	})
}
