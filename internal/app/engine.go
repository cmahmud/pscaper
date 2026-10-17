package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type HarvestSummary struct {
	Sources int
	Fetched int
	New     int
	Queued  int
}

type Engine struct {
	cfg       Config
	store     *Store
	validator *Validator
	events    *EventBuffer
	queue     chan string // discovery and routine dead-proxy checks
	urgent    chan string // rechecks of proven-live proxies
	startedAt time.Time
	cycleKick chan struct{}

	schedMu         sync.Mutex
	scheduled       map[string]struct{}
	sourceMu        sync.RWMutex
	sources         []SourceStatus
	sourceFetchMu   sync.Mutex
	sourceLastFetch map[string]time.Time
	exportMu        sync.Mutex
	statsMu         sync.Mutex
	statsAt         time.Time
	statsCache      Stats

	harvests                  atomic.Int64
	cycles                    atomic.Int64
	successes                 atomic.Int64
	failures                  atomic.Int64
	confirming                atomic.Int64
	transientRecoveries       atomic.Int64
	confirmedLosses           atomic.Int64
	newlyAlive                atomic.Int64
	lostAlive                 atomic.Int64
	newlyDiscovered           atomic.Int64
	inFlight                  atomic.Int64
	newThisHarvest            atomic.Int64
	queuedThisHarvest         atomic.Int64
	sourcesFetchedThisHarvest atomic.Int64
	forceHarvest              atomic.Bool
	lastHarvest               atomic.Int64
	lastExport                atomic.Int64
	lastGitPush               atomic.Int64
	lastArchive               atomic.Int64
	lastCycleEnd              atomic.Int64
	gitStatus                 atomic.Value
	cyclePhase                atomic.Value
}

func NewEngine(cfg Config, store *Store) *Engine {
	_ = store.ImportHistoricalFiles(cfg.ExportDir)
	store.InitializeHistory(cfg.GoldScore)
	e := &Engine{
		cfg:             cfg,
		store:           store,
		validator:       NewValidator(cfg),
		events:          NewEventBuffer(512),
		queue:           make(chan string, cfg.QueueSize),
		urgent:          make(chan string, max(cfg.Workers*8, cfg.QueueSize/4)),
		startedAt:       time.Now(),
		cycleKick:       make(chan struct{}, 1),
		scheduled:       make(map[string]struct{}),
		sourceLastFetch: make(map[string]time.Time),
	}
	// Rebuild persisted source effectiveness from true initial-source
	// attribution. Restarts must not erase hard-earned validation evidence.
	origins := make(map[string]*SourceStatus)
	for _, p := range store.Snapshot() {
		name := p.OriginSource
		if name == "" || name == "history" {
			continue
		}
		q := origins[name]
		if q == nil {
			q = &SourceStatus{Name: name}
			origins[name] = q
		}
		q.NovelSeen++
		if p.EverAlive {
			q.NovelConfirmed++
		}
	}
	sources, err := LoadSources(cfg.SourcesFile)
	if err == nil && len(sources) > 0 {
		seen := make(map[string]bool, len(sources))
		for _, src := range sources {
			if seen[src.Name] {
				continue
			}
			seen[src.Name] = true
			q := origins[src.Name]
			if q == nil {
				q = &SourceStatus{Name: src.Name}
			}
			if q.NovelSeen > 0 {
				q.VerifiedYieldPct = 100 * float64(q.NovelConfirmed) / float64(q.NovelSeen)
			}
			e.sources = append(e.sources, *q)
		}
	} else {
		// Keep historical-only fixtures/tests functional without a sources file.
		for _, q := range origins {
			if q.NovelSeen > 0 {
				q.VerifiedYieldPct = 100 * float64(q.NovelConfirmed) / float64(q.NovelSeen)
			}
			e.sources = append(e.sources, *q)
		}
	}
	e.gitStatus.Store("idle")
	e.cyclePhase.Store("starting")
	return e
}

