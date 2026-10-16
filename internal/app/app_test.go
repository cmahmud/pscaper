package app

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestLargeFeedSamplesPastFirstPage(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		// public-range literals; varied hosts guarantee distinct candidates.
		b.WriteString("8." + strconv.Itoa((i/65025)%254+1) + "." + strconv.Itoa((i/255)%255) + "." + strconv.Itoa(i%255) + ":8080\n")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(b.String()))
	}))
	defer server.Close()
	sample, err := fetchSource(context.Background(), server.Client(), Source{URL: server.URL, Name: "big", Protocol: "http", MaxCandidates: 100})
	if err != nil || len(sample) != 100 {
		t.Fatalf("sample=%d err=%v", len(sample), err)
	}
	extended := false
	for _, p := range sample {
		if p.Host == "8.1.0.1" {
			continue
		}
		parts := strings.Split(p.Host, ".")
		x, _ := strconv.Atoi(parts[2])
		if x >= 2 {
			extended = true
			break
		}
	}
	if !extended {
		t.Fatal("sampler never reached beyond early feed lines")
	}
}

func TestHistorySurvivesRestartAndRange(t *testing.T) {
	file := filepath.Join(t.TempDir(), "history.jsonl")
	h := NewHistoryLog(file)
	now := time.Now().Add(-6 * 31 * time.Second)
	for i := 0; i < 6; i++ {
		if err := h.Append(HistoryPoint{Time: now.Add(time.Duration(i) * 31 * time.Second), Alive: i, Gold: i / 2}); err != nil {
			t.Fatal(err)
		}
	}
	second := NewHistoryLog(file)
	got := second.Range(now.Add(-time.Minute), 3)
	if len(got) != 3 || got[0].Alive != 0 || got[2].Alive != 5 {
		t.Fatalf("unexpected persisted history: %+v", got)
	}
	if err := second.Append(HistoryPoint{Time: now.Add(5*31*time.Second + 5*time.Second), Alive: 7}); err != nil {
		t.Fatal(err)
	}
	if n := len(second.Range(now.Add(-time.Minute), 100)); n != 6 {
		t.Fatalf("duplicate interval written: %d", n)
	}
}

func TestSignedProbeRejectsForgedOrReplayedResponses(t *testing.T) {
	key := "test-signing-secret-that-is-long-enough-to-be-independent"
	nonce := "0123456789abcdef0123456789abcdef"
	ip := "8.8.4.4"
	good := []byte(`{"ok":true,"ip":"8.8.4.4","signature":"` + probeSignature(key, nonce, ip) + `"}`)
	result, err := parseProbeBody(good, nonce, key)
	if err != nil || result != ip {
		t.Fatalf("valid signed probe rejected: %v", err)
	}
	for _, candidate := range []struct{ nonce, secret string }{
		{nonce, "wrong-signing-secret"}, {strings.Repeat("f", 32), key},
	} {
		if _, err := parseProbeBody(good, candidate.nonce, candidate.secret); err == nil {
			t.Fatal("forged or replayed probe accepted")
		}
	}
}

func TestDeadProxyBackoffAndGoldFreshness(t *testing.T) {
	now := time.Now()
	e := &Engine{cfg: Config{AliveRecheck: 15 * time.Minute, DeadRecheck: 2 * time.Hour}}
	dead := ProxyRecord{LastChecked: now.Add(-3 * time.Hour), LastSeen: now.Add(-5 * time.Hour), ConsecutiveFailures: 5}
	if e.shouldQueue(dead, now) {
		t.Fatal("repeat failures should back off")
	}
	dead.LastChecked = now.Add(-17 * time.Hour)
	if !e.shouldQueue(dead, now) {
		t.Fatal("long overdue proxy must retry eventually")
	}
	gold := ProxyRecord{Alive: true, Successes: 5, Score: 99, LastChecked: now.Add(-31 * time.Minute)}
	if gold.Gold(80) {
		t.Fatal("stale gold proxy still qualified")
	}
	gold.LastChecked = now
	gold.RecentCount = 5
	gold.RecentOutcomes = 0b00111 // only three recent successes
	if gold.Gold(80) {
		t.Fatal("flaky proxy still qualified")
	}
	gold.RecentOutcomes = 0b11111
	if !gold.Gold(80) {
		t.Fatal("reliable proxy should qualify")
	}
}

