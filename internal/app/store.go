package app

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store struct {
	mu          sync.RWMutex
	ioMu        sync.Mutex
	snapshot    string
	journalPath string
	journal     *os.File
	records     map[string]*ProxyRecord
	dirty       map[string]struct{}
}

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	s := &Store{snapshot: path, journalPath: path + ".journal", records: make(map[string]*ProxyRecord), dirty: make(map[string]struct{})}
	if err := s.loadSnapshot(); err != nil {
		return nil, err
	}
	if err := s.loadJournal(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.journalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	s.journal = f
	return s, nil
}

func (s *Store) loadSnapshot() error {
	f, err := os.Open(s.snapshot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	dec := json.NewDecoder(gz)
	var items []ProxyRecord
	if err := dec.Decode(&items); err != nil && err != io.EOF {
		return err
	}
	for i := range items {
		p := items[i]
		normalizeHistory(&p)
		s.records[p.Key] = &p
	}
	return nil
}
func (s *Store) loadJournal() error {
	f, err := os.Open(s.journalPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for sc.Scan() {
		var p ProxyRecord
		if json.Unmarshal(sc.Bytes(), &p) == nil && p.Key != "" {
			cp := p
			normalizeHistory(&cp)
			s.records[p.Key] = &cp
		}
	}
	return sc.Err()
}
func (s *Store) Close() error {
	_ = s.Flush()
	_ = s.Compact()
	if s.journal != nil {
		return s.journal.Close()
	}
	return nil
}

func (s *Store) UpsertCandidate(c Candidate, now time.Time) (bool, ProxyRecord) {
	key := c.Key()
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.records[key]; ok {
		changed := false
		if p.LastSeen.IsZero() || now.Sub(p.LastSeen) >= 30*time.Minute {
			p.LastSeen = now
			changed = true
		}
		if c.Source != "" && !contains(p.Sources, c.Source) {
			p.Sources = append(p.Sources, c.Source)
			sort.Strings(p.Sources)
			changed = true
		}
		if changed {
			s.dirty[key] = struct{}{}
		}
		return false, *p
	}
	p := &ProxyRecord{Key: key, Host: c.Host, Port: c.Port, Protocol: c.Protocol, Sources: []string{c.Source}, FirstSeen: now, LastSeen: now}
	s.records[key] = p
	s.dirty[key] = struct{}{}
	return true, *p
}
func (s *Store) Get(key string) (ProxyRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.records[key]
	if !ok {
		return ProxyRecord{}, false
	}
	return *p, true
}
func (s *Store) ApplyValidation(key string, ok bool, latency time.Duration, exitIP, errText string, now time.Time, goldThreshold float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, exists := s.records[key]
	if !exists {
		return errors.New("proxy not found")
	}
	p.LastChecked = now
	p.RecentOutcomes <<= 1
	if ok {
		p.RecentOutcomes |= 1
	}
	if p.RecentCount < 16 {
		p.RecentCount++
	}
	if ok {
		p.Alive = true
		p.EverAlive = true
		p.Successes++
		p.ConsecutiveFailures = 0
		p.LastSuccess = now
		p.LatencyMS = latency.Milliseconds()
		p.ExitIP = exitIP
		p.LastError = ""
	} else {
		p.Alive = false
		p.Failures++
		p.ConsecutiveFailures++
		p.LastError = truncate(errText, 220)
	}
	p.Score = scoreRecord(p, now)
	if p.Score > p.PeakScore {
		p.PeakScore = p.Score
	}
	if p.Gold(goldThreshold) {
		p.EverGold = true
	}
	s.dirty[key] = struct{}{}
	return nil
}
func normalizeHistory(p *ProxyRecord) {
	if p.Successes > 0 {
		p.EverAlive = true
	}
	if p.Score > p.PeakScore {
		p.PeakScore = p.Score
	}
}

// ImportHistoricalFiles restores append-only export history into the local
// store. This makes a fresh VPS migration able to recover discovered,
// EverAlive, and EverGold state from the export repository even when the old
// runtime snapshot no longer exists.
func (s *Store) ImportHistoricalFiles(dir string) error {
	types := []struct {
		name      string
		everAlive bool
		everGold  bool
	}{
		{"all-discovered.txt", false, false},
		{"discovered.txt", false, false},
		{"ever-alive.txt", true, false},
		{"ever-gold.txt", true, true},
	}
	now := time.Now()
	for _, typ := range types {
		b, err := os.ReadFile(filepath.Join(dir, "historical", typ.name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		s.mu.Lock()
		for _, line := range strings.Split(string(b), "\n") {
			key := strings.TrimSpace(line)
			if key == "" {
				continue
			}
			p, ok := s.records[key]
			if !ok {
				c, parsed := parseCandidate(key, "", "history")
				if !parsed {
					continue
				}
				p = &ProxyRecord{
					Key:       c.Key(),
					Host:      c.Host,
					Port:      c.Port,
					Protocol:  c.Protocol,
					Sources:   []string{"history"},
					FirstSeen: now,
					LastSeen:  now,
				}
				s.records[p.Key] = p
				key = p.Key
			}
			changed := false
			if typ.everAlive && !p.EverAlive {
				p.EverAlive = true
				changed = true
			}
			if typ.everGold && !p.EverGold {
				p.EverGold = true
				changed = true
			}
			if changed || !ok {
				s.dirty[key] = struct{}{}
			}
		}
		s.mu.Unlock()
	}
	return nil
}

// InitializeHistory upgrades v1.0/v1.1 records in-place without discarding state.
// EverAlive is recoverable from Successes. Current Gold records are seeded into
// EverGold; older Gold history is additionally recovered from the export repo by
// the v1.2 upgrade script and preserved by append-only historical exports.
func (s *Store) InitializeHistory(goldThreshold float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, p := range s.records {
		changed := false
		if p.Successes > 0 && !p.EverAlive {
			p.EverAlive = true
			changed = true
		}
		if p.Score > p.PeakScore {
			p.PeakScore = p.Score
			changed = true
		}
		if p.Gold(goldThreshold) && !p.EverGold {
			p.EverGold = true
			changed = true
		}
		if changed {
			s.dirty[key] = struct{}{}
		}
	}
}

func (s *Store) Snapshot() []ProxyRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ProxyRecord, 0, len(s.records))
	for _, p := range s.records {
		out = append(out, *p)
	}
	return out
}
func (s *Store) Count() int { s.mu.RLock(); defer s.mu.RUnlock(); return len(s.records) }

func (s *Store) Flush() error {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	s.mu.Lock()
	if len(s.dirty) == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := make([]ProxyRecord, 0, len(s.dirty))
	for k := range s.dirty {
		if p, ok := s.records[k]; ok {
			batch = append(batch, *p)
		}
	}
	s.dirty = make(map[string]struct{})
	s.mu.Unlock()
	bw := bufio.NewWriterSize(s.journal, 256*1024)
	enc := json.NewEncoder(bw)
	for i := range batch {
		if err := enc.Encode(batch[i]); err != nil {
			return err
		}
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return s.journal.Sync()
}

func (s *Store) Compact() error {
	if err := s.Flush(); err != nil {
		return err
	}
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	items := s.Snapshot()
	tmp := s.snapshot + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	enc := json.NewEncoder(gz)
	err = enc.Encode(items)
	if cerr := gz.Close(); err == nil {
		err = cerr
	}
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, s.snapshot); err != nil {
		return err
	}
	if s.journal != nil {
		_ = s.journal.Close()
	}
	s.journal, err = os.OpenFile(s.journalPath, os.O_CREATE|os.O_TRUNC|os.O_APPEND|os.O_WRONLY, 0o600)
	return err
}

func (s *Store) Export(dir string, goldThreshold float64) (map[string]int, error) {
	records := s.Snapshot()
	SortRecords(records)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for _, sub := range []string{"alive", "gold", "historical"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	protocols := []string{"http", "https", "socks4", "socks5"}
	alive := map[string][]string{}
	gold := map[string][]string{}
	var discovered, everAlive, everGold []string
	for _, p := range records {
		discovered = append(discovered, p.URI())
		if p.EverAlive || p.Successes > 0 {
			everAlive = append(everAlive, p.URI())
		}
		if p.EverGold {
			everGold = append(everGold, p.URI())
		}
		if !p.Alive {
			continue
		}
		alive[p.Protocol] = append(alive[p.Protocol], p.Address())
		if p.Gold(goldThreshold) {
			gold[p.Protocol] = append(gold[p.Protocol], p.Address())
		}
	}
	counts := map[string]int{}
	var allAlive, allGold []string
	for _, proto := range protocols {
		for _, addr := range alive[proto] {
			allAlive = append(allAlive, proto+"://"+addr)
		}
		for _, addr := range gold[proto] {
			allGold = append(allGold, proto+"://"+addr)
		}
		if err := atomicWrite(filepath.Join(dir, "alive", proto+".txt"), []byte(joinLines(alive[proto]))); err != nil {
			return nil, err
		}
		if err := atomicWrite(filepath.Join(dir, "gold", proto+".txt"), []byte(joinLines(gold[proto]))); err != nil {
			return nil, err
		}
		counts["alive_"+proto] = len(alive[proto])
		counts["gold_"+proto] = len(gold[proto])
	}
	if err := atomicWrite(filepath.Join(dir, "alive", "all.txt"), []byte(joinLines(allAlive))); err != nil {
		return nil, err
	}
	if err := atomicWrite(filepath.Join(dir, "gold", "all.txt"), []byte(joinLines(allGold))); err != nil {
		return nil, err
	}

	// Historical files are append-only unions. This deliberately merges any
	// history already present in the Git export repo so upgrades never erase
	// proxies that were previously observed alive/gold but are currently down.
	history := []struct {
		name  string
		lines []string
		key   string
	}{
		{"all-discovered.txt", discovered, "discovered"},
		{"ever-alive.txt", everAlive, "ever_alive"},
		{"ever-gold.txt", everGold, "ever_gold"},
	}
	for _, h := range history {
		path := filepath.Join(dir, "historical", h.name)
		merged, err := mergeHistoricalFile(path, h.lines)
		if err != nil {
			return nil, err
		}
		counts[h.key] = len(merged)
	}
	counts["alive"] = len(allAlive)
	counts["gold"] = len(allGold)
	return counts, nil
}

func mergeHistoricalFile(path string, current []string) ([]string, error) {
	set := make(map[string]struct{}, len(current))
	for _, line := range current {
		line = strings.TrimSpace(line)
		if line != "" {
			set[line] = struct{}{}
		}
	}
	if b, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				set[line] = struct{}{}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	out := make([]string, 0, len(set))
	for line := range set {
		out = append(out, line)
	}
	sort.Strings(out)
	if err := atomicWrite(path, []byte(joinLines(out))); err != nil {
		return nil, err
	}
	return out, nil
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func joinLines(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return strings.Join(v, "\n") + "\n"
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
func ensureDir(path string) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	return os.MkdirAll(path, 0o755)
}
