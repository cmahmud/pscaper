package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
	Protocol       string `json:"protocol"`
	RefreshSeconds int    `json:"refresh_seconds,omitempty"`
	MaxBytesMB     int    `json:"max_bytes_mb,omitempty"`
}

func (s Source) RefreshInterval() time.Duration {
	if s.RefreshSeconds <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(s.RefreshSeconds) * time.Second
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

func fetchSource(ctx context.Context, client *http.Client, src Source) ([]Candidate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
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
	var out []Candidate
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if c, ok := parseCandidate(line, src.Protocol, src.Name); ok {
			out = append(out, c)
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

func validEndpoint(host string, port int) bool {
	if port < 1 || port > 65535 {
		return false
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	} // public-list engine deliberately accepts literal IPs only
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return false
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
