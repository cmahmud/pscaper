package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCandidate(t *testing.T) {
	cases := []struct {
		line, hint, proto, host string
		port                    int
	}{
		{"1.2.3.4:8080", "http", "http", "1.2.3.4", 8080},
		{"socks5://8.8.8.8:1080", "http", "socks5", "8.8.8.8", 1080},
		{"45.225.89.145:999:Ecuador", "http", "http", "45.225.89.145", 999},
	}
	for _, tc := range cases {
		c, ok := parseCandidate(tc.line, tc.hint, "test")
		if !ok {
			t.Fatalf("failed %q", tc.line)
		}
		if c.Protocol != tc.proto || c.Host != tc.host || c.Port != tc.port {
			t.Fatalf("bad parse: %#v", c)
		}
	}
	if _, ok := parseCandidate("127.0.0.1:8080", "http", "test"); ok {
		t.Fatal("private/loopback endpoint should be rejected")
	}
}
func TestStorePersistenceAndExport(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "state.json.gz")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c := Candidate{Host: "8.8.8.8", Port: 8080, Protocol: "http", Source: "unit"}
	_, p := s.UpsertCandidate(c, now)
	if err := s.ApplyValidation(p.Key, true, 100*time.Millisecond, "8.8.4.4", "", now, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, ok := s2.Get(p.Key)
	if !ok || !got.Alive || got.Successes != 1 {
		t.Fatalf("bad persisted record: %#v", got)
	}
	out := filepath.Join(d, "out")
	if _, err := s2.Export(out, 0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "alive", "http.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("expected exported proxy")
	}
}

func TestShouldQueueUsesDifferentAliveAndDeadCadence(t *testing.T) {
	e := &Engine{cfg: Config{AliveRecheck: 5 * time.Minute, DeadRecheck: 30 * time.Minute}}
	now := time.Now()

	if !e.shouldQueue(ProxyRecord{}, now) {
		t.Fatal("never-checked proxy should queue immediately")
	}
	aliveFresh := ProxyRecord{Alive: true, LastChecked: now.Add(-4 * time.Minute)}
	if e.shouldQueue(aliveFresh, now) {
		t.Fatal("fresh alive proxy should not be requeued before alive interval")
	}
	aliveDue := ProxyRecord{Alive: true, LastChecked: now.Add(-5 * time.Minute)}
	if !e.shouldQueue(aliveDue, now) {
		t.Fatal("alive proxy should requeue when alive interval is due")
	}
	deadFresh := ProxyRecord{Alive: false, LastChecked: now.Add(-6 * time.Minute)}
	if e.shouldQueue(deadFresh, now) {
		t.Fatal("dead proxy must not be requeued using the alive interval")
	}
	deadDue := ProxyRecord{Alive: false, LastChecked: now.Add(-30 * time.Minute)}
	if !e.shouldQueue(deadDue, now) {
		t.Fatal("dead proxy should requeue when dead interval is due")
	}
}

