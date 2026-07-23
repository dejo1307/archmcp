package status

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// StatusFile is the name of the status file written to .enola/.
const StatusFile = ".enola-status.json"

// StatusInfo is the serializable content of a per-repo usage file.
//
// ToolCounts is the lifetime grand total for that repo, accumulated across
// restarts AND across every enola process on the machine — the file is shared,
// so writers merge their own delta into it rather than overwriting it (see
// Tracker.flush).
//
// The remaining fields describe a single process and are therefore only as good
// as the last writer. They are still persisted for compatibility with older
// binaries, but nothing reads them any more: live process state comes from the
// instance registry (see instance.go), which has one writer per file.
type StatusInfo struct {
	PID           int            `json:"pid"`
	StartTime     time.Time      `json:"start_time"`               // current process start (drives Uptime)
	TrackingSince time.Time      `json:"tracking_since,omitempty"` // first-ever start (drives grand-total window)
	RepoPath      string         `json:"repo_path"`
	ToolCounts    map[string]int `json:"tool_counts"`              // lifetime grand total
	SessionCounts map[string]int `json:"session_counts,omitempty"` // since last reload

	// DashboardPort is the localhost port of the dashboard served by whichever
	// process wrote this file last (0 if none). Kept for compatibility with
	// binaries predating the instance registry; current readers take the port
	// from the registry, which can name every running server rather than one.
	DashboardPort int `json:"dashboard_port,omitempty"`
}

// Tracker accumulates per-repo tool usage counters and manages the on-disk
// status files. A single server can serve many repos over its lifetime, so
// usage is attributed to the repo each call actually operated on (passed to
// OnToolCall) rather than to a single fixed repo.
//
// Each repo gets its own repoState and its own file under ~/.enola/usage/. Those
// files are SHARED with every other enola process on the machine, so the tracker
// only ever adds its own unflushed delta to whatever is on disk, under a
// cross-process lock (see flush). Everything that belongs to this process alone
// — PID, start time, dashboard port, session counts, loaded graph — lives in its
// instance record instead (see instance.go), which has a single writer.
type Tracker struct {
	mu            sync.Mutex
	start         time.Time
	dashboardPort int    // localhost HTTP dashboard port (0 if none)
	frontDoor     bool   // owns the stable dashboard port
	ident         Identity
	fallbackRepo  string // used when a call reports an empty repo path
	repos         map[string]*repoState
	lastTool      string
	lastCallAt    time.Time

	// graphFn reports the caller's current graph state for the instance record.
	// Held as an atomic so Self can call it without the tracker lock — the
	// callback reads the engine, which must never re-enter the tracker.
	graphFn atomic.Pointer[GraphFunc]

	stopHeartbeat chan struct{}
	stopOnce      sync.Once
}

// Identity is the launch context that distinguishes one running server from
// another in the registry: which binary, from which workspace, with which config.
type Identity struct {
	Binary     string // "enola" / "enola-enterprise"
	Version    string
	Licensed   bool // enterprise features active
	ConfigPath string
	WorkDir    string
}

// GraphState is the snapshot of the engine's graph published in the instance
// record, so a reader can tell what this server has actually loaded.
type GraphState struct {
	PrimaryRepo  string
	Repos        []InstanceRepo
	SnapshotID   string
	SnapshotAt   time.Time
	FactCount    int
	InsightCount int
}

// GraphFunc reports the current graph state. It is called without the tracker
// lock held and must not call back into the Tracker.
type GraphFunc func() GraphState

// repoState holds one repo's counters. baseline is the lifetime total loaded
// from disk at first touch; session is this process's increments; flushed is
// how much of session has already been merged into the shared file, so a write
// contributes exactly the unflushed delta and never clobbers another process's
// concurrent increments.
type repoState struct {
	repoPath      string
	filePath      string
	baseline      map[string]int
	session       map[string]int
	flushed       map[string]int
	trackingSince time.Time
}

// NewTracker creates a multi-repo tracker. fallbackRepo (typically the server's
// launch dir) is used to attribute calls that report an empty repo path.
func NewTracker(fallbackRepo string) *Tracker {
	return &Tracker{
		fallbackRepo: canonicalRepoPath(fallbackRepo),
		repos:        make(map[string]*repoState),
	}
}

// SetIdentity records the launch context published in this process's instance
// record. Call it once at startup, before PersistStartup.
func (t *Tracker) SetIdentity(id Identity) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ident = id
}

