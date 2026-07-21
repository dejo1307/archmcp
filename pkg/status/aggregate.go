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

// ServerStatus is the aggregated view of a single MCP server's activity across
// every repo it has served: the historical grand total plus the current
// session. This is what plain --status renders — it is not tied to any cwd.
type ServerStatus struct {
	GrandTotal    map[string]int // sum of ToolCounts across all repos (lifetime)
	Session       map[string]int // sum of SessionCounts for the current server run
	StartTime     time.Time      // newest StartTime seen (the current/most-recent run)
	PID           int            // PID of that run
	DashboardPort int            // localhost HTTP dashboard port of that run (0 if none)
	Alive         bool           // whether that PID is still running
	TrackingSince time.Time      // earliest TrackingSince across all repos
	Repos         int            // number of repos with recorded usage
	Found         bool           // false if no usage files exist
}

// AggregateServer collapses all per-repo usage files in dir into one server
// view. The grand total sums every file; the session sums only files written by
// the current server run — identified as those sharing the newest StartTime, so
// stale session counts from a previous run (files not re-touched yet) are
// excluded. Best-effort: unreadable/malformed files are skipped.
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

	// First pass: grand total, newest StartTime, earliest TrackingSince.
	for _, info := range infos {
		for k, v := range info.ToolCounts {
			ss.GrandTotal[k] += v
		}
		if info.StartTime.After(ss.StartTime) {
			ss.StartTime = info.StartTime
			ss.PID = info.PID
			ss.DashboardPort = info.DashboardPort
		}
		if !info.TrackingSince.IsZero() && (ss.TrackingSince.IsZero() || info.TrackingSince.Before(ss.TrackingSince)) {
			ss.TrackingSince = info.TrackingSince
		}
	}

	// Second pass: session counts only from files belonging to the current run.
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
