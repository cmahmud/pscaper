package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Source struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	FallbackURL    string `json:"fallback_url,omitempty"`
	Protocol       string `json:"protocol"`
	RefreshSeconds int    `json:"refresh_seconds,omitempty"`
	MaxBytesMB     int    `json:"max_bytes_mb,omitempty"`
	MaxCandidates  int    `json:"max_candidates,omitempty"`
}

func (s Source) RefreshInterval() time.Duration {
	if s.RefreshSeconds <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(s.RefreshSeconds) * time.Second
}

// CandidateLimit caps the number of records admitted from any single feed.
// Avoid monopolizing a small VPS with million-line aggregate lists.
func (s Source) CandidateLimit() int {
	if s.MaxCandidates <= 0 {
		return 2000
	}
	if s.MaxCandidates > 10000 {
		return 10000
	}
	return s.MaxCandidates
}

func (s Source) MaxBytes() int64 {
	mb := s.MaxBytesMB
	if mb <= 0 {
		mb = 16
	}
	if mb > 128 {
		mb = 128
	}
	return int64(mb) << 20
}

func LoadSources(path string) ([]Source, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var src []Source
	if err := json.Unmarshal(b, &src); err != nil {
		return nil, err
	}
	out := src[:0]
	for _, s := range src {
		s.Name = strings.TrimSpace(s.Name)
		s.URL = strings.TrimSpace(s.URL)
		s.FallbackURL = strings.TrimSpace(s.FallbackURL)
		s.Protocol = normalizeProtocol(s.Protocol)
		if s.RefreshSeconds < 0 {
			s.RefreshSeconds = 0
		}
		if s.MaxBytesMB < 0 {
			s.MaxBytesMB = 0
		}
		if s.Name != "" && s.URL != "" && s.Protocol != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func HarvestSources(ctx context.Context, sources []Source, concurrency int) (map[string][]Candidate, []SourceStatus) {
	if concurrency < 1 {
		concurrency = 1
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{
		MaxIdleConns: concurrency * 2, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second,
	}}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	mu := sync.Mutex{}
	results := make(map[string][]Candidate)
	statuses := make([]SourceStatus, 0, len(sources))
	for _, src := range sources {
		src := src
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			start := time.Now()
			cands, err := fetchSource(ctx, client, src)
			fetchedAt := time.Now()
			st := SourceStatus{Name: src.Name, LastFetch: fetchedAt, Found: len(cands), DurationMS: time.Since(start).Milliseconds(), RefreshSeconds: int(src.RefreshInterval() / time.Second), NextFetch: fetchedAt.Add(src.RefreshInterval())}
			if err != nil {
				st.Error = err.Error()
			}
			seen := make(map[string]struct{})
			uniq := cands[:0]
			for _, c := range cands {
				if _, ok := seen[c.Key()]; ok {
					continue
				}
				seen[c.Key()] = struct{}{}
				uniq = append(uniq, c)
			}
			st.Unique = len(uniq)
			mu.Lock()
			results[src.Name] = uniq
			statuses = append(statuses, st)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results, statuses
}

// fetchSource retains the original feed as primary. Explicitly configured,
// same-repository HTTPS jsDelivr URLs act as a fallback on transient failures.
// Never fetch an arbitrary untrusted fallback destination.
func fetchSource(ctx context.Context, client *http.Client, src Source) ([]Candidate, error) {
	candidates, err := fetchSourceURL(ctx, client, src, src.URL)
	if err == nil || src.FallbackURL == "" || !trustedFeedFallback(src.URL, src.FallbackURL) {
		return candidates, err
	}
	alternateClient := *client
	alternateClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Hostname() != "cdn.jsdelivr.net" {
			return fmt.Errorf("untrusted alternate feed redirect")
		}
		if len(via) >= 4 {
			return fmt.Errorf("too many alternate redirects")
		}
		return nil
	}
	alternate, alternateErr := fetchSourceURL(ctx, &alternateClient, src, src.FallbackURL)
	if alternateErr == nil {
		return alternate, nil
	}
	return nil, fmt.Errorf("primary: %v; fallback: %w", err, alternateErr)
}

func trustedFeedFallback(primary, fallback string) bool {
	first, err1 := url.Parse(primary)
	next, err2 := url.Parse(fallback)
	if err1 != nil || err2 != nil || first.Scheme != "https" || next.Scheme != "https" ||
		!strings.EqualFold(first.Host, "raw.githubusercontent.com") ||
		!strings.EqualFold(next.Host, "cdn.jsdelivr.net") ||
		first.User != nil || next.User != nil || first.RawQuery != "" || next.RawQuery != "" {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(first.Path, "/"), "/")
	if len(parts) < 5 || strings.Contains(parts[0], ".") || strings.Contains(parts[1], ".") {
		return false
	}
	expected := "/gh/" + parts[0] + "/" + parts[1] + "@" + parts[2] + "/" + strings.Join(parts[3:], "/")
	return next.Path == expected && next.Fragment == ""
}

func fetchSourceURL(ctx context.Context, client *http.Client, src Source, targetURL string) ([]Candidate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pscaper/1.3 public-proxy-validator")
	req.Header.Set("Accept", "text/plain,*/*;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	r := io.LimitReader(resp.Body, src.MaxBytes())
	s := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, 1024*1024)
	// Reservoir sampling reaches addresses beyond the first 2,000 records.
	// Memory stays bounded even when feeds contain hundreds of thousands.
	// Seed changes on each source refresh so repeated fetches explore the feed.
	seedHash := fnv.New64a()
	_, _ = seedHash.Write([]byte(src.Name))
	_, _ = seedHash.Write([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
	rng := rand.New(rand.NewSource(int64(seedHash.Sum64())))
	limit := src.CandidateLimit()
	out := make([]Candidate, 0, limit)
	seen := 0
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		c, ok := parseCandidate(line, src.Protocol, src.Name)
		if !ok {
			continue
		}
		seen++
		if len(out) < limit {
			out = append(out, c)
			continue
		}
		// Algorithm R: uniform bounded sample of all candidates in the downloaded feed.
		if pick := rng.Intn(seen); pick < limit {
			out[pick] = c
		}
	}
	return out, s.Err()
}

func parseCandidate(line, hint, source string) (Candidate, bool) {
	line = strings.TrimSpace(strings.Trim(line, "\"',"))
	proto := normalizeProtocol(hint)
	if strings.Contains(line, "://") {
		u, err := url.Parse(line)
		if err == nil && u.Host != "" {
			p := normalizeProtocol(u.Scheme)
			if p == "" {
				p = proto
			}
			host := u.Hostname()
			port, _ := strconv.Atoi(u.Port())
			if validEndpoint(host, port) && p != "" {
				return Candidate{Host: host, Port: port, Protocol: p, Source: source}, true
			}
		}
	}
	// Accept the first whitespace/comma-delimited token that is host:port.
	fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '\t' || r == ',' || r == ';' || r == '|' })
	for _, f := range fields {
		f = strings.TrimSpace(strings.Trim(f, "[](){}<>\"'"))
		// Some public lists append metadata, e.g. 1.2.3.4:8080:Country.
		// Only apply this shortcut to IPv4 literals; keep IPv6 host:port handling.
		if parts := strings.SplitN(f, ":", 3); len(parts) == 3 && net.ParseIP(parts[0]) != nil {
			if port, err := strconv.Atoi(parts[1]); err == nil && validEndpoint(parts[0], port) && proto != "" {
				return Candidate{Host: parts[0], Port: port, Protocol: proto, Source: source}, true
			}
		}
		host, portStr, err := net.SplitHostPort(f)
		if err != nil {
			idx := strings.LastIndex(f, ":")
			if idx <= 0 {
				continue
			}
			host, portStr = f[:idx], f[idx+1:]
		}
		port, err := strconv.Atoi(portStr)
		if err == nil && validEndpoint(host, port) && proto != "" {
			return Candidate{Host: strings.Trim(host, "[]"), Port: port, Protocol: proto, Source: source}, true
		}
	}
	return Candidate{}, false
}

// Special-purpose ranges which may pass IsGlobalUnicast but cannot act as
// usable public forwarding endpoints. Precompiled prefixes avoid per-line
// allocations when hundreds of thousands of proxy candidates are parsed.
var publicProxyBogons = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // shared CGNAT addresses
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), // TEST-NET-1
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmark networks
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), // IPv6 documentation
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:2::/48"), // IPv6 benchmarking
}

func validEndpoint(host string, port int) bool {
	if port < 1 || port > 65535 {
		return false
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	} // public-list engine uses literal IPs only
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range publicProxyBogons {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func normalizeProtocol(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "http", "https", "socks4", "socks5":
		return s
	case "socks", "socks5h":
		return "socks5"
	default:
		return ""
	}
}