// SetGraphFunc registers the callback that reports the engine's current graph
// state. Pulling it on demand (rather than pushing after each snapshot) keeps
// the instance record fresh with no extra wiring at the generate_snapshot site.
func (t *Tracker) SetGraphFunc(fn GraphFunc) {
	t.graphFn.Store(&fn)
}

// SetFrontDoor records whether this instance currently owns the stable dashboard
// port, and republishes the record so other dashboards see the change promptly.
func (t *Tracker) SetFrontDoor(v bool) {
	t.mu.Lock()
	t.frontDoor = v
	t.mu.Unlock()
	t.persistInstance()
}

// SetStartTime records the current process start time (drives Uptime).
func (t *Tracker) SetStartTime(tm time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.start = tm
}

// SetDashboardPort records the localhost HTTP dashboard port so it is persisted
// with every status write and can be surfaced by a separate --status process.
func (t *Tracker) SetDashboardPort(p int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dashboardPort = p
}

// PersistStartup publishes this process's instance record and writes the
// fallback repo's usage file, so both exist before the first tool call is
// recorded, and starts the heartbeat that keeps the record fresh while the
// server sits idle. Best-effort.
func (t *Tracker) PersistStartup() {
	t.mu.Lock()
	rs := t.stateLocked(t.fallbackRepo)
	t.mu.Unlock()

	t.flush(rs)
	t.persistInstance()
	t.startHeartbeat()
}

// OnToolCall is the callback registered with the server. repo is the absolute
// path of the repo the call operated on; an empty repo falls back to the
// tracker's fallback repo.
func (t *Tracker) OnToolCall(toolName, repo string) {
	if repo == "" {
		repo = t.fallbackRepo
	}
	repo = canonicalRepoPath(repo)

	t.mu.Lock()
	rs := t.stateLocked(repo)
	rs.session[toolName]++
	t.lastTool = toolName
	t.lastCallAt = time.Now()
	t.mu.Unlock()

	// Both writes happen outside the tracker lock: flush takes a cross-process
	// file lock, and persistInstance calls the graph callback (which reads the
	// engine). Holding t.mu across either would serialize tool calls behind IO.
	t.flush(rs)
	t.persistInstance()
}

// startHeartbeat refreshes the instance record on a fixed interval so an idle
// server still looks alive to readers (LiveInstances treats a long-unrefreshed
// record as stale). Idempotent; stopped by Close.
func (t *Tracker) startHeartbeat() {
	t.mu.Lock()
	if t.stopHeartbeat != nil {
		t.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	t.stopHeartbeat = stop
	t.mu.Unlock()

	go func() {
		tick := time.NewTicker(heartbeatInterval)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				t.persistInstance()
			}
		}
	}()
}

// Close stops the heartbeat, flushes any unwritten counters and removes this
// process's instance record, so the registry reflects the shutdown immediately
// rather than waiting for a reader to reap it. Safe to call more than once, and
// safe to defer even if PersistStartup was never called.
func (t *Tracker) Close() {
	t.stopOnce.Do(func() {
		t.mu.Lock()
		stop := t.stopHeartbeat
		t.stopHeartbeat = nil
		states := make([]*repoState, 0, len(t.repos))
		for _, rs := range t.repos {
			states = append(states, rs)
		}
		start := t.start
		t.mu.Unlock()

		if stop != nil {
			close(stop)
		}
		for _, rs := range states {
			t.flush(rs)
		}
		removeInstance(os.Getpid(), start)
	})
}

