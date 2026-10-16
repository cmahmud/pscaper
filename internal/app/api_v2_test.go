package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func v2Fixture(t *testing.T) (*Store, *Engine, *ProxyIndex, *http.ServeMux) {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "db.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		c := Candidate{Host: "8.8.8." + strconv.Itoa(2+i), Port: 2000 + i, Protocol: "http", Source: "test"}
		_, p := store.UpsertCandidate(c, now)
		if i < 4 {
			for n := 0; n < 3; n++ {
				if err := store.ApplyValidation(p.Key, true, time.Duration(60+i*10)*time.Millisecond, "8.8.4.4", "", now, 85); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	engine := &Engine{store: store, cfg: Config{GoldScore: 85}, startedAt: time.Now()}
	index := NewProxyIndex(store, 8*time.Second)
	mux := http.NewServeMux()
	registerAPIV2(mux, engine, NewASNDatabase("unavailable"), index)
	return store, engine, index, mux
}
func v2Fetch(t *testing.T, mux *http.ServeMux, target string, etag string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}
func TestProxyIndexOnlyCachesVerifiedAlive(t *testing.T) {
	st, _, idx, _ := v2Fixture(t)
	defer st.Close()
	built, items := idx.Current()
	if len(items) != 4 {
		t.Fatalf("expected 4 Alive, got %d", len(items))
	}
	for i := 1; i < len(items); i++ {
		if items[i].Score > items[i-1].Score {
			t.Fatalf("unsorted index %+v", items)
		}
	}
	if built.IsZero() {
		t.Fatal("index never built")
	}
	_, rec := st.UpsertCandidate(Candidate{Host: "9.9.9.9", Port: 9999, Protocol: "http", Source: "test"}, time.Now())
	if err := st.ApplyValidation(rec.Key, true, time.Millisecond, "8.8.4.4", "", time.Now(), 85); err != nil {
		t.Fatal(err)
	}
	_, cached := idx.Current()
	if len(cached) != 4 {
		t.Fatal("8s index cache not used")
	}
	idx.mu.Lock()
	idx.built = time.Now().Add(-10 * time.Second)
	idx.mu.Unlock()
	_, refreshed := idx.Current()
	if len(refreshed) != 5 {
		t.Fatalf("cache refresh wrong %d", len(refreshed))
	}
}
func TestV2PaginationCursorETagAndFields(t *testing.T) {
	st, _, _, mux := v2Fixture(t)
	defer st.Close()
	first := v2Fetch(t, mux, "/api/v2/proxies?limit=2", "")
	if first.Code != 200 {
		t.Fatalf("first page HTTP %d %s", first.Code, first.Body.String())
	}
	var data struct {
		Count   int              `json:"count"`
		Total   int              `json:"total"`
		Next    string           `json:"next_cursor"`
		Proxies []map[string]any `json:"proxies"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Count != 2 || data.Total != 4 || data.Next == "" {
		t.Fatalf("unexpected first page %+v", data)
	}
	if _, ok := data.Proxies[0]["recent_success_pct"]; !ok {
		t.Fatalf("reliability field missing")
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	if other := v2Fetch(t, mux, "/api/v2/proxies?limit=2", etag); other.Code != 304 {
		t.Fatalf("same cached page should return 304, got %d", other.Code)
	}
	next := v2Fetch(t, mux, "/api/v2/proxies?limit=2&cursor="+url.QueryEscape(data.Next), "")
	if next.Code != 200 {
		t.Fatalf("next page error %d: %s", next.Code, next.Body.String())
	}
	var page struct {
		Count   int              `json:"count"`
		Next    *string          `json:"next_cursor"`
		Proxies []map[string]any `json:"proxies"`
	}
	if err := json.Unmarshal(next.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 2 || page.Next != nil {
		t.Fatalf("bad page %+v", page)
	}
	if page.Proxies[0]["url"] == data.Proxies[0]["url"] {
		t.Fatal("page duplication")
	}
	tampered := v2Fetch(t, mux, "/api/v2/proxies?limit=2&min_score=20&cursor="+url.QueryEscape(data.Next), "")
	if tampered.Code != 400 {
		t.Fatalf("different filters should reject cursor: %d", tampered.Code)
	}
}
func TestV2QueryValidation(t *testing.T) {
	st, _, _, mux := v2Fixture(t)
	defer st.Close()
	inputs := []string{
		"/api/v2/proxies?limit=300",
		"/api/v2/proxies?limit=0",
		"/api/v2/proxies?limit=2&limit=5",
		"/api/v2/proxies?min_score=NaN",
		"/api/v2/proxies?min_score=Infinity",
		"/api/v2/proxies?fresh_minutes=-10",
		"/api/v2/proxies?asn=-1",
		"/api/v2/proxies?as_country=UnitedStates",
		"/api/v2/proxies?unrecognized=1",
		"/api/v2/proxies?cursor=forged",
	}
	for _, u := range inputs {
		resp := v2Fetch(t, mux, u, "")
		if resp.Code != 400 {
			t.Errorf("%s unexpectedly HTTP %d", u, resp.Code)
		}
	}
}
func TestV2GoldUsesConfigThreshold(t *testing.T) {
	st, engine, idx, mux := v2Fixture(t)
	defer st.Close()
	engine.cfg.GoldScore = 100
	data := v2Fetch(t, mux, "/api/v2/proxies?tier=gold", "")
	if data.Code != 200 {
		t.Fatalf("error %d", data.Code)
	}
	var v struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(data.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Total > 0 {
		// With a threshold of 100, Gold additionally requires multiple
		// confirmations and fresh checks; validate against actual record rules.
		_, alive := idx.Current()
		expected := 0
		for _, p := range alive {
			if p.Gold(engine.cfg.GoldScore) {
				expected++
			}
		}
		if v.Total != expected {
			t.Fatalf("Gold threshold diverged actual %d vs %d", v.Total, expected)
		}
	}
}
func TestCursorHistoryRetention(t *testing.T) {
	st, _, idx, _ := v2Fixture(t)
	defer st.Close()
	stamp, _ := idx.Current()
	idx.mu.Lock()
	idx.ttl = time.Nanosecond
	idx.mu.Unlock()
	for i := 0; i < 3; i++ {
		time.Sleep(2 * time.Millisecond)
		idx.Current()
	}
	if _, _, ok := idx.ForCursor(stamp.UnixNano()); !ok {
		t.Fatal("cursor should survive repeated index refreshes")
	}
	if _, _, ok := idx.ForCursor(1); ok {
		t.Fatal("unknown cursor should fail")
	}
}
func TestActivityWindowReportsOnlyMeasuredIntervals(t *testing.T) {
	var eng Engine
	var window ActivityWindow
	now := time.Now()
	window.Capture(&eng, now)
	if got := window.Summary(now)["status"]; got != "warming_up" {
		t.Fatal("single point should warm up")
	}
	eng.successes.Store(15)
	eng.failures.Store(5)
	eng.transientRecoveries.Store(3)
	eng.confirmedLosses.Store(1)
	window.Capture(&eng, now.Add(time.Minute))
	summary := window.Summary(now.Add(time.Minute))
	if summary["status"] != "ok" {
		t.Fatal("missing metrics")
	}
	if summary["validation_per_min"] != int64(20) || summary["success_pct"] != float64(75) {
		t.Fatalf("wrong aggregate %+v", summary)
	}
	if summary["transient_recoveries"] != int64(3) {
		t.Fatalf("wrong recovery %+v", summary)
	}
}
func TestLiveSnapshotDoesNotIncludeDeadEndpoints(t *testing.T) {
	st, _, idx, mux := v2Fixture(t)
	defer st.Close()
	_, recs := idx.Current()
	if len(recs) != 4 {
		t.Fatal("setup failed")
	}
	out := v2Fetch(t, mux, "/api/v2/proxies?limit=250", "")
	if strings.Contains(out.Body.String(), "8.8.8.6:2004") {
		t.Fatal("unverified address exposed")
	}
}
