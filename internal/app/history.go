package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// HistoryPoint is a small aggregate snapshot; no proxy endpoints or credentials.
type HistoryPoint struct {
	Time  time.Time `json:"time"`
	Alive int       `json:"alive"`
	Gold  int       `json:"gold"`
	Queue int       `json:"queue"`
}
type HistoryLog struct {
	mu     sync.RWMutex
	path   string
	points []HistoryPoint
	last   time.Time
}

func NewHistoryLog(path string) *HistoryLog {
	h := &HistoryLog{path: path, points: make([]HistoryPoint, 0, 21000)}
	f, err := os.Open(path)
	if err != nil {
		return h
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 8192), 65536)
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for scan.Scan() {
		var p HistoryPoint
		if json.Unmarshal(scan.Bytes(), &p) == nil && p.Time.After(cutoff) && !p.Time.After(time.Now().Add(time.Minute)) {
			h.points = append(h.points, p)
			h.last = p.Time
		}
	}
	return h
}
func (h *HistoryLog) Append(p HistoryPoint) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.last.IsZero() && p.Time.Sub(h.last) < 30*time.Second {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(p)
	_, err = f.Write(append(b, '\n'))
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	h.points = append(h.points, p)
	h.last = p.Time
	if len(h.points) > 22000 {
		return h.compactLocked(p.Time)
	}
	return nil
}
func (h *HistoryLog) compactLocked(now time.Time) error {
	keep := h.points[:0]
	cutoff := now.Add(-7 * 24 * time.Hour)
	for _, p := range h.points {
		if !p.Time.Before(cutoff) {
			keep = append(keep, p)
		}
	}
	h.points = keep
	temp := h.path + ".next"
	f, err := os.OpenFile(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
	if err != nil {
		return err
	}
	buf := bufio.NewWriter(f)
	for _, p := range h.points {
		b, _ := json.Marshal(p)
		if _, err = buf.Write(append(b, '\n')); err != nil {
			break
		}
	}
	if err == nil {
		err = buf.Flush()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		_ = os.Remove(temp)
		return err
	}
	return os.Rename(temp, h.path)
}
func (h *HistoryLog) Range(since time.Time, max int) []HistoryPoint {
	h.mu.RLock()
	defer h.mu.RUnlock()
	idx := 0
	for idx < len(h.points) && h.points[idx].Time.Before(since) {
		idx++
	}
	src := h.points[idx:]
	if max <= 0 {
		max = 500
	}
	if len(src) <= max {
		return append([]HistoryPoint(nil), src...)
	}
	out := make([]HistoryPoint, 0, max)
	for i := 0; i < max; i++ {
		j := i * (len(src) - 1) / (max - 1)
		out = append(out, src[j])
	}
	return out
}
