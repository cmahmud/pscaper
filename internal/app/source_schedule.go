package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type feedDue struct {
	source   Source
	last     time.Time
	promoted bool
}

// selectSourceBatch fairly bounds source collection independent of the
// number of registered sources. Reserve up to one-quarter of slots for
// proven productive sources; use the rest for oldest-due exploration.
// Never-fetched feeds remain eligible but cannot monopolize every slot.
func selectSourceBatch(sources []Source, last map[string]time.Time,
	historical map[string]SourceStatus, now time.Time, force bool, budget int) []Source {
	if budget <= 0 {
		budget = 120
	}
	pending := make([]feedDue, 0, len(sources))
	for _, src := range sources {
		interval := src.RefreshInterval()
		st := historical[src.Name]
		decision := sourceConfidence(st)
		switch decision.Recommendation {
		case "prioritize":
			if interval > 20*time.Minute {
				interval /= 2
			}
		case "deprioritize":
			if interval < 2*time.Hour {
				interval *= 2
			}
		}
		fetched := last[src.Name]
		if !force && !fetched.IsZero() && now.Sub(fetched) < interval {
			continue
		}
		pending = append(pending, feedDue{source: src, last: fetched, promoted: decision.Recommendation == "prioritize"})
	}
	less := func(a, b feedDue) bool {
		if !a.last.Equal(b.last) {
			return a.last.Before(b.last)
		}
		return a.source.Name < b.source.Name
	}
	sort.Slice(pending, func(i, j int) bool { return less(pending[i], pending[j]) })
	if len(pending) <= budget {
		out := make([]Source, 0, len(pending))
		for _, p := range pending {
			out = append(out, p.source)
		}
		return out
	}
	reserved := budget / 4
	out := make([]Source, 0, budget)
	selected := make(map[string]struct{}, budget)
	for _, p := range pending {
		if len(out) >= reserved {
			break
		}
		if p.promoted {
			out = append(out, p.source)
			selected[p.source.Name] = struct{}{}
		}
	}
	for _, p := range pending {
		if len(out) >= budget {
			break
		}
		if _, ok := selected[p.source.Name]; ok {
			continue
		}
		out = append(out, p.source)
	}
	return out
}
func fetchStatePath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "source-fetch-state.json")
}
func loadSourceFetchTimes(dir string) map[string]time.Time {
	output := make(map[string]time.Time)
	p := fetchStatePath(dir)
	if p == "" {
		return output
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return output
	}
	if json.Unmarshal(b, &output) != nil {
		return make(map[string]time.Time)
	}
	return output
}
func saveSourceFetchTimes(dir string, values map[string]time.Time) error {
	p := fetchStatePath(dir)
	if p == "" {
		return nil
	}
	b, err := json.Marshal(values)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".source-state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), p); err != nil {
		return fmt.Errorf("commit source state: %w", err)
	}
	return nil
}