// Self returns this process's instance record: its identity, its dashboard, its
// own session counts, and the graph it currently holds. This — never the
// cross-process aggregate — is what a dashboard must use to describe itself.
func (t *Tracker) Self() Instance {
	// Pull graph state before taking the lock: the callback reads the engine.
	var g GraphState
	if fn := t.graphFn.Load(); fn != nil {
		g = (*fn)()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	session := make(map[string]int)
	for _, rs := range t.repos {
		for k, v := range rs.session {
			session[k] += v
		}
	}

	primary := g.PrimaryRepo
	if primary == "" {
		primary = t.fallbackRepo
	}

	return Instance{
		PID:           os.Getpid(),
		StartTime:     t.start,
		Heartbeat:     time.Now(),
		Binary:        t.ident.Binary,
		Version:       t.ident.Version,
		Licensed:      t.ident.Licensed,
		ConfigPath:    t.ident.ConfigPath,
		WorkDir:       t.ident.WorkDir,
		PrimaryRepo:   primary,
		Repos:         g.Repos,
		SnapshotID:    g.SnapshotID,
		SnapshotAt:    g.SnapshotAt,
		FactCount:     g.FactCount,
		InsightCount:  g.InsightCount,
		DashboardPort: t.dashboardPort,
		FrontDoor:     t.frontDoor,
		LastTool:      t.lastTool,
		LastCallAt:    t.lastCallAt,
		SessionCounts: session,
	}
}

// persistInstance republishes this process's registry record. Best-effort: a
// failed write only makes this instance briefly invisible to other dashboards.
func (t *Tracker) persistInstance() {
	_ = writeInstance(t.Self())
}

// GetStatus returns the current status snapshot for a single repo. Caller need
// not hold the lock.
func (t *Tracker) GetStatus(repo string) StatusInfo {
	repo = canonicalRepoPath(repo)
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.infoLocked(t.stateLocked(repo))
}

// stateLocked returns the repoState for repo, lazily creating it and loading
// its baseline (with legacy migration) from disk on first touch. Caller holds t.mu.
func (t *Tracker) stateLocked(repo string) *repoState {
	if rs, ok := t.repos[repo]; ok {
		return rs
	}
	rs := &repoState{
		repoPath: repo,
		baseline: make(map[string]int),
		session:  make(map[string]int),
		flushed:  make(map[string]int),
	}
	fp, err := usagePath(repo)
	if err != nil {
		fp = legacyPath(repo)
	}
	rs.filePath = fp
	t.repos[repo] = rs

	t.loadLocked(rs)
	if rs.trackingSince.IsZero() {
		rs.trackingSince = t.start
	}
	return rs
}

// loadLocked seeds a repoState's baseline from its home file, or migrates a
// legacy in-repo file (adopt totals, write through to home, remove legacy).
// Best-effort. Caller holds t.mu.
func (t *Tracker) loadLocked(rs *repoState) {
	info, _, err := ReadStatus(rs.filePath)
	migrated := false
	if err != nil {
		legacy := legacyPath(rs.repoPath)
		if legacy == rs.filePath {
			return // already using the legacy location; nothing to migrate
		}
		info, _, err = ReadStatus(legacy)
		if err != nil {
			return // nothing to load; fresh start
		}
		migrated = true
	}

	for k, v := range info.ToolCounts {
		rs.baseline[k] = v
	}
	rs.trackingSince = info.TrackingSince
	if migrated {
		// Only remove the legacy file once the write to its new home has
		// actually succeeded. If the write fails (disk full, permission
		// denied, unwritable dir), leave the legacy file intact so the data
		// survives and migration is retried on the next call.
		if err := t.migrateLocked(rs); err == nil {
			_ = os.Remove(legacyPath(rs.repoPath))
		}
	}
}

// migrateLocked writes an adopted legacy total through to the repo's home file.
// It runs on first touch, while t.mu is held, so it cannot go through flush (which
// takes that lock itself). Skipping the cross-process lock is safe here: this path
// only runs when the home file does not exist yet, and a sibling migrating the same
// legacy file would write the very same totals. Caller holds t.mu.
func (t *Tracker) migrateLocked(rs *repoState) error {
	data, err := json.MarshalIndent(t.infoLocked(rs), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(rs.filePath), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(rs.filePath, data, 0o644)
}

// infoLocked builds a StatusInfo (grand total + session) for a repo. Caller holds t.mu.
func (t *Tracker) infoLocked(rs *repoState) StatusInfo {
	total := make(map[string]int, len(rs.baseline)+len(rs.session))
	for k, v := range rs.baseline {
		total[k] = v
	}
	for k, v := range rs.session {
		total[k] += v
	}
	session := make(map[string]int, len(rs.session))
	for k, v := range rs.session {
		session[k] = v
	}
	return StatusInfo{
		PID:           os.Getpid(),
		StartTime:     t.start,
		TrackingSince: rs.trackingSince,
		RepoPath:      rs.repoPath,
		ToolCounts:    total,
		SessionCounts: session,
		DashboardPort: t.dashboardPort,
	}
}

// flush merges this process's unflushed increments into a repo's shared counter
// file. The file is shared with every other enola process on the machine, so the
// whole read-modify-write runs under a cross-process lock and contributes only
// the delta — never this process's own idea of the total, which would silently
// discard a sibling's concurrent increments.
//
// Best-effort: on any failure the delta stays unflushed and is retried on the
// next call, so counts are deferred rather than lost.
func (t *Tracker) flush(rs *repoState) {
	if err := os.MkdirAll(filepath.Dir(rs.filePath), 0o755); err != nil {
		return
	}

	// Take the cross-process lock first, then the tracker lock — always in that
	// order, and never the reverse, so two enola goroutines cannot deadlock.
	// A lock failure is not fatal: we degrade to an unsynchronized merge.
	lk, err := acquireLock(rs.filePath)
	if err != nil {
		log.Printf("[status] warning: could not lock %s: %v (writing unsynchronized)", rs.filePath, err)
	}
	defer lk.release()

	t.mu.Lock()
	delta := make(map[string]int, len(rs.session))
	pending := make(map[string]int, len(rs.session))
	for k, v := range rs.session {
		pending[k] = v
		if d := v - rs.flushed[k]; d > 0 {
			delta[k] = d
		}
	}
	info := t.infoLocked(rs)
	baseline := make(map[string]int, len(rs.baseline))
	for k, v := range rs.baseline {
		baseline[k] = v
	}
	t.mu.Unlock()

	// Re-read the on-disk totals inside the lock so the merge starts from what
	// siblings have already written. A missing file falls back to the baseline
	// this process loaded (covers the first write and legacy migration).
	total := baseline
	if onDisk, _, err := ReadStatus(rs.filePath); err == nil {
		total = onDisk.ToolCounts
		if total == nil {
			total = make(map[string]int)
		}
		// Keep the earliest tracking-since across processes.
		if !onDisk.TrackingSince.IsZero() && (info.TrackingSince.IsZero() || onDisk.TrackingSince.Before(info.TrackingSince)) {
			info.TrackingSince = onDisk.TrackingSince
		}
	}
	for k, v := range delta {
		total[k] += v
	}
	info.ToolCounts = total

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	if err := writeFileAtomic(rs.filePath, data, 0o644); err != nil {
		return
	}

	// The delta is now durable; record it so the next flush does not re-add it.
	t.mu.Lock()
	for k, v := range pending {
		rs.flushed[k] = v
	}
	t.mu.Unlock()
}

// ReadStatus reads and validates a status file at the given path.
func ReadStatus(path string) (StatusInfo, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return StatusInfo{}, false, err
	}
	var info StatusInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return StatusInfo{}, false, err
	}
	// Check if PID is still alive
	alive := isProcessAlive(info.PID)
	return info, alive, nil
}

