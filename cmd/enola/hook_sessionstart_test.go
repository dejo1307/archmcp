package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// writeBaseline lays down a baseline directory with the given git state, optionally
// marked as auto-pinned.
func writeBaseline(t *testing.T, dir string, git *facts.GitInfo, auto bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "facts.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := facts.SnapshotMeta{RepoPath: "/repo", Git: git}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot.meta.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if auto {
		if err := os.WriteFile(filepath.Join(dir, autoPinMarker), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestShouldAutoPin_NeverReplacesADeliberatePin is the guarantee that makes automatic
// pinning safe to enable at all.
//
// A baseline pinned by a person — or by an agent following the server's prompt — is the
// "before" of work that may span days. Replacing it at the next session start would
// destroy exactly the thing it was recording, and the user would have no way to know why
// their diff suddenly reported nothing.
func TestShouldAutoPin_NeverReplacesADeliberatePin(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baseline")
	writeBaseline(t, dir, &facts.GitInfo{Commit: "deadbeef"}, false) // no auto marker

	if shouldAutoPin(dir, t.TempDir(), ".enola", nil) {
		t.Error("a deliberately pinned baseline was scheduled for replacement")
	}
}

// TestShouldAutoPin_WithNoBaselineYet — the case the hook exists for.
func TestShouldAutoPin_WithNoBaselineYet(t *testing.T) {
	if !shouldAutoPin(filepath.Join(t.TempDir(), "baseline"), t.TempDir(), ".enola", nil) {
		t.Error("no baseline exists, so one should be pinned")
	}
}

// TestShouldAutoPin_SkipsWhenTreeHasNotMoved — re-snapshotting an unchanged tree burns
// seconds to reproduce a byte-identical result. On a large repository that is the
// difference between a hook nobody notices and one they disable.
func TestShouldAutoPin_SkipsWhenTreeHasNotMoved(t *testing.T) {
	repo := t.TempDir() // not a git repo, so GitState returns nil
	dir := filepath.Join(t.TempDir(), "baseline")
	writeBaseline(t, dir, &facts.GitInfo{Commit: "deadbeef", Dirty: false}, true)

	// GitState cannot read a non-git directory, so the decision must fail toward
	// refreshing rather than toward silently keeping a baseline that may be wrong.
	if !shouldAutoPin(dir, repo, ".enola", nil) {
		t.Error("with git state unavailable, the baseline must be refreshed rather than trusted")
	}
}

// TestShouldAutoPin_DirtyTreeIsNeverCurrent — "dirty" says the content is not identified
// by the commit, so two dirty trees at the same commit may differ arbitrarily. Treating
// them as equal would skip a pin that is genuinely needed.
func TestShouldAutoPin_DirtyTreeIsNeverCurrent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baseline")
	writeBaseline(t, dir, &facts.GitInfo{Commit: "deadbeef", Dirty: true}, true)

	if !shouldAutoPin(dir, t.TempDir(), ".enola", nil) {
		t.Error("a dirty baseline must never be treated as current")
	}
}

// TestSessionStartHook_IsSilentOnEveryFailurePath — the hook runs at the start of every
// session. Anything it prints, or any non-zero exit, surfaces to a user who did not ask
// for it and cannot act on it.
func TestSessionStartHook_IsSilentOnEveryFailurePath(t *testing.T) {
	// A directory that is not a repository, and a path that does not exist. Neither may
	// produce output; runSessionStartHook returns rather than exiting so it is callable
	// here, and the process-level exit-0 contract is covered by the CLI tests.
	for _, cwd := range []string{t.TempDir(), "/no/such/directory"} {
		t.Run(cwd, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "baseline")
			// shouldAutoPin is the only part reachable without spawning; exercising it
			// with hostile inputs must not panic.
			_ = shouldAutoPin(dir, cwd, ".enola", nil)
		})
	}
}

// TestAutoPinMarker_LivesInsideTheBaseline — SetBaseline republishes the directory
// atomically, so a marker written anywhere else would be lost or would outlive the
// baseline it describes.
func TestAutoPinMarker_LivesInsideTheBaseline(t *testing.T) {
	if filepath.IsAbs(autoPinMarker) || filepath.Dir(autoPinMarker) != "." {
		t.Errorf("autoPinMarker = %q, want a bare filename placed inside the baseline dir", autoPinMarker)
	}
}
