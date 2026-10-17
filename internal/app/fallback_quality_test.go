package app

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fallbackRoundTrip func(*http.Request) (*http.Response, error)

func (f fallbackRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func sourceResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)),
		Header: make(http.Header)}
}
func TestTrustedFeedFallbackMatchesExactRepository(t *testing.T) {
	primary := "https://raw.githubusercontent.com/fyvri/fresh-proxy-list/archive/storage/classic/http.txt"
	valid := "https://cdn.jsdelivr.net/gh/fyvri/fresh-proxy-list@archive/storage/classic/http.txt"
	if !trustedFeedFallback(primary, valid) {
		t.Fatal("real same-repository fallback rejected")
	}
	bad := []string{
		"http://cdn.jsdelivr.net/gh/fyvri/fresh-proxy-list@archive/storage/classic/http.txt",
		"https://cdn.jsdelivr.net/gh/evil/other@main/http.txt",
		"https://evil.test/gh/fyvri/fresh-proxy-list@archive/storage/classic/http.txt",
		"https://cdn.jsdelivr.net/gh/fyvri/fresh-proxy-list@archive/storage/classic/socks5.txt",
		"https://cdn.jsdelivr.net/gh/fyvri/fresh-proxy-list@archive/storage/classic/http.txt?redirect=https://127.0.0.1",
	}
	for _, url := range bad {
		if trustedFeedFallback(primary, url) {
			t.Fatalf("allowed mismatched fallback %s", url)
		}
	}
}
func TestFeedFallbackOnlyAfterPrimaryFailure(t *testing.T) {
	primary := "https://raw.githubusercontent.com/fyvri/fresh-proxy-list/archive/storage/classic/http.txt"
	alternate := "https://cdn.jsdelivr.net/gh/fyvri/fresh-proxy-list@archive/storage/classic/http.txt"
	var primaryCalls, alternateCalls int
	client := &http.Client{Timeout: time.Second, Transport: fallbackRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "raw.githubusercontent.com" {
			primaryCalls++
			return sourceResponse(404, "not found"), nil
		}
		if req.URL.Host == "cdn.jsdelivr.net" {
			alternateCalls++
			return sourceResponse(200, "8.8.8.8:8080\n1.1.1.1:3128\n"), nil
		}
		t.Fatalf("unexpected host: %s", req.URL.Host)
		return sourceResponse(500, ""), nil
	})}
	feed := Source{Name: "fallback-case", URL: primary, FallbackURL: alternate, Protocol: "http", MaxCandidates: 100}
	proxies, err := fetchSource(context.Background(), client, feed)
	if err != nil || len(proxies) != 2 || primaryCalls != 1 || alternateCalls != 1 {
		t.Fatalf("fallback not used correctly: %d %v primary=%d alternate=%d", len(proxies), err, primaryCalls, alternateCalls)
	}
	client.Transport = fallbackRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "raw.githubusercontent.com" {
			return sourceResponse(200, "8.8.8.8:80\n"), nil
		}
		t.Fatal("should not request backup on primary success")
		return sourceResponse(500, ""), nil
	})
	proxies, err = fetchSource(context.Background(), client, feed)
	if err != nil || len(proxies) != 1 {
		t.Fatalf("healthy primary must remain canonical: %v", err)
	}
}
func TestFallbackRejectsUntrustedAlternateHost(t *testing.T) {
	client := &http.Client{Transport: fallbackRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "raw.githubusercontent.com" {
			return sourceResponse(404, ""), nil
		}
		t.Fatalf("untrusted fallback called: %s", req.URL)
		return sourceResponse(200, "8.8.8.8:8080\n"), nil
	})}
	feed := Source{URL: "https://raw.githubusercontent.com/fyvri/fresh-proxy-list/archive/storage/classic/http.txt",
		FallbackURL: "http://127.0.0.1:8788", Protocol: "http"}
	_, err := fetchSource(context.Background(), client, feed)
	if err == nil {
		t.Fatal("expected primary status error")
	}
}
func TestSourceNovelAliveAndObservedSurvivalAreNotMirrors(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "state.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	_, p := st.UpsertCandidate(Candidate{Host: "8.8.8.8", Port: 8080, Protocol: "http", Source: "origin"}, now)
	st.UpsertCandidate(Candidate{Host: "8.8.8.8", Port: 8080, Protocol: "http", Source: "mirror"}, now)
	_ = st.ApplyValidation(p.Key, true, time.Millisecond, "8.8.4.4", "", now, 85)
	_ = st.ApplyValidation(p.Key, true, time.Millisecond, "8.8.4.4", "", now.Add(16*time.Minute), 85)
	e := &Engine{store: st, cfg: Config{GoldScore: 85}, queue: make(chan string, 4), urgent: make(chan string, 4),
		sources: []SourceStatus{{Name: "origin", NovelSeen: 1, NovelConfirmed: 1}, {Name: "mirror"}}, startedAt: now}
	got := e.Stats()
	for _, x := range got.SourceStatus {
		if x.Name == "origin" && (x.NovelAlive != 1 || x.NovelSurvived15 != 1) {
			t.Fatalf("origin attribution wrong %+v", x)
		}
		if x.Name == "mirror" && (x.NovelAlive != 0 || x.NovelSurvived15 != 0) {
			t.Fatalf("mirror credited with origin %+v", x)
		}
	}
}