func TestHistoricalExportSurvivesCurrentFailure(t *testing.T) {
	d := t.TempDir()
	s, err := OpenStore(filepath.Join(d, "state.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	c := Candidate{Host: "8.8.8.8", Port: 8080, Protocol: "http", Source: "unit"}
	_, p := s.UpsertCandidate(c, now)
	if err := s.ApplyValidation(p.Key, true, 100*time.Millisecond, "8.8.4.4", "", now, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyValidation(p.Key, true, 100*time.Millisecond, "8.8.4.4", "", now.Add(time.Second), 0); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyValidation(p.Key, false, 0, "", "timeout", now.Add(2*time.Second), 0); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Get(p.Key)
	if got.Alive {
		t.Fatal("proxy should be currently dead")
	}
	if !got.EverAlive || !got.EverGold {
		t.Fatalf("history flags lost: %#v", got)
	}

	out := filepath.Join(d, "out")
	if _, err := s.Export(out, 0); err != nil {
		t.Fatal(err)
	}
	aliveNow, _ := os.ReadFile(filepath.Join(out, "alive", "all.txt"))
	if strings.Contains(string(aliveNow), p.URI()) {
		t.Fatal("dead proxy must not remain in current alive")
	}
	everAlive, _ := os.ReadFile(filepath.Join(out, "historical", "ever-alive.txt"))
	everGold, _ := os.ReadFile(filepath.Join(out, "historical", "ever-gold.txt"))
	if !strings.Contains(string(everAlive), p.URI()) {
		t.Fatal("missing from ever-alive")
	}
	if !strings.Contains(string(everGold), p.URI()) {
		t.Fatal("missing from ever-gold")
	}

	// Existing seeded history must survive future exports even if it is not in state.
	seed := "socks5://1.1.1.1:1080\n"
	if err := os.WriteFile(filepath.Join(out, "historical", "ever-gold.txt"), append(everyGoldBytes(everGold), []byte(seed)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Export(out, 0); err != nil {
		t.Fatal(err)
	}
	merged, _ := os.ReadFile(filepath.Join(out, "historical", "ever-gold.txt"))
	if !strings.Contains(string(merged), "socks5://1.1.1.1:1080") {
		t.Fatal("seeded historical line was overwritten")
	}
}

func everyGoldBytes(b []byte) []byte { return append([]byte(nil), b...) }

func TestSourceRefreshSettings(t *testing.T) {
	s := Source{RefreshSeconds: 600, MaxBytesMB: 64}
	if got := s.RefreshInterval(); got != 10*time.Minute {
		t.Fatalf("refresh interval = %v", got)
	}
	if got := s.MaxBytes(); got != 64<<20 {
		t.Fatalf("max bytes = %d", got)
	}
	defaults := Source{}
	if defaults.RefreshInterval() != 5*time.Minute {
		t.Fatalf("default refresh interval = %v", defaults.RefreshInterval())
	}
	if defaults.MaxBytes() != 16<<20 {
		t.Fatalf("default max bytes = %d", defaults.MaxBytes())
	}
}

func TestFreshStoreImportsHistoricalExport(t *testing.T) {
	root := t.TempDir()
	exportDir := filepath.Join(root, "exports")
	if err := os.MkdirAll(filepath.Join(exportDir, "historical"), 0o755); err != nil {
		t.Fatal(err)
	}
	discovered := "http://8.8.8.8:8080\nsocks5://1.1.1.1:1080\nhttps://9.9.9.9:3128\n"
	everAlive := "http://8.8.8.8:8080\nsocks5://1.1.1.1:1080\n"
	everGold := "socks5://1.1.1.1:1080\n"
	for name, body := range map[string]string{
		"all-discovered.txt": discovered,
		"ever-alive.txt":     everAlive,
		"ever-gold.txt":      everGold,
	} {
		if err := os.WriteFile(filepath.Join(exportDir, "historical", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := OpenStore(filepath.Join(root, "data", "proxies.snapshot.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.ImportHistoricalFiles(exportDir); err != nil {
		t.Fatal(err)
	}
	items := store.Snapshot()
	if len(items) != 3 {
		t.Fatalf("expected 3 restored records, got %d", len(items))
	}
	byKey := map[string]ProxyRecord{}
	for _, item := range items {
		byKey[item.Key] = item
	}
	if !byKey["http://8.8.8.8:8080"].EverAlive || byKey["http://8.8.8.8:8080"].EverGold {
		t.Fatalf("unexpected alive history: %+v", byKey["http://8.8.8.8:8080"])
	}
	if !byKey["socks5://1.1.1.1:1080"].EverAlive || !byKey["socks5://1.1.1.1:1080"].EverGold {
		t.Fatalf("unexpected gold history: %+v", byKey["socks5://1.1.1.1:1080"])
	}
	if byKey["https://9.9.9.9:3128"].EverAlive {
		t.Fatalf("discovered-only proxy should not be ever-alive: %+v", byKey["https://9.9.9.9:3128"])
	}
}

// Test source-level bounded harvests to protect 2-core deployments from huge feeds.
func TestSourceCandidateLimit(t *testing.T) {
	if n := (Source{}).CandidateLimit(); n != 2000 {
		t.Fatalf("default cap %d", n)
	}
	if n := (Source{MaxCandidates: 250}).CandidateLimit(); n != 250 {
		t.Fatalf("override cap %d", n)
	}
	if n := (Source{MaxCandidates: 500000}).CandidateLimit(); n != 10000 {
		t.Fatalf("max cap %d", n)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("8.8.8.8:80\n1.1.1.1:81\n9.9.9.9:82\n"))
	}))
	defer server.Close()
	got, err := fetchSource(context.Background(), server.Client(), Source{URL: server.URL, Protocol: "http", Name: "test", MaxCandidates: 2})
	if err != nil || len(got) != 2 {
		t.Fatalf("capped result len=%d err=%v", len(got), err)
	}
}

// Nginx is the only trusted source for an externally observed exit IP.
func TestProbeClientIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/probe/test", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Real-IP", "9.9.9.9")
	if got := probeClientIP(req); got != "9.9.9.9" {
		t.Fatalf("trusted gateway: %q", got)
	}
	req.RemoteAddr = "8.8.8.8:1234"
	if got := probeClientIP(req); got != "8.8.8.8" {
		t.Fatalf("untrusted header: %q", got)
	}
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Real-IP", "192.168.1.1")
	if got := probeClientIP(req); got != "127.0.0.1" {
		t.Fatalf("private header: %q", got)
	}
}