func (e *Engine) Run(ctx context.Context) error {
	if err := StartProbeServers(ctx, e.cfg); err != nil {
		return fmt.Errorf("probe server: %w", err)
	}
	for i := 0; i < e.cfg.Workers; i++ {
		go e.worker(ctx)
	}
	go e.flushLoop(ctx)
	go e.compactLoop(ctx)
	go e.recheckLoop(ctx)
	go func() {
		if err := StartAPIServer(ctx, e.cfg.APIAddr, e); err != nil {
			log.Printf("api: %v", err)
		}
	}()

	// Validation no longer blocks harvesting or export. The source fetch
	// loop is serialized; export uses its own lock and 30s ticker.
	go e.exportLoop(ctx)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(4 * time.Second):
			if err := e.ExportAndPush(ctx); err != nil {
				log.Printf("initial export: %v", err)
			}
		}
	}()
	if e.cfg.Continuous {
		go e.continuousLoop(ctx)
	} else {
		go e.harvestLoop(ctx)
		go func() {
			if err := e.Harvest(ctx); err != nil {
				log.Printf("initial harvest: %v", err)
			}
		}()
	}

	<-ctx.Done()
	_ = e.store.Flush()
	return nil
}

func (e *Engine) worker(ctx context.Context) {
	urgentStreak := 0
	for {
		// Three priority checks followed by a discovery check, if available.
		// Preserve capacity for discovery without allowing bulk feeds to starve
		// rechecks of known-live proxies.
		var key string
		var got bool
		if urgentStreak >= 3 {
			select {
			case key = <-e.queue:
				got = true
				urgentStreak = 0
			default:
			}
		}
		if !got && urgentStreak < 3 {
			select {
			case key = <-e.urgent:
				got = true
				urgentStreak++
			default:
			}
		}
		if !got {
			select {
			case <-ctx.Done():
				return
			case key = <-e.urgent:
				urgentStreak++
			case key = <-e.queue:
				urgentStreak = 0
			}
		}
		if ctx.Err() != nil {
			return
		}
		p, ok := e.store.Get(key)
		if !ok {
			e.unschedule(key)
			continue
		}
		e.inFlight.Add(1)
		res, attempts, confirmed := e.validateWithConfirmation(ctx, p)
		now := time.Now()
		if res.OK {
			e.successes.Add(1)
		} else {
			e.failures.Add(1)
		}
		// The initial failed attempt is real network work, even if the
		// confirming attempt recovers. Counters represent attempts.
		if attempts > 1 {
			if res.OK {
				e.failures.Add(1)
				e.transientRecoveries.Add(1)
			} else {
				e.failures.Add(1)
			}
		}
		if err := e.store.ApplyValidation(key, res.OK, res.Latency, res.ExitIP, res.Error, now, e.cfg.GoldScore); err == nil {
			if updated, found := e.store.Get(key); found {
				if res.OK {
					if !p.EverAlive {
						e.creditNewVerifiedSource(p.OriginSource)
					}
					if !p.Alive {
						e.newlyAlive.Add(1)
					}
					gold := updated.Gold(e.cfg.GoldScore)
					e.events.Add(ProxyEvent{Time: now.UTC(), Address: updated.Address(),
						Protocol: updated.Protocol, Score: updated.Score, LatencyMS: updated.LatencyMS,
						Kind: "verified", Gold: gold, NewlyGold: gold && !p.Gold(e.cfg.GoldScore)})
				} else if p.Alive {
					e.lostAlive.Add(1)
					if confirmed {
						e.confirmedLosses.Add(1)
					}
					e.events.Add(ProxyEvent{Time: now.UTC(), Address: updated.Address(),
						Protocol: updated.Protocol, Score: updated.Score, LatencyMS: updated.LatencyMS, Kind: "lost"})
				}
			}
		}
		e.inFlight.Add(-1)
		e.unschedule(key)
	}
}