func TestAdaptiveFeedYieldScoring(t *testing.T) {
	e := &Engine{}
	e.mergeSourceStatuses([]SourceStatus{{Name: "high", Unique: 100, NewCandidates: 25, KnownAlive: 20}})
	e.mergeSourceStatuses([]SourceStatus{{Name: "high", Unique: 100, NewCandidates: 5, KnownAlive: 5}})
	e.sourceMu.RLock()
	got := e.sources[0]
	e.sourceMu.RUnlock()
	if got.Fetches != 2 || got.NovelSeen != 30 || got.NovelConfirmed != 0 || got.VerifiedYieldPct != 0 {
		t.Fatalf("novelty must not be mistaken for verified quality: %+v", got)
	}
	e.creditNewVerifiedSource("high")
	e.creditNewVerifiedSource("high")
	e.sourceMu.RLock()
	got = e.sources[0]
	e.sourceMu.RUnlock()
	if got.NovelConfirmed != 2 || got.VerifiedYieldPct < 6 || got.VerifiedYieldPct > 7 {
		t.Fatalf("new verified proxy attribution failed: %+v", got)
	}
}

func TestPublicSourceStatisticsUseCatalogWorkingRecords(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	c := Candidate{Host: "8.8.8.8", Port: 9090, Protocol: "http", Source: "test-feed"}
	_, p := store.UpsertCandidate(c, now)
	if err := store.ApplyValidation(p.Key, true, 100*time.Millisecond, "8.8.4.4", "", now, 80); err != nil {
		t.Fatal(err)
	}
	e := &Engine{store: store, sources: []SourceStatus{{Name: "test-feed"}}}
	st := e.Stats()
	if len(st.SourceStatus) != 1 || st.SourceStatus[0].KnownAlive != 1 || st.SourceStatus[0].YieldPct != 100 {
		t.Fatalf("public catalog source quality is inaccurate: %+v", st.SourceStatus)
	}
}

func TestOfflineASNLookupAndDatasetReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "as.tsv.gz")
	put := func(asn string) {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		_, err = gz.Write([]byte("16777216\t16777471\t" + asn + "\tUS\tTEST NETWORK\n"))
		if err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	put("13335")
	db := NewASNDatabase(path)
	if err := db.Reload(); err != nil {
		t.Fatal(err)
	}
	v, ok := db.Lookup("1.0.0.1")
	if !ok || v.ASN != 13335 || v.ASCountry != "US" {
		t.Fatalf("unexpected metadata: %+v", v)
	}
	if _, ok := db.Lookup("127.0.0.1"); ok {
		t.Fatal("unrouted address falsely matched")
	}
	if _, ok := db.Lookup("2001:4860:4860::8888"); ok {
		t.Fatal("IPv6 incorrectly classified against IPv4 ASN map")
	}
}

