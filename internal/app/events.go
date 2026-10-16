package app

import (
	"sync"
	"time"
)

// ProxyEvent is a verified validator outcome, not a synthetic UI animation.
// Records are bounded in memory and cleared on engine restart.
type ProxyEvent struct {
	ID        uint64    `json:"id"`
	Time      time.Time `json:"time"`
	Address   string    `json:"address"`
	Protocol  string    `json:"protocol"`
	Score     float64   `json:"score"`
	LatencyMS int64     `json:"latency_ms"`
	Kind      string    `json:"kind"`
	Gold      bool      `json:"gold"`
	NewlyGold bool      `json:"newly_gold"`
}

type EventBuffer struct {
	mu     sync.RWMutex
	last   uint64
	events []ProxyEvent
	cap    int
}

func NewEventBuffer(size int) *EventBuffer {
	if size < 1 {
		size = 256
	}
	return &EventBuffer{cap: size, events: make([]ProxyEvent, 0, size)}
}
func (b *EventBuffer) Add(evt ProxyEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.last++
	evt.ID = b.last
	if evt.Time.IsZero() {
		evt.Time = time.Now().UTC()
	}
	if len(b.events) == b.cap {
		copy(b.events, b.events[1:])
		b.events[len(b.events)-1] = evt
	} else {
		b.events = append(b.events, evt)
	}
}
func (b *EventBuffer) Since(after uint64, limit int) ([]ProxyEvent, uint64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 {
		limit = 12
	}
	if limit > 40 {
		limit = 40
	}
	out := make([]ProxyEvent, 0, limit)
	// When a browser reconnects after missing the ring, show the freshest
	// verified records instead of replaying hundreds of outdated events.
	if after == 0 || after > b.last || (len(b.events) > 0 && after < b.events[0].ID-1) {
		start := len(b.events) - limit
		if start < 0 {
			start = 0
		}
		out = append(out, b.events[start:]...)
	} else {
		for _, v := range b.events {
			if v.ID > after {
				out = append(out, v)
			}
		}
		if len(out) > limit {
			out = out[len(out)-limit:]
		}
	}
	return out, b.last
}