// Only recheck previously verified Alive endpoints. An isolated network
// timeout must not immediately demote a proxy, but no unverified endpoint
// becomes Alive and a twice-failed proxy is removed normally.
func (e *Engine) validateWithConfirmation(ctx context.Context, p ProxyRecord) (ValidationResult, int, bool) {
	vctx, cancel := context.WithTimeout(ctx, e.cfg.Timeout+2*time.Second)
	first := e.validator.Validate(vctx, p)
	cancel()
	if first.OK || !p.Alive || ctx.Err() != nil || !retryableValidationFailure(first.Error) {
		return first, 1, false
	}
	e.confirming.Add(1)
	defer e.confirming.Add(-1)
	// Small bounded gap avoids reusing the same transient congestion burst.
	select {
	case <-ctx.Done():
		return first, 1, false
	case <-time.After(400 * time.Millisecond):
	}
	retryCfg := e.cfg
	if retryCfg.Timeout < 8*time.Second {
		retryCfg.Timeout = 8 * time.Second
	}
	confirmCtx, confirmCancel := context.WithTimeout(ctx, retryCfg.Timeout+2*time.Second)
	second := NewValidator(retryCfg).Validate(confirmCtx, p)
	confirmCancel()
	return second, 2, true
}
func retryableValidationFailure(message string) bool {
	msg := strings.ToLower(message)
	if strings.Contains(msg, "unauthenticated") || strings.Contains(msg, "invalid probe") ||
		strings.Contains(msg, "unsupported protocol") || strings.Contains(msg, "invalid challenge") {
		return false
	}
	return true
}

func (e *Engine) enqueue(key string) bool { return e.enqueuePriority(key, false) }

func (e *Engine) enqueuePriority(key string, priority bool) bool {
	e.schedMu.Lock()
	if _, ok := e.scheduled[key]; ok {
		e.schedMu.Unlock()
		return false
	}
	e.scheduled[key] = struct{}{}
	e.schedMu.Unlock()
	target := e.queue
	if priority && e.urgent != nil {
		target = e.urgent
	}
	select {
	case target <- key:
		return true
	default:
		e.unschedule(key)
		return false
	}
}

func (e *Engine) unschedule(key string) {
	e.schedMu.Lock()
	delete(e.scheduled, key)
	e.schedMu.Unlock()
}

func (e *Engine) scheduledCount() int {
	e.schedMu.Lock()
	n := len(e.scheduled)
	e.schedMu.Unlock()
	return n
}

// recoverableRecentlyAlive limits accelerated retries to endpoints with a
// demonstrated recent success and a small number of subsequent failures.
// A retry never makes a proxy Alive until it passes signed validation again.
func (e *Engine) recoverableRecentlyAlive(rec ProxyRecord, now time.Time) bool {
	if rec.Alive || !rec.EverAlive || rec.LastChecked.IsZero() ||
		rec.LastSuccess.IsZero() || rec.ConsecutiveFailures > 3 {
		return false
	}
	age := now.Sub(rec.LastSuccess)
	if age < 0 || age > 4*time.Hour {
		return false
	}
	if !retryableValidationFailure(rec.LastError) {
		return false
	}
	return now.Sub(rec.LastChecked) >= 5*time.Minute
}

func (e *Engine) shouldQueue(rec ProxyRecord, now time.Time) bool {
	if rec.LastChecked.IsZero() {
		return true
	}
	if rec.Alive {
		return now.Sub(rec.LastChecked) >= e.cfg.AliveRecheck
	}
	// A recently working endpoint gets an earlier, bounded opportunity to
	// recover; never relabel an endpoint Alive without a successful check.
	if e.recoverableRecentlyAlive(rec, now) {
		return true
	}
	// Stale repeatedly failing proxies get exponential backoff,
	// while recently refreshed source entries retain the normal retry time.
	delay := e.cfg.DeadRecheck
	if rec.ConsecutiveFailures >= 3 && now.Sub(rec.LastSeen) > 2*time.Hour {
		steps := rec.ConsecutiveFailures - 2
		if steps > 4 {
			steps = 4
		}
		for i := 0; i < steps; i++ {
			delay *= 2
		}
		if delay > 24*time.Hour {
			delay = 24 * time.Hour
		}
	}
	return now.Sub(rec.LastChecked) >= delay
}