func TestProxyEventBufferCursorAndGoldTransitions(t *testing.T) {
	buffer := NewEventBuffer(3)
	buffer.Add(ProxyEvent{Address: "8.8.8.8:1080", Protocol: "socks5", Score: 91, Kind: "verified", NewlyGold: true})
	first, last := buffer.Since(0, 10)
	if len(first) != 1 || last != 1 || !first[0].NewlyGold {
		t.Fatalf("unexpected initial event: %+v", first)
	}
	buffer.Add(ProxyEvent{Address: "1.1.1.1:8080", Protocol: "https", Score: 78, Kind: "verified"})
	buffer.Add(ProxyEvent{Address: "9.9.9.9:1080", Protocol: "socks5", Score: 0, Kind: "lost"})
	newer, last := buffer.Since(1, 10)
	if len(newer) != 2 || newer[1].Kind != "lost" || last != 3 {
		t.Fatalf("cursor failed %+v %d", newer, last)
	}
	buffer.Add(ProxyEvent{Address: "4.4.4.4:443", Protocol: "https", Score: 82, Kind: "verified"})
	recent, last := buffer.Since(0, 2)
	if len(recent) != 2 || recent[0].ID != 3 || recent[1].ID != 4 || last != 4 {
		t.Fatalf("bounded ring failed %+v", recent)
	}
	restarted := NewEventBuffer(3)
	restarted.Add(ProxyEvent{Address: "9.9.9.9:80", Kind: "verified"})
	reset, last := restarted.Since(999, 3)
	if len(reset) != 1 || last != 1 {
		t.Fatalf("restarting server must recover from stale cursor: %+v", reset)
	}
}

func TestPriorityQueuePreservesRechecksWithDiscoveryBacklog(t *testing.T) {
	e := &Engine{
		queue: make(chan string, 2), urgent: make(chan string, 2),
		scheduled: make(map[string]struct{}),
	}
	if !e.enqueue("bulk-a") || !e.enqueue("bulk-b") {
		t.Fatal("could not stage discovery backlog")
	}
	if e.enqueue("bulk-c") {
		t.Fatal("overflowing discovery queue should fail")
	}
	if !e.enqueuePriority("known-working", true) {
		t.Fatal("live recheck blocked by discovery backlog")
	}
	if got := <-e.urgent; got != "known-working" {
		t.Fatalf("wrong priority entry: %s", got)
	}
	if e.scheduledCount() != 3 {
		t.Fatal("queue dedup map corrupted")
	}
}

func TestRetryableValidationFailurePolicy(t *testing.T) {
	for _, v := range []string{
		"context deadline exceeded", "dial tcp: i/o timeout", "connection reset by peer",
		"connection refused", "EOF",
	} {
		if !retryableValidationFailure(v) {
			t.Fatalf("transient rejection not retried: %s", v)
		}
	}
	for _, v := range []string{
		"unauthenticated probe response", "invalid probe signature", "unsupported protocol",
	} {
		if retryableValidationFailure(v) {
			t.Fatalf("unsafe authentication failure retried: %s", v)
		}
	}
}

func TestNovelOriginPersistsAndRecoveredFeedGetsNoFalseNovelty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.json.gz")
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	c := Candidate{Host: "8.8.8.8", Port: 8080, Protocol: "http", Source: "new-feed"}
	fresh, p := st.UpsertCandidate(c, time.Now())
	if !fresh || p.OriginSource != "new-feed" {
		t.Fatalf("incorrect novel source %+v", p)
	}
	st.UpsertCandidate(Candidate{Host: "8.8.8.8", Port: 8080, Protocol: "http", Source: "mirror"}, time.Now())
	got, _ := st.Get(p.Key)
	if got.OriginSource != "new-feed" {
		t.Fatal("mirror incorrectly claimed original discovery")
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	again, ok := loaded.Get(p.Key)
	if !ok || again.OriginSource != "new-feed" {
		t.Fatalf("source attribution lost across restart: %+v", again)
	}
}

