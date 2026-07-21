package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// StatusFile is the name of the status file written to .enola/.
const StatusFile = ".enola-status.json"

// StatusInfo holds the serializable status data.
//
// ToolCounts is the lifetime grand total (accumulated across server restarts);
// SessionCounts is the usage of the current process only (since the last
// reload). Both are persisted so a separate --status invocation, which only
// reads this file, can render both tiers.
type StatusInfo struct {
	PID           int            `json:"pid"`
	StartTime     time.Time      `json:"start_time"`               // current process start (drives Uptime)
	TrackingSince time.Time      `json:"tracking_since,omitempty"` // first-ever start (drives grand-total window)
	RepoPath      string         `json:"repo_path"`
	ToolCounts    map[string]int `json:"tool_counts"`              // lifetime grand total
	SessionCounts map[string]int `json:"session_counts,omitempty"` // since last reload

	// DashboardPort is the localhost port of an HTTP dashboard served by this
	// process (0 if none). It is persisted so a separate --status invocation,
	// which never talks to the running server, can print the dashboard URL.
	//
	// The OSS server serves no dashboard and always leaves this zero; a wrapper
	// binary that does sets it via Tracker.SetDashboardPort. Because both share
	// ~/.enola/usage/, an OSS --status may read a file written by such a wrapper
	// — printing that URL is correct, since it points at the server that is
	// actually running.
	DashboardPort int `json:"dashboard_port,omitempty"`
}

// Tracker accumulates per-repo tool usage counters and manages the on-disk
// status files. A single server can serve many repos over its lifetime, so
// usage is attributed to the repo each call actually operated on (passed to
// OnToolCall) rather than to a single fixed repo.
//
// Each repo gets its own repoState and its own file under ~/.enola/usage/.
type Tracker struct {
	mu            sync.Mutex
	start         time.Time
	dashboardPort int    // localhost HTTP dashboard port (0 if none), persisted per write
	fallbackRepo  string // used when a call reports an empty repo path
	repos         map[string]*repoState
}

// repoState holds one repo's counters. baseline is the lifetime total loaded
// from disk (immutable for the process); session is this process's increments.
// The persisted grand total is baseline + session.
type repoState struct {
	repoPath      string
	filePath      string
	baseline      map[string]int
	session       map[string]int
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

// PersistStartup writes the fallback repo's status file once at startup, so a
// usage file carrying this process's PID, start time and dashboard port exists
// even before the first tool call is recorded. Best-effort.
func (t *Tracker) PersistStartup() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writeLocked(t.stateLocked(t.fallbackRepo))
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
	defer t.mu.Unlock()
	rs := t.stateLocked(repo)
	rs.session[toolName]++
	t.writeLocked(rs)
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
		if err := t.writeLockedErr(rs); err == nil {
			_ = os.Remove(legacyPath(rs.repoPath))
		}
	}
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

// writeLocked persists a repo's counters to its file, best-effort. Caller holds t.mu.
func (t *Tracker) writeLocked(rs *repoState) {
	_ = t.writeLockedErr(rs)
}

// writeLockedErr persists a repo's counters to its file and returns any error,
// so callers that must not act on a failed write (e.g. legacy-file migration)
// can check it. Caller holds t.mu.
func (t *Tracker) writeLockedErr(rs *repoState) error {
	data, err := json.MarshalIndent(t.infoLocked(rs), "", "  ")
	if err != nil {
		return err
	}
	// Ensure the containing directory exists (~/.enola/usage or legacy .enola).
	if err := os.MkdirAll(filepath.Dir(rs.filePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(rs.filePath, data, 0o644)
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

	if ss.Alive {
		fmt.Fprintf(os.Stderr, "Server:    running (PID %d)\n", ss.PID)
		fmt.Fprintf(os.Stderr, "Uptime:    %s\n", formatDuration(time.Since(ss.StartTime)))
		if ss.DashboardPort > 0 {
			fmt.Fprintf(os.Stderr, "Dashboard: http://127.0.0.1:%d (auto-refreshes every 30s)\n", ss.DashboardPort)
		}
	} else {
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
	fmt.Fprintf(os.Stderr, "  %-*s  %8s  %8s\n", maxLen, "tool", "session", "total")
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
	maxLen := len("this session")
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
		maxLen, "this session", sess.TotalCalls, formatDuration(sess.TotalTimeSaved), humanCount(sess.TotalTokensSaved))
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
