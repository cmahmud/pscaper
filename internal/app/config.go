package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DataDir           string
	SourcesFile       string
	ExportDir         string
	APIAddr           string
	ProbeHost         string
	ProbeHTTPAddr     string
	ProbeHTTPSAddr    string
	ProbeHTTPPort     int
	ProbeHTTPSPort    int
	ProbeToken        string
	Workers           int
	QueueSize         int
	Timeout           time.Duration
	HarvestInterval   time.Duration
	AliveRecheck      time.Duration
	DeadRecheck       time.Duration
	ExportInterval    time.Duration
	ArchiveInterval   time.Duration
	Continuous        bool
	CycleBackoffMin   time.Duration
	CycleBackoffMax   time.Duration
	FlushInterval     time.Duration
	SourceConcurrency int
	GoldScore         float64
	GitPush           bool
	GitRemote         string
	GitBranch         string
}

func LoadConfig() (Config, error) {
	cwd, _ := os.Getwd()
	dataDir := env("PSCAPER_DATA_DIR", filepath.Join(cwd, "data"))
	cfg := Config{
		DataDir:           dataDir,
		SourcesFile:       env("PSCAPER_SOURCES", filepath.Join(cwd, "config", "sources.json")),
		ExportDir:         env("PSCAPER_EXPORT_DIR", filepath.Join(cwd, "exports")),
		APIAddr:           env("PSCAPER_API_ADDR", "127.0.0.1:8788"),
		ProbeHost:         strings.TrimSpace(os.Getenv("PSCAPER_PUBLIC_HOST")),
		ProbeHTTPAddr:     env("PSCAPER_PROBE_HTTP_ADDR", ":8787"),
		ProbeHTTPSAddr:    env("PSCAPER_PROBE_HTTPS_ADDR", ":8789"),
		ProbeHTTPPort:     envInt("PSCAPER_PROBE_HTTP_PORT", 8787),
		ProbeHTTPSPort:    envInt("PSCAPER_PROBE_HTTPS_PORT", 8789),
		ProbeToken:        strings.TrimSpace(os.Getenv("PSCAPER_PROBE_TOKEN")),
		Workers:           envInt("PSCAPER_WORKERS", 3000),
		QueueSize:         envInt("PSCAPER_QUEUE_SIZE", 3000000),
		Timeout:           envDuration("PSCAPER_TIMEOUT", 6*time.Second),
		HarvestInterval:   envDuration("PSCAPER_HARVEST_INTERVAL", 5*time.Minute),
		AliveRecheck:      envDuration("PSCAPER_ALIVE_RECHECK", 5*time.Minute),
		DeadRecheck:       envDuration("PSCAPER_DEAD_RECHECK", 30*time.Minute),
		ExportInterval:    envDuration("PSCAPER_EXPORT_INTERVAL", 10*time.Minute),
		ArchiveInterval:   envDuration("PSCAPER_ARCHIVE_INTERVAL", time.Hour),
		Continuous:        envBool("PSCAPER_CONTINUOUS", true),
		CycleBackoffMin:   envDuration("PSCAPER_CYCLE_BACKOFF_MIN", 5*time.Second),
		CycleBackoffMax:   envDuration("PSCAPER_CYCLE_BACKOFF_MAX", 30*time.Second),
		FlushInterval:     envDuration("PSCAPER_FLUSH_INTERVAL", 3*time.Second),
		SourceConcurrency: envInt("PSCAPER_SOURCE_CONCURRENCY", 24),
		GoldScore:         envFloat("PSCAPER_GOLD_SCORE", 80),
		GitPush:           envBool("PSCAPER_GIT_PUSH", true),
		GitRemote:         env("PSCAPER_GIT_REMOTE", "origin"),
		GitBranch:         env("PSCAPER_GIT_BRANCH", "main"),
	}
	if cfg.ProbeToken == "" {
		b := make([]byte, 18)
		if _, err := rand.Read(b); err != nil {
			return cfg, err
		}
		cfg.ProbeToken = hex.EncodeToString(b)
	}
	if cfg.ProbeHost == "" {
		return cfg, fmt.Errorf("PSCAPER_PUBLIC_HOST is required (the VPS public IPv4 or DNS name)")
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.QueueSize < cfg.Workers*2 {
		cfg.QueueSize = cfg.Workers * 2
	}
	if cfg.CycleBackoffMin < time.Second {
		cfg.CycleBackoffMin = time.Second
	}
	if cfg.CycleBackoffMax < cfg.CycleBackoffMin {
		cfg.CycleBackoffMax = cfg.CycleBackoffMin
	}
	return cfg, nil
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
func envInt(k string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k))); err == nil && v > 0 {
		return v
	}
	return def
}
func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(k)), 64); err == nil {
		return v
	}
	return def
}
func envBool(k string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(k)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
func envDuration(k string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