func TestConfirmedRetryPreservesPreviouslyAliveProxy(t *testing.T) {
	var attempts atomic.Int64
	secret := "unit-test-signing-key-more-than-32-characters"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "temporary upstream failure", http.StatusServiceUnavailable)
			return
		}
		nonce := r.URL.Query().Get("nonce")
		payload, _ := json.Marshal(map[string]any{
			"ok": true, "ip": "8.8.4.4", "signature": probeSignature(secret, nonce, "8.8.4.4"),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	cfg := Config{Timeout: 2 * time.Second, ProbeHost: "127.0.0.1",
		ProbeHTTPPort: 8787, ProbeSigningKey: secret, ProbeToken: "test"}
	engine := &Engine{cfg: cfg, validator: NewValidator(cfg)}
	p := ProxyRecord{Alive: true, Host: host, Port: port, Protocol: "http"}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	result, count, confirmed := engine.validateWithConfirmation(ctx, p)
	if !result.OK || count != 2 || !confirmed || attempts.Load() != 2 {
		t.Fatalf("transient failure should be recovered: %+v count=%d confirmed=%t attempts=%d",
			result, count, confirmed, attempts.Load())
	}
	if engine.confirming.Load() != 0 {
		t.Fatal("in-progress suspect counter leaked")
	}
}

func TestUnsignedProbeDoesNotReceiveConfirmationRetry(t *testing.T) {
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		_, _ = w.Write([]byte(`{"ok":true,"ip":"8.8.4.4","signature":"abcd"}`))
	}))
	defer srv.Close()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	cfg := Config{Timeout: 2 * time.Second, ProbeHost: "127.0.0.1",
		ProbeHTTPPort: 8787, ProbeSigningKey: "unit-test-signing-key-more-than-32-characters", ProbeToken: "test"}
	e := &Engine{cfg: cfg, validator: NewValidator(cfg)}
	result, n, confirmed := e.validateWithConfirmation(context.Background(),
		ProxyRecord{Alive: true, Host: host, Port: port, Protocol: "http"})
	if result.OK || n != 1 || confirmed || attempts.Load() != 1 {
		t.Fatalf("invalid signature must not be retried: %+v %d %t", result, n, confirmed)
	}
}

func TestHarvestNovelCountersNotInflatedByMirror(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("8.8.8.8:8080\n1.1.1.1:3128\n"))
	}))
	defer srv.Close()
	temp := t.TempDir()
	sources := []Source{
		{Name: "feed-a", URL: srv.URL, Protocol: "http"},
		{Name: "feed-b", URL: srv.URL, Protocol: "http"},
	}
	data, _ := json.Marshal(sources)
	path := filepath.Join(temp, "sources.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(temp, "state.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := Config{SourcesFile: path, SourceConcurrency: 2, QueueSize: 10, Workers: 2,
		DeadRecheck: time.Hour, AliveRecheck: time.Minute}
	e := &Engine{cfg: cfg, store: store, queue: make(chan string, 10), urgent: make(chan string, 4),
		scheduled: make(map[string]struct{}), sourceLastFetch: make(map[string]time.Time)}
	summary, err := e.harvestOnce(context.Background(), false)
	if err != nil || summary.New != 2 {
		t.Fatalf("unexpected harvest %+v %v", summary, err)
	}
	e.sourceMu.RLock()
	defer e.sourceMu.RUnlock()
	totalNovel := 0
	for _, st := range e.sources {
		if st.Unique != 2 {
			t.Fatalf("source got wrong unique sample count: %+v", st)
		}
		totalNovel += st.NewCandidates
	}
	if totalNovel != 2 {
		t.Fatalf("mirror inflated actual novel discoveries: %d", totalNovel)
	}
}