func (e *Engine) harvestOnce(ctx context.Context, force bool) (HarvestSummary, error) {
	sources, err := LoadSources(e.cfg.SourcesFile)
	if err != nil {
		return HarvestSummary{}, err
	}
	now := time.Now()
	due := make([]Source, 0, len(sources))
	e.sourceMu.RLock()
	historical := make(map[string]SourceStatus, len(e.sources))
	for _, st := range e.sources {
		historical[st.Name] = st
	}
	e.sourceMu.RUnlock()
	e.sourceFetchMu.Lock()
	for _, src := range sources {
		interval := src.RefreshInterval()
		// Prefer feeds proven to discover new proxies that subsequently pass
		// signed validation. Mere address volume and shared live proxies do
		// not qualify as evidence of source effectiveness.
		if h, ok := historical[src.Name]; ok {
			decision := sourceConfidence(h)
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
		}
		last := e.sourceLastFetch[src.Name]
		if force || last.IsZero() || now.Sub(last) >= interval {
			due = append(due, src)
			e.sourceLastFetch[src.Name] = now
		}
	}
	e.sourceFetchMu.Unlock()
	results, statuses := HarvestSources(ctx, due, e.cfg.SourceConcurrency)
	summary := HarvestSummary{Sources: len(due)}
	newBySource := make(map[string]int)
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic initial source attribution, no map luck
	for _, name := range names {
		cands := results[name]
		summary.Fetched += len(cands)
		for _, c := range cands {
			isNew, rec := e.store.UpsertCandidate(c, now)
			if isNew {
				summary.New++
				newBySource[name]++
				e.newlyDiscovered.Add(1)
			}
			if isNew || e.shouldQueue(rec, now) {
				if rec.Alive {
					if e.enqueuePriority(rec.Key, true) {
						summary.Queued++
					}
				} else if len(e.queue) < cap(e.queue)/2 {
					if e.enqueue(rec.Key) {
						summary.Queued++
					}
				}
			}
		}
	}
	for i := range statuses {
		statuses[i].NewCandidates = newBySource[statuses[i].Name]
	}
	e.mergeSourceStatuses(statuses)
	e.harvests.Add(1)
	e.sourcesFetchedThisHarvest.Store(int64(summary.Sources))
	e.newThisHarvest.Store(int64(summary.New))
	e.queuedThisHarvest.Store(int64(summary.Queued))
	e.lastHarvest.Store(now.Unix())
	return summary, nil
}

func (e *Engine) mergeSourceStatuses(statuses []SourceStatus) {
	if len(statuses) == 0 {
		return
	}
	e.sourceMu.Lock()
	defer e.sourceMu.Unlock()
	byName := make(map[string]SourceStatus, len(e.sources)+len(statuses))
	for _, st := range e.sources {
		byName[st.Name] = st
	}
	for _, st := range statuses {
		old, seen := byName[st.Name]
		st.Fetches = old.Fetches + 1
		if !seen {
			st.Fetches = 1
		}
		st.NovelSeen = old.NovelSeen + int64(st.NewCandidates)
		st.NovelConfirmed = old.NovelConfirmed
		if st.NovelSeen > 0 {
			st.VerifiedYieldPct = 100 * float64(st.NovelConfirmed) / float64(st.NovelSeen)
		}
		byName[st.Name] = st
	}
	e.sources = e.sources[:0]
	for _, st := range byName {
		e.sources = append(e.sources, st)
	}
}

func (e *Engine) creditNewVerifiedSource(source string) {
	if source == "" || source == "history" {
		return
	}
	e.sourceMu.Lock()
	defer e.sourceMu.Unlock()
	for i := range e.sources {
		st := &e.sources[i]
		if st.Name == source {
			st.NovelConfirmed++
			if st.NovelSeen > 0 {
				st.VerifiedYieldPct = 100 * float64(st.NovelConfirmed) / float64(st.NovelSeen)
			}
			return
		}
	}
	// Checks can finish before the initial harvest status merge.
	e.sources = append(e.sources, SourceStatus{Name: source, NovelConfirmed: 1})
}

func (e *Engine) Harvest(ctx context.Context) error {
	_, err := e.harvestOnce(ctx, true)
	return err
}

func (e *Engine) ForceHarvest() {
	e.forceHarvest.Store(true)
	e.KickCycle()
}

func (e *Engine) KickCycle() {
	select {
	case e.cycleKick <- struct{}{}:
	default:
	}
}

