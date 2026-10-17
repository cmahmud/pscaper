package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestQualityCohortsAreObservedNotInferred(t *testing.T) {
	t0 := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	var p ProxyRecord
	updateQualityCohorts(&p, true, t0)
	if p.Quality == nil || p.Quality.Observed != [3]uint32{} {
		t.Fatal("activation must not grant a survival milestone")
	}
	updateQualityCohorts(&p, true, t0.Add(4*time.Minute))
	if p.Quality.Observed != [3]uint32{} {
		t.Fatal("early repeated success must not count")
	}
	updateQualityCohorts(&p, true, t0.Add(6*time.Minute))
	if p.Quality.Passed != [3]uint32{1, 0, 0} || p.Quality.Observed != [3]uint32{1, 0, 0} {
		t.Fatalf("5m milestone wrong: %+v", p.Quality)
	}
	updateQualityCohorts(&p, true, t0.Add(16*time.Minute))
	if p.Quality.Passed != [3]uint32{1, 1, 0} {
		t.Fatalf("15m milestone wrong: %+v", p.Quality)
	}
	updateQualityCohorts(&p, false, t0.Add(20*time.Minute))
	if p.Quality.Observed != [3]uint32{1, 1, 1} || p.Quality.Passed != [3]uint32{1, 1, 0} {
		t.Fatalf("failed before 60m must not be counted as survived: %+v", p.Quality)
	}
	updateQualityCohorts(&p, false, t0.Add(21*time.Minute))
	if p.Quality.Observed != [3]uint32{1, 1, 1} {
		t.Fatal("duplicate failed checks must not add cohorts")
	}
	updateQualityCohorts(&p, true, t0.Add(30*time.Minute))
	updateQualityCohorts(&p, false, t0.Add(31*time.Minute))
	if p.Quality.Observed != [3]uint32{2, 2, 2} || p.Quality.Passed != [3]uint32{1, 1, 0} {
		t.Fatalf("separate activation not measured correctly: %+v", p.Quality)
	}
}
func TestQualityPersistsAcrossStoreReload(t *testing.T) {
	base := filepath.Join(t.TempDir(), "db.json.gz")
	st, err := OpenStore(base)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	_, p := st.UpsertCandidate(Candidate{Host: "8.8.8.8", Port: 8080, Protocol: "http", Source: "test"}, t0)
	for _, v := range []struct {
		age time.Duration
		ok  bool
	}{
		{0, true}, {6 * time.Minute, true}, {16 * time.Minute, true}, {20 * time.Minute, false},
	} {
		if err := st.ApplyValidation(p.Key, v.ok, 100*time.Millisecond, "8.8.4.4", "test", t0.Add(v.age), 85); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(base)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, ok := restored.Get(p.Key)
	if !ok || got.Quality == nil || got.Quality.Observed != [3]uint32{1, 1, 1} ||
		got.Quality.Passed != [3]uint32{1, 1, 0} {
		t.Fatalf("quality observations not restored %+v", got.Quality)
	}
	var observed, passed [3]uint64
	for i := range observed {
		observed[i] = uint64(got.Quality.Observed[i])
		passed[i] = uint64(got.Quality.Passed[i])
	}
	cohorts := buildSurvivalCohorts(observed, passed)
	if cohorts[0].RatePct == nil || *cohorts[0].RatePct != 100 || *cohorts[2].RatePct != 0 {
		t.Fatalf("quality percentages not derived correctly %+v", cohorts)
	}
}
func TestQualitySnapshotDoesNotShareMutableTrackingPointer(t *testing.T) {
	p := &ProxyRecord{Quality: &ProxySurvival{Observed: [3]uint32{2, 2, 2}}}
	copy := snapshotProxy(p)
	p.Quality.Observed[0] = 15
	if copy.Quality.Observed[0] != 2 {
		t.Fatal("snapshot aliased mutable cohort tracking")
	}
}
func TestSourceEvidenceGates(t *testing.T) {
	inputs := []struct {
		st   SourceStatus
		want string
	}{
		{SourceStatus{Fetches: 4, NovelSeen: 300, NovelConfirmed: 30, VerifiedYieldPct: 10}, "learning"},
		{SourceStatus{Fetches: 5, NovelSeen: 299, NovelConfirmed: 20, VerifiedYieldPct: 7}, "learning"},
		{SourceStatus{Fetches: 5, NovelSeen: 400, NovelConfirmed: 9, VerifiedYieldPct: 2.25, NovelAlive: 5, NovelSurvived15: 3}, "prioritize"},
		{SourceStatus{Fetches: 5, NovelSeen: 400, NovelConfirmed: 9, VerifiedYieldPct: 2.25, NovelAlive: 5, NovelSurvived15: 0}, "normal"},
		{SourceStatus{Fetches: 6, NovelSeen: 800, NovelConfirmed: 2, VerifiedYieldPct: .025}, "deprioritize"},
		{SourceStatus{Fetches: 7, NovelSeen: 500, NovelConfirmed: 4, VerifiedYieldPct: .8}, "normal"},
	}
	for _, v := range inputs {
		if got := sourceConfidence(v.st).Recommendation; got != v.want {
			t.Fatalf("source quality %v got %s want %s", v.st, got, v.want)
		}
	}
}
func TestQualityAPIHasNoInventedEarlySurvival(t *testing.T) {
	st, eng, idx, _ := v2Fixture(t)
	defer st.Close()
	mux := http.NewServeMux()
	registerQualityAPI(mux, eng, idx, NewASNDatabase("no-file"))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/api/v2/quality", nil))
	if response.Code != 200 {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	var v struct {
		Status    string           `json:"status"`
		Survival  []SurvivalCohort `json:"survival"`
		Diversity QualityDiversity `json:"diversity"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Status != "collecting" || len(v.Survival) != 3 {
		t.Fatalf("false confidence in sparse samples %+v", v)
	}
	for _, sample := range v.Survival {
		if sample.RatePct != nil || sample.Samples != 0 {
			t.Fatalf("unearned survival evidence: %+v", sample)
		}
	}
	if v.Diversity.Endpoints != 4 || v.Diversity.UniqueIPs != 4 {
		t.Fatalf("bad diversity count: %+v", v.Diversity)
	}
}
