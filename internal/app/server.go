package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func StartAPIServer(ctx context.Context, addr string, e *Engine) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "app": "pscaper", "time": time.Now().UTC()})
	})
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, e.Stats()) })
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