// continuousLoop fetches on a configured cadence irrespective of validation
// queue depth. The former waitForDrain design could stall fresh data for
// many minutes while tens of thousands of dead candidates were checked.
func (e *Engine) continuousLoop(ctx context.Context) {
	interval := e.cfg.HarvestInterval
	if interval < time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		e.cyclePhase.Store("harvesting")
		_, err := e.harvestOnce(ctx, e.forceHarvest.Swap(false))
		if err != nil {
			log.Printf("harvest: %v", err)
		}
		e.cyclePhase.Store("validating")
		e.cycles.Add(1)
		e.lastCycleEnd.Store(time.Now().Unix())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-e.cycleKick:
		}
	}
}

func (e *Engine) waitForDrain(ctx context.Context) error {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		if e.scheduledCount() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (e *Engine) harvestLoop(ctx context.Context) {
	t := time.NewTicker(e.cfg.HarvestInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := e.harvestOnce(ctx, false); err != nil {
				log.Printf("harvest: %v", err)
			}
		}
	}
}

func (e *Engine) recheckLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	sweep := func() {
		now := time.Now()
		records := e.store.Snapshot()
		alive := make([]ProxyRecord, 0, 4096)
		recovery := make([]ProxyRecord, 0, e.cfg.Workers*4)
		untested := make([]ProxyRecord, 0, e.cfg.Workers*10)
		dead := make([]ProxyRecord, 0, e.cfg.Workers*2)
		for _, p := range records {
			if !e.shouldQueue(p, now) {
				continue
			}
			switch {
			case p.Alive:
				alive = append(alive, p)
			case e.recoverableRecentlyAlive(p, now):
				if len(recovery) < e.cfg.Workers*4 {
					recovery = append(recovery, p)
				}
			case p.LastChecked.IsZero():
				if len(untested) < e.cfg.Workers*10 {
					untested = append(untested, p)
				}
			default:
				if len(dead) < e.cfg.Workers*2 {
					dead = append(dead, p)
				}
			}
		}
		// Urgent rechecks ordered oldest-first so arbitrary map iteration
		// cannot perpetually postpone some healthy endpoints.
		sort.Slice(alive, func(i, j int) bool { return alive[i].LastChecked.Before(alive[j].LastChecked) })
		for _, p := range alive {
			if ctx.Err() != nil {
				return
			}
			if len(e.urgent) >= cap(e.urgent) {
				break
			}
			e.enqueuePriority(p.Key, true)
		}
		// Recovery budget: recently working proxies that failed confirmation
		// get another chance after five minutes. Keep active rechecks first,
		// and cap recovery work so discovery stays productive.
		sort.Slice(recovery, func(i, j int) bool {
			return recovery[i].LastSuccess.After(recovery[j].LastSuccess)
		})
		maxRecovery := max(12, e.cfg.Workers/2)
		if maxRecovery > len(recovery) {
			maxRecovery = len(recovery)
		}
		for i := 0; i < maxRecovery; i++ {
			if ctx.Err() != nil || len(e.urgent) >= cap(e.urgent)/2 {
				break
			}
			e.enqueuePriority(recovery[i].Key, true)
		}
		// Limit one sweep's admissions; newly discovered records get first
		// chance, with a small dead-proxy exploration budget behind them.
		sort.Slice(untested, func(i, j int) bool { return untested[i].FirstSeen.After(untested[j].FirstSeen) })
		maxNew := e.cfg.Workers * 10
		if maxNew > len(untested) {
			maxNew = len(untested)
		}
		for i := 0; i < maxNew; i++ {
			if ctx.Err() != nil || len(e.queue) >= cap(e.queue)/2 {
				break
			}
			e.enqueue(untested[i].Key)
		}
		maxDead := e.cfg.Workers * 2
		if maxDead > len(dead) {
			maxDead = len(dead)
		}
		// Pick overdue dead endpoints by oldest last check to avoid repeats.
		sort.Slice(dead, func(i, j int) bool { return dead[i].LastChecked.Before(dead[j].LastChecked) })
		for i := 0; i < maxDead; i++ {
			if ctx.Err() != nil || len(e.queue) >= cap(e.queue)/2 {
				break
			}
			e.enqueue(dead[i].Key)
		}
	}
	// Do not fill 15,000 slots before any first-harvest data can flow.
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}

