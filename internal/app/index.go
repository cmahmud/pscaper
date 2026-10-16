package app

import (
	"sync"
	"time"
)

// ProxyIndex holds a bounded-lifetime sorted view of verified live proxies.
// Unlike Store.Snapshot, it does not clone hundreds of thousands of dead
// endpoints for a request that can only return the currently Alive pool.
type proxyIndexView struct {
	at      time.Time
	records []ProxyRecord
}

type ProxyIndex struct {
	store   *Store
	mu      sync.Mutex
	built   time.Time
	records []ProxyRecord
	history []proxyIndexView
	ttl     time.Duration
}

func NewProxyIndex(store *Store, ttl time.Duration) *ProxyIndex {
	if ttl <= 0 {
		ttl = 8 * time.Second
	}
	return &ProxyIndex{store: store, ttl: ttl}
}

func (p *ProxyIndex) Current() (time.Time, []ProxyRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.built.IsZero() && time.Since(p.built) < p.ttl {
		return p.built, p.records
	}
	// Up to six short-lived snapshots keep cursor pagination stable while
	// each snapshot holds only the Alive pool (not all discovered records).
	if !p.built.IsZero() {
		p.history = append(p.history, proxyIndexView{at: p.built, records: p.records})
	}
	cutoff := time.Now().Add(-45 * time.Second)
	retained := p.history[:0]
	for _, view := range p.history {
		if view.at.After(cutoff) {
			retained = append(retained, view)
		}
	}
	p.history = retained
	if len(p.history) > 6 {
		p.history = p.history[len(p.history)-6:]
	}
	p.records = p.store.AliveRecords()
	SortRecords(p.records)
	p.built = time.Now().UTC()
	return p.built, p.records
}

func (p *ProxyIndex) ForCursor(stamp int64) (time.Time, []ProxyRecord, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.built.IsZero() && p.built.UnixNano() == stamp {
		return p.built, p.records, true
	}
	for _, view := range p.history {
		if view.at.UnixNano() == stamp && time.Since(view.at) < 45*time.Second {
			return view.at, view.records, true
		}
	}
	return time.Time{}, nil, false
}
