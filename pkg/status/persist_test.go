package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setupHome points os.UserHomeDir at a temp dir and returns a repo path.
func setupHome(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return t.TempDir()
}

func readFile(t *testing.T, path string) StatusInfo {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read status file: %v", err)
	}
	var info StatusInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal status file: %v", err)
	}
	return info
}

// Stats are written to the home store (~/.enola/usage/), keyed by repo, not
// inside the repo.
func TestOnToolCallWritesToHomeStore(t *testing.T) {
	repo := setupHome(t)
	tr := NewTracker(repo)
	tr.SetStartTime(time.Now())
	tr.OnToolCall("explore", repo)
	tr.OnToolCall("explore", repo)
	tr.OnToolCall("query_facts", repo)

	homePath, err := usagePath(canonicalRepoPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(homePath); err != nil {
		t.Fatalf("expected home store file at %s: %v", homePath, err)
	}
	if _, err := os.Stat(legacyPath(repo)); err == nil {
		t.Error("no file should be written to the legacy in-repo location")
	}

	info := readFile(t, homePath)
	if info.ToolCounts["explore"] != 2 || info.SessionCounts["explore"] != 2 {
		t.Errorf("explore: total=%d session=%d, want 2/2", info.ToolCounts["explore"], info.SessionCounts["explore"])
	}
	if info.TrackingSince.IsZero() {
		t.Error("TrackingSince should be stamped on first run")
	}
	if info.RepoPath != canonicalRepoPath(repo) {
		t.Errorf("RepoPath: got %q, want %q", info.RepoPath, canonicalRepoPath(repo))
	}
}

// Usage is attributed to the repo each call reports, even from one tracker.
func TestOnToolCallAttributesPerRepo(t *testing.T) {
	_ = setupHome(t)
	repoA := t.TempDir()
	repoB := t.TempDir()

	tr := NewTracker(repoA) // fallback repoA
	tr.SetStartTime(time.Now())
	tr.OnToolCall("explore", repoA)
	tr.OnToolCall("impact_analysis", repoB)
	tr.OnToolCall("impact_analysis", repoB)

	a := tr.GetStatus(repoA)
	b := tr.GetStatus(repoB)
	if a.ToolCounts["explore"] != 1 || a.ToolCounts["impact_analysis"] != 0 {
		t.Errorf("repoA counts wrong: %+v", a.ToolCounts)
	}
	if b.ToolCounts["impact_analysis"] != 2 || b.ToolCounts["explore"] != 0 {
		t.Errorf("repoB counts wrong: %+v", b.ToolCounts)
	}

	// Each repo has its own file.
	pa, _ := usagePath(canonicalRepoPath(repoA))
	pb, _ := usagePath(canonicalRepoPath(repoB))
	if pa == pb {
		t.Fatal("repoA and repoB must map to different files")
	}
	readFile(t, pa)
	readFile(t, pb)
}

// An empty repo argument falls back to the tracker's fallback repo.
func TestOnToolCallEmptyRepoUsesFallback(t *testing.T) {
	_ = setupHome(t)
	fallback := t.TempDir()
	tr := NewTracker(fallback)
	tr.SetStartTime(time.Now())
	tr.OnToolCall("explore", "")

	if tr.GetStatus(fallback).ToolCounts["explore"] != 1 {
		t.Error("empty repo should attribute to the fallback repo")
	}
}

// After a "restart" (new tracker), a repo's lifetime total is carried forward
// on first touch while the session starts empty.
func TestAccumulatesAcrossRestart(t *testing.T) {
	repo := setupHome(t)

	tr1 := NewTracker(repo)
	start1 := time.Now().Add(-time.Hour)
	tr1.SetStartTime(start1)
	tr1.OnToolCall("explore", repo)
	tr1.OnToolCall("explore", repo)
	tr1.OnToolCall("query_facts", repo)

	tr2 := NewTracker(repo)
	tr2.SetStartTime(time.Now())
	tr2.OnToolCall("explore", repo)         // total 3, session 1
	tr2.OnToolCall("impact_analysis", repo) // total 1, session 1

	info := tr2.GetStatus(repo)
	if info.ToolCounts["explore"] != 3 {
		t.Errorf("explore grand total: got %d, want 3", info.ToolCounts["explore"])
	}
	if info.SessionCounts["explore"] != 1 {
		t.Errorf("explore session: got %d, want 1", info.SessionCounts["explore"])
	}
	if info.ToolCounts["query_facts"] != 1 {
		t.Errorf("query_facts grand total (from baseline): got %d, want 1", info.ToolCounts["query_facts"])
	}
	if _, ok := info.SessionCounts["query_facts"]; ok {
		t.Error("query_facts should not appear in session 2 (only in baseline)")
	}
	if !info.TrackingSince.Equal(start1) {
		t.Errorf("TrackingSince: got %v, want %v (from first session)", info.TrackingSince, start1)
	}
}

// A legacy in-repo status file is migrated to the home store on first touch,
// and the legacy file is removed.
func TestMigratesLegacyFile(t *testing.T) {
	repo := setupHome(t)

	legacyDir := filepath.Join(repo, ".enola")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"pid":123,"start_time":"2020-01-01T00:00:00Z","repo_path":"` + repo + `","tool_counts":{"explore":5}}`
	if err := os.WriteFile(legacyPath(repo), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := NewTracker(repo)
	tr.SetStartTime(time.Now())
	tr.OnToolCall("explore", repo) // triggers migration, then +1

	homePath, _ := usagePath(canonicalRepoPath(repo))
	info := readFile(t, homePath)
	if info.ToolCounts["explore"] != 6 {
		t.Errorf("migrated total + 1 call: got %d, want 6", info.ToolCounts["explore"])
	}
	if _, err := os.Stat(legacyPath(repo)); !os.IsNotExist(err) {
		t.Error("legacy file should be removed after migration")
	}
}

// A migration whose home-store write fails must NOT delete the legacy file, so
// the accumulated usage data survives and migration is retried on the next call.
func TestMigrationPreservesLegacyOnWriteFailure(t *testing.T) {
	repo := setupHome(t)

	// Seed a legacy in-repo status file with real accumulated counts.
	legacyDir := filepath.Join(repo, ".enola")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"pid":123,"start_time":"2020-01-01T00:00:00Z","repo_path":"` + repo + `","tool_counts":{"explore":5}}`
	if err := os.WriteFile(legacyPath(repo), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sabotage the home store: create ~/.enola/usage as a *regular file* so the
	// migrating write's MkdirAll of that directory fails ("not a directory").
	// Deterministic across platforms and unaffected by running as root, unlike
	// chmod-based permission tricks.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".enola"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".enola", "usage"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := NewTracker(repo)
	tr.SetStartTime(time.Now())
	tr.OnToolCall("explore", repo) // triggers migration; home write fails

	// The legacy file must still be present with its data intact.
	if _, err := os.Stat(legacyPath(repo)); err != nil {
		t.Fatalf("legacy file must survive a failed migration write: %v", err)
	}
	info, _, err := ReadStatus(legacyPath(repo))
	if err != nil {
		t.Fatalf("legacy file unreadable after failed migration: %v", err)
	}
	if info.ToolCounts["explore"] != 5 {
		t.Errorf("legacy data corrupted: explore=%d, want 5", info.ToolCounts["explore"])
	}
}

// A fresh tracker with nothing on disk counts normally.
func TestFreshStart(t *testing.T) {
	repo := setupHome(t)
	tr := NewTracker(repo)
	tr.SetStartTime(time.Now())
	tr.OnToolCall("explore", repo)
	if tr.GetStatus(repo).ToolCounts["explore"] != 1 {
		t.Error("fresh tracker should count normally")
	}
}