func (e *Engine) compactLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.store.Compact(); err != nil {
				log.Printf("state compact: %v", err)
			}
		}
	}
}

func (e *Engine) flushLoop(ctx context.Context) {
	t := time.NewTicker(e.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.store.Flush(); err != nil {
				log.Printf("db flush: %v", err)
			}
		}
	}
}

func (e *Engine) exportLoop(ctx context.Context) {
	t := time.NewTicker(e.cfg.ExportInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.ExportAndPush(ctx); err != nil {
				log.Printf("export: %v", err)
			}
		}
	}
}

func (e *Engine) ExportAndPush(ctx context.Context) error {
	e.exportMu.Lock()
	defer e.exportMu.Unlock()
	counts, err := e.store.Export(e.cfg.ExportDir, e.cfg.GoldScore)
	if err != nil {
		return err
	}
	now := time.Now()
	e.lastExport.Store(now.Unix())
	stats := e.Stats()
	b, _ := json.MarshalIndent(stats, "", "  ")
	b = append(b, '\n')
	if err := atomicWrite(filepath.Join(e.cfg.ExportDir, "stats.json"), b); err != nil {
		return err
	}
	if e.archiveDue(now) {
		if err := archivePool(e.cfg.ExportDir, now); err != nil {
			log.Printf("archive: %v", err)
		} else {
			e.lastArchive.Store(now.Unix())
		}
	}
	readme := fmt.Sprintf("# pscaper validated proxy pool\n\n## Current pool\n\n- Alive now: %d\n- Gold now: %d\n- HTTP: %d alive / %d gold\n- HTTPS: %d alive / %d gold\n- SOCKS4: %d alive / %d gold\n- SOCKS5: %d alive / %d gold\n\n## Historical pool\n\n- Discovered: %d\n- Ever alive: %d\n- Ever gold: %d\n\n`alive/` is the current passing pool. `gold/` is the current repeatedly verified, higher-scoring subset. `historical/` is append-only and does not remove a proxy just because it later goes offline. `archive/` stores periodic snapshots of the current combined alive/gold pools.\n", counts["alive"], counts["gold"], counts["alive_http"], counts["gold_http"], counts["alive_https"], counts["gold_https"], counts["alive_socks4"], counts["gold_socks4"], counts["alive_socks5"], counts["gold_socks5"], counts["discovered"], counts["ever_alive"], counts["ever_gold"])
	_ = atomicWrite(filepath.Join(e.cfg.ExportDir, "README.md"), []byte(readme))
	if !e.cfg.GitPush {
		e.gitStatus.Store("disabled")
		return nil
	}
	if _, err := os.Stat(filepath.Join(e.cfg.ExportDir, ".git")); err != nil {
		e.gitStatus.Store("not configured")
		return nil
	}
	pushed, err := gitPush(ctx, e.cfg.ExportDir, e.cfg.GitRemote, e.cfg.GitBranch)
	if err != nil {
		e.gitStatus.Store("error: " + truncate(err.Error(), 120))
		return err
	}
	if pushed {
		e.lastGitPush.Store(time.Now().Unix())
		e.gitStatus.Store("pushed")
	} else {
		e.gitStatus.Store("no pool changes")
	}
	return nil
}

func (e *Engine) archiveDue(now time.Time) bool {
	if e.cfg.ArchiveInterval <= 0 {
		return false
	}
	last := e.lastArchive.Load()
	return last == 0 || now.Sub(time.Unix(last, 0)) >= e.cfg.ArchiveInterval
}

func archivePool(dir string, now time.Time) error {
	dayDir := filepath.Join(dir, "archive", now.UTC().Format("2006-01-02"))
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		return err
	}
	stamp := now.UTC().Format("150405")
	for _, kind := range []string{"alive", "gold"} {
		src := filepath.Join(dir, kind, "all.txt")
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		dst := filepath.Join(dayDir, stamp+"-"+kind+".txt")
		if err := atomicWrite(dst, b); err != nil {
			return err
		}
	}
	return nil
}

