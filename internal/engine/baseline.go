package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// Baseline subdirectories under the .enola output dir. `previous` is rotated
// automatically on every snapshot write (one step back); `baseline` is pinned
// explicitly via SetBaseline and survives subsequent generate_snapshot runs, so
// an agent can pin once at task start and diff after several rounds of edits.
const (
	PreviousSubdir = "previous"
	BaselineSubdir = "baseline"
)

// snapshotArtifactFiles are the on-disk files that constitute a persisted
// snapshot. receipt.json rides along so a pinned/previous baseline carries its
// provenance + quality receipt, which compare_receipts diffs against the current.
var snapshotArtifactFiles = []string{"facts.jsonl", "insights.json", "snapshot.meta.json", "receipt.json"}

// OutputDir returns the absolute .enola output directory for repoPath.
func (e *Engine) OutputDir(repoPath string) string {
	return filepath.Join(repoPath, e.cfg.Output.Dir)
}

// ResolveBaselineDir maps a baseline selector to the directory holding that
// snapshot's artifacts: "" / "pinned" → the explicit SetBaseline pin, "previous" → the
// automatically-rotated preceding run, anything else → an explicit path.
//
// Shared so every caller resolves a selector identically. The MCP tools
// (diff_snapshot, compare_receipts) and the `enola check` CLI gate must agree on what
// `previous` means, or the same word would name different snapshots depending on which
// surface the caller used.
func ResolveBaselineDir(outDir, selector string) string {
	switch strings.ToLower(strings.TrimSpace(selector)) {
	case "", "pinned":
		return filepath.Join(outDir, BaselineSubdir)
	case "previous":
		return filepath.Join(outDir, PreviousSubdir)
	default:
		return selector
	}
}

// SetBaseline pins the current on-disk snapshot as the diff baseline by copying
// the snapshot artifacts from the output dir into <output>/baseline/. Returns an
// error if no snapshot has been written yet.
func (e *Engine) SetBaseline(repoPath string) error {
	outDir := e.OutputDir(repoPath)
	if _, err := os.Stat(filepath.Join(outDir, "facts.jsonl")); err != nil {
		return fmt.Errorf("no snapshot to pin as baseline (run generate_snapshot first): %w", err)
	}
	return copyArtifacts(outDir, filepath.Join(outDir, BaselineSubdir))
}

// rotatePrevious copies the existing snapshot artifacts (if any) from outDir into
// outDir/previous/ before they are overwritten, giving a one-step-back baseline
// for diffing without any explicit pin. It is a no-op when no prior snapshot
// exists. Only the three root artifact files are copied, so the previous/ and
// baseline/ subdirs are never nested into themselves.
func rotatePrevious(outDir string) error {
	if _, err := os.Stat(filepath.Join(outDir, "facts.jsonl")); err != nil {
		return nil // nothing to rotate
	}
	return copyArtifacts(outDir, filepath.Join(outDir, PreviousSubdir))
}

// copyArtifacts copies the snapshot artifact files from srcDir to dstDir,
// tolerating individually-missing sources (older snapshots may lack insights/meta).
func copyArtifacts(srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dstDir, err)
	}
	for _, name := range snapshotArtifactFiles {
		if err := copyFile(filepath.Join(srcDir, name), filepath.Join(dstDir, name)); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// LoadSnapshotDir reads a persisted snapshot (facts.jsonl plus, when present,
// insights.json and snapshot.meta.json) from dir into an in-memory Snapshot for
// diffing. Missing insights/meta are tolerated; a missing facts.jsonl is an error.
func LoadSnapshotDir(dir string) (*facts.Snapshot, error) {
	factsPath := filepath.Join(dir, "facts.jsonl")
	if _, err := os.Stat(factsPath); err != nil {
		return nil, fmt.Errorf("no snapshot at %s: %w", dir, err)
	}
	store := facts.NewStore()
	if err := store.ReadJSONLFile(factsPath); err != nil {
		return nil, fmt.Errorf("reading facts from %s: %w", factsPath, err)
	}
	snap := &facts.Snapshot{Facts: store.All()}

	if data, err := os.ReadFile(filepath.Join(dir, "insights.json")); err == nil {
		var ins []facts.Insight
		if err := json.Unmarshal(data, &ins); err == nil {
			snap.Insights = ins
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "snapshot.meta.json")); err == nil {
		var meta facts.SnapshotMeta
		if err := json.Unmarshal(data, &meta); err == nil {
			snap.Meta = meta
		}
	}
	return snap, nil
}
