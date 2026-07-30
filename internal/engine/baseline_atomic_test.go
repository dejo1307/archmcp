package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSnapshotArtifacts lays down a minimal set of on-disk snapshot artifacts in dir.
func writeSnapshotArtifacts(t *testing.T, dir, factsBody string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range snapshotArtifactFiles {
		body := "{}"
		if name == "facts.jsonl" {
			body = factsBody
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCopyArtifacts_PublishesCompleteSet — the baseline directory must never be
// observable holding a subset of the artifacts, because LoadSnapshotDir accepts any
// directory containing facts.jsonl and would diff against a truncated one.
func TestCopyArtifacts_PublishesCompleteSet(t *testing.T) {
	out := t.TempDir()
	writeSnapshotArtifacts(t, out, "fact-a\n")
	dst := filepath.Join(out, BaselineSubdir)

	if err := copyArtifacts(out, dst); err != nil {
		t.Fatalf("copyArtifacts: %v", err)
	}

	for _, name := range snapshotArtifactFiles {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("artifact %s missing from the published baseline: %v", name, err)
		}
	}
	assertNoStagingLeftovers(t, out)
}

// TestCopyArtifacts_FailureLeavesPreviousBaselineIntact is the property that matters
// most in the background-pin design: a pin that fails must not have destroyed the
// baseline that was already there.
//
// Copying in place overwrote the old artifacts before it could discover it would fail,
// so an interrupted pin left a baseline that was neither the old one nor the new one.
func TestCopyArtifacts_FailureLeavesPreviousBaselineIntact(t *testing.T) {
	out := t.TempDir()
	writeSnapshotArtifacts(t, out, "new-facts\n")
	dst := filepath.Join(out, BaselineSubdir)

	// An established baseline.
	writeSnapshotArtifacts(t, dst, "ORIGINAL-BASELINE\n")

	// Force the staging step to fail: with the parent read-only, MkdirTemp cannot
	// create the staging directory. (Skipped as root, where the mode is not enforced.)
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	if err := os.Chmod(out, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(out, 0o755) }()

	if err := copyArtifacts(out, dst); err == nil {
		t.Fatal("expected copyArtifacts to fail when it cannot stage")
	}

	got, err := os.ReadFile(filepath.Join(dst, "facts.jsonl"))
	if err != nil {
		t.Fatalf("the previous baseline was destroyed by a failed pin: %v", err)
	}
	if strings.TrimSpace(string(got)) != "ORIGINAL-BASELINE" {
		t.Errorf("previous baseline content = %q, want it untouched by the failed pin", got)
	}
}

// TestCopyArtifacts_ReplacesRatherThanOverlays — publishing must yield exactly the
// current artifact set. Copying in place merged into whatever was already there, so a
// file written by an older enola survived indefinitely, since nothing overwrote it.
func TestCopyArtifacts_ReplacesRatherThanOverlays(t *testing.T) {
	out := t.TempDir()
	writeSnapshotArtifacts(t, out, "fresh\n")
	dst := filepath.Join(out, BaselineSubdir)

	writeSnapshotArtifacts(t, dst, "stale\n")
	stray := filepath.Join(dst, "legacy-artifact.json")
	if err := os.WriteFile(stray, []byte("left by an older version"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyArtifacts(out, dst); err != nil {
		t.Fatalf("copyArtifacts: %v", err)
	}

	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Errorf("a stale artifact survived the republish (err=%v); the baseline must be replaced, not overlaid", err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "facts.jsonl"))
	if strings.TrimSpace(string(got)) != "fresh" {
		t.Errorf("facts.jsonl = %q, want the freshly published content", got)
	}
}

// TestCopyArtifacts_ToleratesMissingOptionalArtifacts — older snapshots may lack
// insights/meta, and that must remain a partial-but-valid publish rather than an error.
func TestCopyArtifacts_ToleratesMissingOptionalArtifacts(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "facts.jsonl"), []byte("only-facts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(out, BaselineSubdir)

	if err := copyArtifacts(out, dst); err != nil {
		t.Fatalf("copyArtifacts with only facts.jsonl: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "facts.jsonl")); err != nil {
		t.Errorf("facts.jsonl not published: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "insights.json")); !os.IsNotExist(err) {
		t.Errorf("insights.json should be absent, got err=%v", err)
	}
	assertNoStagingLeftovers(t, out)
}

// TestCopyArtifacts_RepeatedPinsLeaveNoStagingDirs — the staging directory is created
// inside the output dir, so a leak would accumulate silently on every pin. With the
// background session-start pin, that is once per session.
func TestCopyArtifacts_RepeatedPinsLeaveNoStagingDirs(t *testing.T) {
	out := t.TempDir()
	writeSnapshotArtifacts(t, out, "facts\n")
	dst := filepath.Join(out, BaselineSubdir)

	for i := 0; i < 5; i++ {
		if err := copyArtifacts(out, dst); err != nil {
			t.Fatalf("pin %d: %v", i, err)
		}
	}
	assertNoStagingLeftovers(t, out)
}

func assertNoStagingLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("staging directory %q was left behind", e.Name())
		}
	}
}