// ServerSnapshot returns the aggregated cross-repo server view (the same data
// PrintStatus renders), resolving the usage directory itself. It is the exported
// entry point for consumers outside this package, such as the HTTP dashboard.
// A missing/unreadable usage directory yields a zero-value (Found=false) status.
func ServerSnapshot() ServerStatus {
	dir, err := usageDir()
	if err != nil {
		return ServerStatus{}
	}
	return AggregateServer(dir)
}

// PrintStatus prints the MCP server's activity — the historical grand total and
// the current session — aggregated across every repo the server has served. It
// is independent of the current working directory. The per-repo breakdown is
// available via PrintStatusAll (--status --all).
func PrintStatus() {
	dir, err := usageDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "No status available: %v\n", err)
		return
	}
	ss := AggregateServer(dir)
	if !ss.Found {
		fmt.Fprintf(os.Stderr, "No usage recorded yet.\n")
		fmt.Fprintf(os.Stderr, "Start the MCP server, then run with --status (or --status --all).\n")
		return
	}

	fmt.Fprintf(os.Stderr, "\n=== enola MCP Status ===\n")

	switch {
	case len(ss.Instances) > 0:
		printInstances(ss.Instances)
	case ss.Alive:
		fmt.Fprintf(os.Stderr, "Server:    running (PID %d)\n", ss.PID)
		fmt.Fprintf(os.Stderr, "Uptime:    %s\n", formatDuration(time.Since(ss.StartTime)))
		if ss.DashboardPort > 0 {
			fmt.Fprintf(os.Stderr, "Dashboard: http://127.0.0.1:%d (auto-refreshes every 30s)\n", ss.DashboardPort)
		}
	default:
		fmt.Fprintf(os.Stderr, "Server:    not running (was PID %d)\n", ss.PID)
		if !ss.StartTime.IsZero() {
			fmt.Fprintf(os.Stderr, "Started at: %s\n", ss.StartTime.Format("2006-01-02 15:04:05"))
		}
	}

	if !ss.TrackingSince.IsZero() {
		fmt.Fprintf(os.Stderr, "Tracking since: %s\n", ss.TrackingSince.Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(os.Stderr, "Repos tracked: %d\n", ss.Repos)

	printToolUsage(ss.GrandTotal, ss.Session)
	printValue(ss.GrandTotal, ss.Session)
	fmt.Fprintf(os.Stderr, "\n")
}

// printInstances renders every enola MCP server running right now — one row per
// process, with its own dashboard URL. Several agent terminals mean several
// servers, and each has its own graph, so naming them individually is the only
// honest report; the row marked "(this)" is the process printing the table, and
// "(shared)" marks the one currently serving the stable dashboard URL.
func printInstances(instances []Instance) {
	fmt.Fprintf(os.Stderr, "Servers running: %d\n\n", len(instances))

	maxRepo := len("repos")
	for _, inst := range instances {
		if l := len(inst.RepoLabels()); l > maxRepo {
			maxRepo = l
		}
	}

	fmt.Fprintf(os.Stderr, "  %7s  %-*s  %8s  %6s  %s\n", "pid", maxRepo, "repos", "uptime", "calls", "dashboard")
	self := os.Getpid()
	for _, inst := range instances {
		url := inst.URL()
		if url == "" {
			url = "(none)"
		}
		if inst.FrontDoor {
			url += " (shared)"
		}
		tag := ""
		if inst.PID == self {
			tag = " (this)"
		}
		fmt.Fprintf(os.Stderr, "  %7d  %-*s  %8s  %6d  %s%s\n",
			inst.PID, maxRepo, inst.RepoLabels(),
			formatDuration(time.Since(inst.StartTime)), inst.SessionCalls(), url, tag)
	}
}

// printToolUsage renders per-tool call counts in two tiers: session (current
// process, since last reload) and total (lifetime grand total).
func printToolUsage(total, session map[string]int) {
	fmt.Fprintf(os.Stderr, "\nTool Usage:\n")
	if len(total) == 0 {
		fmt.Fprintf(os.Stderr, "  (no tool calls yet)\n")
		return
	}

	// Union of tool names, sorted.
	names := sortedUnion(total, session)

	maxLen := len("tool")
	for _, name := range names {
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}
	// "running" is the sum over the servers alive right now (all of them, not
	// one arbitrary process); "total" is the lifetime figure on disk.
	fmt.Fprintf(os.Stderr, "  %-*s  %8s  %8s\n", maxLen, "tool", "running", "total")
	for _, name := range names {
		fmt.Fprintf(os.Stderr, "  %-*s  %8d  %8d\n", maxLen, name, session[name], total[name])
	}
}

// sortedUnion returns the sorted union of the keys of the given maps.
func sortedUnion(maps ...map[string]int) []string {
	set := make(map[string]struct{})
	for _, m := range maps {
		for k := range m {
			set[k] = struct{}{}
		}
	}
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// printValue renders the estimated time and context (tokens) saved by the
// recorded tool usage. The per-tool table and grand TOTAL are computed on the
// lifetime totals; a trailing line adds the current-session subtotal. Figures
// are estimates from a static per-tool model.
func printValue(total, session map[string]int) {
	if len(total) == 0 {
		return
	}
	rep := ComputeValue(total)

	fmt.Fprintf(os.Stderr, "\nValue Estimate (approximate):\n")

	// Align the tool column to the longest name.
	maxLen := len("running now")
	for _, tv := range rep.Tools {
		if len(tv.Tool) > maxLen {
			maxLen = len(tv.Tool)
		}
	}
	fmt.Fprintf(os.Stderr, "  %-*s  %6s  %12s  %14s\n", maxLen, "tool", "calls", "~time saved", "~tokens saved")
	for _, tv := range rep.Tools {
		fmt.Fprintf(os.Stderr, "  %-*s  %6d  %12s  %14s\n",
			maxLen, tv.Tool, tv.Calls, formatDuration(tv.TimeSaved), humanCount(tv.TokensSaved))
	}
	fmt.Fprintf(os.Stderr, "  %-*s  %6d  %12s  %14s\n",
		maxLen, "TOTAL", rep.TotalCalls, formatDuration(rep.TotalTimeSaved), humanCount(rep.TotalTokensSaved))

	sess := ComputeValue(session)
	fmt.Fprintf(os.Stderr, "  %-*s  %6d  %12s  %14s\n",
		maxLen, "running now", sess.TotalCalls, formatDuration(sess.TotalTimeSaved), humanCount(sess.TotalTokensSaved))
}

// humanCount renders a large count with thousands/millions suffixes (e.g. 1.2M).
func humanCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
