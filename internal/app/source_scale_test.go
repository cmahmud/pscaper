package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSourceBatchLimitAlsoAppliesToForcedHarvest(t *testing.T) {
	sources := make([]Source, 500)
	for i := range sources {
		sources[i] = Source{Name: fmt.Sprintf("feed%d", i), Protocol: "http", URL: "https://example.com/list.txt"}
	}
	batch := selectSourceBatch(sources, map[string]time.Time{}, nil, time.Now(), true, 120)
	if len(batch) != 120 {
		t.Fatalf("forced harvest exceeded safe batch size: %d", len(batch))
	}
}
func TestScraperRejectsCrossHostOrInsecureRedirects(t *testing.T) {
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted redirect destination should not be contacted")
		_, _ = w.Write([]byte("8.8.8.8:8080"))
	}))
	defer sink.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusFound)
	}))
	defer origin.Close()
	_, status := HarvestSources(context.Background(), []Source{{Name: "unsafe-redirect-test", URL: origin.URL,
		Protocol: "http", MaxCandidates: 20}}, 1)
	if len(status) != 1 || status[0].Error == "" {
		t.Fatalf("malicious redirect accepted: %+v", status)
	}
	if trustedSourceRedirectHost("127.0.0.1") || trustedSourceRedirectHost("metadata.google.internal") {
		t.Fatal("untrusted source redirect host accepted")
	}
}
