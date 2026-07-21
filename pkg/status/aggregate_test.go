package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeUsage(t *testing.T, dir string, info StatusInfo) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	name := usageKey(info.RepoPath) + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAggregateUsage(t *testing.T) {
	dir := t.TempDir()

	t1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) // earlier
	writeUsage(t, dir, StatusInfo{
		RepoPath:      "/repo/a",
		TrackingSince: t1,
		ToolCounts:    map[string]int{"explore": 2, "query_facts": 1},
	})
	writeUsage(t, dir, StatusInfo{
		RepoPath:      "/repo/b",
		TrackingSince: t2,
		ToolCounts:    map[string]int{"explore": 3},
	})

	agg, err := AggregateUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(agg.Repos) != 2 {
		t.Fatalf("repos: got %d, want 2", len(agg.Repos))
	}
	if agg.Combined["explore"] != 5 {
		t.Errorf("combined explore: got %d, want 5", agg.Combined["explore"])
	}
	if agg.Combined["query_facts"] != 1 {
		t.Errorf("combined query_facts: got %d, want 1", agg.Combined["query_facts"])
	}
	if !agg.TrackingSince.Equal(t2) {
		t.Errorf("TrackingSince: got %v, want earliest %v", agg.TrackingSince, t2)
	}
	// Sorted by tokens saved desc: repo/a (2*15 + 1*8 = 38 ops) beats repo/b (3*15 = 45 ops)?
	// explore weight 15, query_facts weight 8: a = 38 ops, b = 45 ops -> b first.
	if agg.Repos[0].RepoPath != "/repo/b" {
		t.Errorf("expected repo/b first (higher tokens saved), got %q", agg.Repos[0].RepoPath)
	}
}

func TestAggregateUsageMissingDir(t *testing.T) {
	agg, err := AggregateUsage(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(agg.Repos) != 0 || len(agg.Combined) != 0 {
		t.Error("missing dir should yield empty aggregate")
	}
}

func TestAggregateUsageSkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"bad.json": "{not json", "note.txt": "ignored"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeUsage(t, dir, StatusInfo{RepoPath: "/repo/a", ToolCounts: map[string]int{"explore": 1}})

	agg, err := AggregateUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(agg.Repos) != 1 {
		t.Errorf("should skip malformed/non-json: got %d repos, want 1", len(agg.Repos))
	}
}
