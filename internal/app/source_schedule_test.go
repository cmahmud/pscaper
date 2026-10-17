package app

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSelectSourceBatchBoundsAndFairness(t *testing.T) {
	now := time.Now()
	all := make([]Source, 0, 1000)
	seen := map[string]time.Time{}
	history := map[string]SourceStatus{}
	for i := 0; i < 1000; i++ {
		n := fmt.Sprintf("feed-%04d", i)
		all = append(all, Source{Name: n, URL: "https://example.com/list.txt", Protocol: "http", RefreshSeconds: 3600})
	}
	first := selectSourceBatch(all, seen, history, now, false, 120)
	if len(first) != 120 {
		t.Fatalf("expected strict cap 120, got %d", len(first))
	}
	for _, src := range first {
		seen[src.Name] = now
	}
	second := selectSourceBatch(all, seen, history, now, false, 120)
	if len(second) != 120 {
		t.Fatalf("missing exploration budget %d", len(second))
	}
	firstSet := map[string]bool{}
	for _, src := range first {
		firstSet[src.Name] = true
	}
	for _, src := range second {
		if firstSet[src.Name] {
			t.Fatalf("repeat before due: %s", src.Name)
		}
	}
	// After enough cycles, 1000 distinct configured feeds each get a turn.
	count := len(firstSet)
	for _, src := range second {
		firstSet[src.Name] = true
	}
	count = len(firstSet)
	for tries := 0; tries < 8; tries++ {
		batch := selectSourceBatch(all, seen, history, now, false, 120)
		for _, src := range batch {
			seen[src.Name] = now
			firstSet[src.Name] = true
		}
	}
	if len(firstSet) != 1000 {
		t.Fatalf("starved feeds: only %d/1000 eventually selected", len(firstSet))
	}
	_ = count
}
func TestSelectSourceBatchPrioritizedButDiscoveryProtected(t *testing.T) {
	now := time.Now()
	all := make([]Source, 0, 1000)
	history := map[string]SourceStatus{}
	for i := 0; i < 1000; i++ {
		n := fmt.Sprintf("feed-%04d", i)
		all = append(all, Source{Name: n, URL: "https://example.com", Protocol: "http", RefreshSeconds: 3600})
		if i >= 500 {
			history[n] = SourceStatus{Fetches: 7, NovelSeen: 1000, NovelConfirmed: 200,
				VerifiedYieldPct: 20, NovelAlive: 100, NovelSurvived15: 30}
		}
	}
	batch := selectSourceBatch(all, map[string]time.Time{}, history, now, false, 120)
	if len(batch) != 120 {
		t.Fatal("over budget")
	}
	promote := 0
	newSources := 0
	for _, src := range batch {
		if history[src.Name].NovelSurvived15 > 0 {
			promote++
		} else {
			newSources++
		}
	}
	if promote != 30 || newSources != 90 {
		t.Fatalf("fairness allocation priority=%d new=%d", promote, newSources)
	}
}
func TestSourceFetchStatePersistsAcrossEngineStart(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	wanted := map[string]time.Time{"feed-b": now, "feed-a": now.Add(-time.Hour)}
	if err := saveSourceFetchTimes(dir, wanted); err != nil {
		t.Fatal(err)
	}
	loaded := loadSourceFetchTimes(dir)
	if len(loaded) != 2 || !loaded["feed-b"].Equal(now) {
		t.Fatalf("roundtrip error %+v", loaded)
	}
	if _, err := filepath.Abs(fetchStatePath(dir)); err != nil {
		t.Fatal(err)
	}
	// Missing state is permissible and does not block engine startup.
	if got := loadSourceFetchTimes(t.TempDir()); len(got) != 0 {
		t.Fatal("missing state should be empty")
	}
}
