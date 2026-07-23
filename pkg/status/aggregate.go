package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RepoUsage is one repo's lifetime totals within an aggregate view.
type RepoUsage struct {
	RepoPath      string
	Counts        map[string]int // lifetime grand total
	TrackingSince time.Time
}

// Aggregate is the cross-repo rollup of all per-repo usage files.
type Aggregate struct {
	Repos         []RepoUsage    // sorted by tokens saved, descending
	Combined      map[string]int // summed grand totals across all repos
	TrackingSince time.Time      // earliest across all repos
}

// AggregateUsage reads every per-repo usage file in dir and sums their lifetime
// totals. It is best-effort: unreadable/malformed files are skipped. A missing
// directory yields an empty aggregate with no error.
func AggregateUsage(dir string) (Aggregate, error) {
	var agg Aggregate
	agg.Combined = make(map[string]int)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return agg, nil
		}
		return agg, err
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var info StatusInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		agg.Repos = append(agg.Repos, RepoUsage{
			RepoPath:      info.RepoPath,
			Counts:        info.ToolCounts,
			TrackingSince: info.TrackingSince,
		})
		for k, v := range info.ToolCounts {
			agg.Combined[k] += v
		}
		if !info.TrackingSince.IsZero() && (agg.TrackingSince.IsZero() || info.TrackingSince.Before(agg.TrackingSince)) {
			agg.TrackingSince = info.TrackingSince
		}
	}

	// Sort repos by estimated tokens saved, descending (biggest value first).
	sort.Slice(agg.Repos, func(i, j int) bool {
		return ComputeValue(agg.Repos[i].Counts).TotalTokensSaved >
			ComputeValue(agg.Repos[j].Counts).TotalTokensSaved
	})

	return agg, nil
}

// ServerStatus is the aggregated view of enola activity across every repo that
// has been served: the historical grand total plus what the currently-running
// servers have done. This is what plain --status renders — it is not tied to any
// cwd.
//
// Several servers commonly run at once (one per agent terminal), so Instances
// is the authoritative list and the scalar PID/StartTime/DashboardPort/Alive
// fields describe only the *primary* instance — this process when the caller is
// itself a server, otherwise the most recently started live one. Anything that
// must describe a specific process (a dashboard naming itself) should read
// Tracker.Self or Instances rather than these.
type ServerStatus struct {
	GrandTotal    map[string]int // sum of ToolCounts across all repos (lifetime)
	Session       map[string]int // sum of SessionCounts across live instances
	Instances     []Instance     // every server running right now, oldest first
	StartTime     time.Time      // primary instance's start time
	PID           int            // primary instance's PID
	DashboardPort int            // primary instance's dashboard port (0 if none)
	Alive         bool           // whether any server is running
	TrackingSince time.Time      // earliest TrackingSince across all repos
	Repos         int            // number of repos with recorded usage
	Found         bool           // false if no usage files exist
}