func gitPush(ctx context.Context, dir, remote, branch string) (bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(cctx, "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := run("add", "-A"); err != nil {
		return false, fmt.Errorf("git add: %s: %w", out, err)
	}
	changed, err := run("diff", "--cached", "--name-only")
	if err != nil {
		return false, fmt.Errorf("git diff: %s: %w", changed, err)
	}
	if strings.TrimSpace(changed) == "" {
		return false, nil
	}
	poolChanged := false
	for _, name := range strings.Split(changed, "\n") {
		name = strings.TrimSpace(name)
		if strings.HasPrefix(name, "alive/") || strings.HasPrefix(name, "gold/") || strings.HasPrefix(name, "historical/") || strings.HasPrefix(name, "archive/") {
			poolChanged = true
			break
		}
	}
	if !poolChanged {
		_, _ = run("reset", "--quiet")
		_, _ = run("checkout", "--", "stats.json", "README.md")
		return false, nil
	}
	msg := "proxy pool update " + time.Now().UTC().Format("2006-01-02T15:04:05Z")
	if out, err := run("commit", "-m", msg); err != nil {
		return false, fmt.Errorf("git commit: %s: %w", out, err)
	}
	if out, err := run("push", remote, "HEAD:"+branch); err != nil {
		return false, fmt.Errorf("git push: %s: %w", out, err)
	}
	return true, nil
}