func TestContinuousHarvestDoesNotWaitForBackloggedValidation(t *testing.T) {
	temp := t.TempDir()
	src := filepath.Join(temp, "empty_sources.json")
	if err := os.WriteFile(src, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(temp, "db.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := Config{SourcesFile: src, HarvestInterval: time.Hour, QueueSize: 4, Workers: 2}
	e := &Engine{
		cfg: cfg, store: store, queue: make(chan string, 4), urgent: make(chan string, 4),
		scheduled:       map[string]struct{}{"never-drained": {}},
		sourceLastFetch: make(map[string]time.Time), cycleKick: make(chan struct{}, 1),
	}
	e.queue <- "never-drained" // deliberate backlog with no workers
	e.cyclePhase.Store("starting")
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { defer close(finished); e.continuousLoop(ctx) }()
	deadline := time.After(2 * time.Second)
	for e.cycles.Load() == 0 {
		select {
		case <-deadline:
			cancel()
			<-finished
			t.Fatal("harvest cycle blocked by validation backlog")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if e.scheduledCount() != 1 {
		t.Fatal("test backlog drained unexpectedly")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("harvest did not shut down")
	}
}

func TestPersistedOriginEffectivenessSurvivesNewEngine(t *testing.T) {
	tmp := t.TempDir()
	store, err := OpenStore(filepath.Join(tmp, "db.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first, p := store.UpsertCandidate(Candidate{Host: "8.8.4.4", Port: 8080, Protocol: "http", Source: "first-feed"}, now)
	if !first {
		t.Fatal("missing first candidate")
	}
	_, _ = store.UpsertCandidate(Candidate{Host: "1.1.1.1", Port: 80, Protocol: "http", Source: "first-feed"}, now)
	if err := store.ApplyValidation(p.Key, true, time.Millisecond, "8.8.4.4", "", now, 80); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(filepath.Join(tmp, "db.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	cfg := Config{Workers: 2, QueueSize: 4, ExportDir: filepath.Join(tmp, "exports")}
	e := NewEngine(cfg, restored)
	e.sourceMu.RLock()
	defer e.sourceMu.RUnlock()
	var found bool
	for _, src := range e.sources {
		if src.Name == "first-feed" {
			found = true
			if src.NovelSeen != 2 || src.NovelConfirmed != 1 || src.VerifiedYieldPct != 50 {
				t.Fatalf("restarted source-quality attribution incorrect: %+v", src)
			}
		}
	}
	if !found {
		t.Fatal("source quality not restored")
	}
}

func TestRecentlyAliveFailureRecoveryScheduling(t *testing.T) {
	now := time.Now()
	e := &Engine{cfg: Config{AliveRecheck: 15 * time.Minute, DeadRecheck: 2 * time.Hour}}
	p := ProxyRecord{
		Alive: false, EverAlive: true, LastChecked: now.Add(-6 * time.Minute),
		LastSuccess: now.Add(-20 * time.Minute), LastError: "context deadline exceeded",
		ConsecutiveFailures: 2,
	}
	if !e.recoverableRecentlyAlive(p, now) || !e.shouldQueue(p, now) {
		t.Fatal("recently successful proxy with two failures should receive a recovery check")
	}
	p.LastChecked = now.Add(-3 * time.Minute)
	if e.shouldQueue(p, now) {
		t.Fatal("recovering too frequently")
	}
	p.LastChecked = now.Add(-6 * time.Minute)
	p.LastSuccess = now.Add(-5 * time.Hour)
	if e.recoverableRecentlyAlive(p, now) || e.shouldQueue(p, now) {
		t.Fatal("aged-out proxy must use normal dead recheck cadence")
	}
	p.LastSuccess = now.Add(-20 * time.Minute)
	p.LastError = "unauthenticated probe signature"
	if e.recoverableRecentlyAlive(p, now) || e.shouldQueue(p, now) {
		t.Fatal("invalid authentication must never trigger accelerated recovery")
	}
	p.LastError = "network timeout"
	p.ConsecutiveFailures = 4
	if e.recoverableRecentlyAlive(p, now) || e.shouldQueue(p, now) {
		t.Fatal("repeatedly failing proxy must obey normal backoff")
	}
}

func TestStatsCacheRefreshesWithoutChangingDataSemantics(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "db.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	e := &Engine{store: store, cfg: Config{GoldScore: 80}, startedAt: time.Now()}
	before := e.Stats()
	if before.Total != 0 {
		t.Fatalf("expected empty store: %+v", before)
	}
	_, _ = store.UpsertCandidate(Candidate{Host: "1.1.1.1", Port: 8080, Protocol: "http", Source: "test"}, time.Now())
	cached := e.Stats()
	if cached.Total != 0 {
		t.Fatal("aggregate cache not used for brief interval")
	}
	e.statsMu.Lock()
	e.statsAt = time.Now().Add(-10 * time.Second)
	e.statsMu.Unlock()
	fresh := e.Stats()
	if fresh.Total != 1 {
		t.Fatal("aggregate cache failed to refresh")
	}
}