// AggregateServer collapses all per-repo usage files in dir into one server
// view, and overlays the live-instance registry.
//
// The grand total sums every usage file. Live process state — which servers are
// running, their PIDs, dashboard ports and session counts — comes from the
// registry (see instance.go), because the usage files are shared by every
// process and their per-process fields are whatever the last writer happened to
// stamp. When the registry is empty (no server running, or records written by an
// older build) it falls back to the previous heuristic: treat the file with the
// newest StartTime as the server, and count sessions only from files sharing it.
// Best-effort: unreadable/malformed files are skipped.
func AggregateServer(dir string) ServerStatus {
	ss := ServerStatus{
		GrandTotal: make(map[string]int),
		Session:    make(map[string]int),
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ss
	}

	var infos []StatusInfo
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var info StatusInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		infos = append(infos, info)
	}
	if len(infos) == 0 {
		return ss
	}
	ss.Found = true
	ss.Repos = len(infos)

	// Lifetime totals and the tracking window come from the shared usage files.
	for _, info := range infos {
		for k, v := range info.ToolCounts {
			ss.GrandTotal[k] += v
		}
		if !info.TrackingSince.IsZero() && (ss.TrackingSince.IsZero() || info.TrackingSince.Before(ss.TrackingSince)) {
			ss.TrackingSince = info.TrackingSince
		}
	}

	// Live process state comes from the registry when it has anything to say.
	if ss.Instances = LiveInstances(); len(ss.Instances) > 0 {
		for _, inst := range ss.Instances {
			for k, v := range inst.SessionCounts {
				ss.Session[k] += v
			}
		}
		primary := primaryInstance(ss.Instances)
		ss.PID = primary.PID
		ss.StartTime = primary.StartTime
		ss.DashboardPort = primary.DashboardPort
		ss.Alive = true
		return ss
	}

	// No registry: fall back to the newest-StartTime heuristic over usage files.
	for _, info := range infos {
		if info.StartTime.After(ss.StartTime) {
			ss.StartTime = info.StartTime
			ss.PID = info.PID
			ss.DashboardPort = info.DashboardPort
		}
	}
	for _, info := range infos {
		if info.StartTime.Equal(ss.StartTime) {
			for k, v := range info.SessionCounts {
				ss.Session[k] += v
			}
		}
	}

	ss.Alive = isProcessAlive(ss.PID)
	return ss
}

// primaryInstance picks the instance the scalar ServerStatus fields describe:
// this process when the caller is itself a running server (so a server never
// reports a sibling as itself), otherwise the most recently started one.
// instances must be non-empty and sorted oldest-first, as LiveInstances returns.
func primaryInstance(instances []Instance) Instance {
	self := os.Getpid()
	for _, inst := range instances {
		if inst.PID == self {
			return inst
		}
	}
	return instances[len(instances)-1]
}

// PrintStatusAll renders the cross-repo aggregate to stderr. Because the stats
// live under ~/.enola/usage/, this works from any directory and includes repos
// whose working copies have since been deleted.
func PrintStatusAll() {
	dir, err := usageDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "No usage data available: %v\n", err)
		return
	}
	agg, err := AggregateUsage(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No usage data available: %v\n", err)
		return
	}

	fmt.Fprintf(os.Stderr, "\n=== enola MCP Status — all repos ===\n")
	if len(agg.Repos) == 0 {
		fmt.Fprintf(os.Stderr, "  (no usage recorded yet)\n\n")
		return
	}

	fmt.Fprintf(os.Stderr, "Repos:          %d\n", len(agg.Repos))
	if !agg.TrackingSince.IsZero() {
		fmt.Fprintf(os.Stderr, "Tracking since: %s\n", agg.TrackingSince.Format("2006-01-02 15:04:05"))
	}

	// Align the repo column to the longest path (capped for readability).
	maxLen := len("TOTAL")
	for _, r := range agg.Repos {
		if l := len(displayRepo(r.RepoPath)); l > maxLen {
			maxLen = l
		}
	}

	fmt.Fprintf(os.Stderr, "\nValue Estimate (approximate):\n")
	fmt.Fprintf(os.Stderr, "  %-*s  %6s  %12s  %14s\n", maxLen, "repo", "calls", "~time saved", "~tokens saved")
	for _, r := range agg.Repos {
		v := ComputeValue(r.Counts)
		fmt.Fprintf(os.Stderr, "  %-*s  %6d  %12s  %14s\n",
			maxLen, displayRepo(r.RepoPath), v.TotalCalls, formatDuration(v.TotalTimeSaved), humanCount(v.TotalTokensSaved))
	}
	total := ComputeValue(agg.Combined)
	fmt.Fprintf(os.Stderr, "  %-*s  %6d  %12s  %14s\n",
		maxLen, "TOTAL", total.TotalCalls, formatDuration(total.TotalTimeSaved), humanCount(total.TotalTokensSaved))
	fmt.Fprintf(os.Stderr, "\n")
}

// displayRepo renders a repo path for the aggregate table. The full path is
// used so repos that share a base name stay distinguishable. Empty paths render
// as "(unknown)".
func displayRepo(p string) string {
	if p == "" {
		return "(unknown)"
	}
	return p
}