func (e *Engine) Stats() Stats {
	// Share expensive O(database-size) aggregate scans across the website,
	// exports, and multiple API clients for a short, bounded interval.
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	if !e.statsAt.IsZero() && time.Since(e.statsAt) < 4*time.Second {
		return e.statsCache
	}
	records := e.store.Snapshot()
	st := Stats{
		Protocols:                 map[string]int{},
		AliveProtocols:            map[string]int{},
		GoldProtocols:             map[string]int{},
		Queue:                     len(e.queue) + len(e.urgent),
		UrgentQueue:               len(e.urgent),
		DiscoveryQueue:            len(e.queue),
		Confirming:                e.confirming.Load(),
		TransientRecoveries:       e.transientRecoveries.Load(),
		ConfirmedLosses:           e.confirmedLosses.Load(),
		InFlight:                  e.inFlight.Load(),
		ValidationAttempts:        e.successes.Load() + e.failures.Load(),
		Workers:                   e.cfg.Workers,
		Harvests:                  e.harvests.Load(),
		Cycles:                    e.cycles.Load(),
		NewThisHarvest:            e.newThisHarvest.Load(),
		QueuedThisHarvest:         e.queuedThisHarvest.Load(),
		SourcesFetchedThisHarvest: e.sourcesFetchedThisHarvest.Load(),
		StartedAt:                 e.startedAt,
	}
	if v := e.cyclePhase.Load(); v != nil {
		st.CyclePhase, _ = v.(string)
	}
	var qualityObserved, qualityPassed [3]uint64
	var latencyTotal, latencyCount int64
	sourceSeen := make(map[string]int)
	sourceAlive := make(map[string]int)
	novelAlive := make(map[string]int)
	novelSurvived15 := make(map[string]int)
	for _, p := range records {
		if p.Quality != nil {
			if !p.Quality.FirstAt.IsZero() &&
				(st.QualityFirstAt.IsZero() || p.Quality.FirstAt.Before(st.QualityFirstAt)) {
				st.QualityFirstAt = p.Quality.FirstAt
			}
			for i := 0; i < 3; i++ {
				qualityObserved[i] += uint64(p.Quality.Observed[i])
				qualityPassed[i] += uint64(p.Quality.Passed[i])
			}
		}
		for _, name := range p.Sources {
			sourceSeen[name]++
			if p.Alive {
				sourceAlive[name]++
			}
		}
		// Count only genuinely attributed discoveries, not all the
		// publisher mirrors that happen to contain the same address.
		if p.OriginSource != "" && p.OriginSource != "history" {
			if p.Alive {
				novelAlive[p.OriginSource]++
			}
			if p.Quality != nil && p.Quality.Passed[1] > 0 {
				novelSurvived15[p.OriginSource]++
			}
		}
		st.Total++
		st.Protocols[p.Protocol]++
		if !p.LastChecked.IsZero() {
			st.Checked++
		}
		if p.Alive {
			st.Alive++
			st.AliveProtocols[p.Protocol]++
			if p.LatencyMS > 0 {
				latencyTotal += p.LatencyMS
				latencyCount++
			}
			if time.Since(p.LastChecked) <= 15*time.Minute {
				st.FreshAlive++
			}
		} else {
			st.DeadNow++
		}
		if p.EverAlive || p.Successes > 0 {
			st.EverAlive++
		} else {
			st.NeverAlive++
		}
		if p.EverGold {
			st.EverGold++
		}
		if p.Gold(e.cfg.GoldScore) {
			st.Gold++
			st.GoldProtocols[p.Protocol]++
		}
	}
	st.SurvivalQuality = buildSurvivalCohorts(qualityObserved, qualityPassed)
	for i, h := range survivalHorizons {
		st.SurvivalQuality[i].Mature = !st.QualityFirstAt.IsZero() &&
			time.Since(st.QualityFirstAt) >= h
	}
	st.Successes = e.successes.Load()
	st.Failures = e.failures.Load()
	st.LastHarvest = unixTime(e.lastHarvest.Load())
	st.LastExport = unixTime(e.lastExport.Load())
	st.LastGitPush = unixTime(e.lastGitPush.Load())
	st.LastArchive = unixTime(e.lastArchive.Load())
	st.LastCycleEnd = unixTime(e.lastCycleEnd.Load())
	if v := e.gitStatus.Load(); v != nil {
		st.GitStatus, _ = v.(string)
	}
	e.sourceMu.RLock()
	st.SourceStatus = append([]SourceStatus(nil), e.sources...)
	e.sourceMu.RUnlock()
	for i := range st.SourceStatus {
		key := st.SourceStatus[i].Name
		st.SourceStatus[i].KnownAlive = sourceAlive[key]
		st.SourceStatus[i].NovelAlive = novelAlive[key]
		st.SourceStatus[i].NovelSurvived15 = novelSurvived15[key]
		if st.SourceStatus[i].NovelSeen > 0 {
			st.SourceStatus[i].VerifiedYieldPct = 100 * float64(st.SourceStatus[i].NovelConfirmed) / float64(st.SourceStatus[i].NovelSeen)
		}
		if sourceSeen[key] > 0 {
			st.SourceStatus[i].YieldPct = 100 * float64(sourceAlive[key]) / float64(sourceSeen[key])
		}
	}
	// Feed these measured contributions into the next bounded harvest
	// planning pass. Values are overwritten from the live store on each
	// stats refresh, not accumulated from mirrored publisher mentions.
	e.sourceMu.Lock()
	for i := range e.sources {
		name := e.sources[i].Name
		e.sources[i].NovelAlive = novelAlive[name]
		e.sources[i].NovelSurvived15 = novelSurvived15[name]
	}
	e.sourceMu.Unlock()
	st.SourceFeeds = len(st.SourceStatus)
	now := time.Now()
	for _, src := range st.SourceStatus {
		if src.Error != "" {
			st.SourceErrors++
		}
		if src.NextFetch.IsZero() || !src.NextFetch.After(now) {
			st.SourcesDue++
		}
	}
	sort.Slice(st.SourceStatus, func(i, j int) bool { return st.SourceStatus[i].Name < st.SourceStatus[j].Name })
	if latencyCount > 0 {
		st.AvgLatencyMS = latencyTotal / latencyCount
	}
	e.statsCache = st
	e.statsAt = time.Now()
	return st
}

func (e *Engine) Top(limit int, protocol string, goldOnly bool) []ProxyRecord {
	if limit < 1 {
		limit = 20
	}
	if limit > 1000 {
		limit = 1000
	}
	protocol = normalizeProtocol(protocol)
	r := e.store.Snapshot()
	out := r[:0]
	for _, p := range r {
		if !p.Alive {
			continue
		}
		if protocol != "" && p.Protocol != protocol {
			continue
		}
		if goldOnly && !p.Gold(e.cfg.GoldScore) {
			continue
		}
		out = append(out, p)
	}
	SortRecords(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func unixTime(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}
