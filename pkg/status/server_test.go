package status

import (
	"os"
	"testing"
	"time"
)

func TestAggregateServer(t *testing.T) {
	dir := t.TempDir()

	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	older := now.Add(-48 * time.Hour)
	trackA := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	trackB := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) // earliest

	// Two repos from the current run (same newest StartTime).
	writeUsage(t, dir, StatusInfo{
		RepoPath: "/repo/a", PID: os.Getpid(), StartTime: now, TrackingSince: trackA,
		ToolCounts:    map[string]int{"explore": 5, "query_facts": 2},
		SessionCounts: map[string]int{"explore": 3},
	})
	writeUsage(t, dir, StatusInfo{
		RepoPath: "/repo/b", PID: os.Getpid(), StartTime: now, TrackingSince: trackB,
		ToolCounts:    map[string]int{"explore": 1},
		SessionCounts: map[string]int{"explore": 1},
	})
	// A repo last touched in an EARLIER run: counts in grand total, but its
	// stale session must be excluded.
	writeUsage(t, dir, StatusInfo{
		RepoPath: "/repo/c", PID: 999999, StartTime: older, TrackingSince: trackA,
		ToolCounts:    map[string]int{"generate_snapshot": 4},
		SessionCounts: map[string]int{"generate_snapshot": 4},
	})

	ss := AggregateServer(dir)
	if !ss.Found {
		t.Fatal("expected Found=true")
	}
	if ss.Repos != 3 {
		t.Errorf("Repos: got %d, want 3", ss.Repos)
	}
	// Grand total sums all three files.
	if ss.GrandTotal["explore"] != 6 || ss.GrandTotal["query_facts"] != 2 || ss.GrandTotal["generate_snapshot"] != 4 {
		t.Errorf("GrandTotal wrong: %+v", ss.GrandTotal)
	}
	// Session sums only the current-run files (a+b), not c.
	if ss.Session["explore"] != 4 {
		t.Errorf("Session explore: got %d, want 4 (3+1, excluding older run)", ss.Session["explore"])
	}
	if _, ok := ss.Session["generate_snapshot"]; ok {
		t.Error("stale session from older run must be excluded")
	}
	if !ss.StartTime.Equal(now) {
		t.Errorf("StartTime: got %v, want newest %v", ss.StartTime, now)
	}
	if ss.PID != os.Getpid() {
		t.Errorf("PID: got %d, want current-run pid %d", ss.PID, os.Getpid())
	}
	if !ss.Alive {
		t.Error("Alive should be true (current process pid is alive)")
	}
	if !ss.TrackingSince.Equal(trackB) {
		t.Errorf("TrackingSince: got %v, want earliest %v", ss.TrackingSince, trackB)
	}
}

func TestAggregateServerEmpty(t *testing.T) {
	ss := AggregateServer(t.TempDir())
	if ss.Found {
		t.Error("empty dir should yield Found=false")
	}
}
