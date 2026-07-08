package engine

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/version"
)

// globalReceiptDirName / globalReceiptFileName locate the graph-wide receipt under
// the user's home directory (~/.enola/receipt.json).
const (
	globalReceiptDirName  = ".enola"
	globalReceiptFileName = "receipt.json"
)

// globalReceiptPath resolves ~/.enola/receipt.json. It returns an error when the
// home directory is unavailable (e.g. a sandboxed run with no $HOME) so callers
// can degrade gracefully instead of failing the snapshot.
func globalReceiptPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	return filepath.Join(home, globalReceiptDirName, globalReceiptFileName), nil
}

// repoEntries returns one GraphRepoEntry per repository currently in the graph.
// In multi-repo (append) mode it iterates RepoPaths(); in single-repo mode it
// falls back to the sole primary repo from the snapshot meta. Git state is captured
// per repo via gitInfo (nil for non-git dirs) and fact counts come from the store's
// byRepo index. AddedAt/CommitChangedAt are left to the merge step; InGraphFor is
// derived at write time. Entries are sorted by Label for stable output.
func (e *Engine) repoEntries() []facts.GraphRepoEntry {
	// label -> absolute path for every repo in the graph.
	repos := e.RepoPaths()
	if len(repos) == 0 {
		// Single-repo graph: RepoPaths() is nil until a repo is appended.
		if e.snapshot == nil || e.snapshot.Meta.RepoPath == "" {
			return nil
		}
		abs := e.snapshot.Meta.RepoPath
		repos = map[string]string{filepath.Base(abs): abs}
	}

	entries := make([]facts.GraphRepoEntry, 0, len(repos))
	for label, abs := range repos {
		entries = append(entries, facts.GraphRepoEntry{
			Label:     label,
			Path:      abs,
			Git:       gitInfo(abs),
			FactCount: e.store.CountByRepo(label),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Label < entries[j].Label })
	return entries
}

// crossRepoEdgeCount counts the consumer->provider edges in the cross-repo "graph
// of graphs". Those edges are materialized as depends_on relations on the synthetic
// KindService nodes (one per repo), so summing them reads the true cross-repo edge
// count directly — and cheaply (there are only as many service nodes as repos). It
// deliberately does NOT use ByKind(KindDependency): that kind also covers every
// ordinary import/dependency fact, which would over-count by orders of magnitude.
func crossRepoEdgeCount(store *facts.Store) int {
	n := 0
	for _, svc := range store.ByKind(facts.KindService) {
		for _, r := range svc.Relations {
			if r.Kind == facts.RelDependsOn {
				n++
			}
		}
	}
	return n
}

// assembleGraphReceipt builds a GraphReceipt describing the current graph state.
// Membership timestamps (AddedAt/CommitChangedAt) are set to their first-write
// defaults here; WriteGlobalReceipt merges forward from any prior receipt.
func (e *Engine) assembleGraphReceipt(now time.Time) facts.GraphReceipt {
	nowStr := now.UTC().Format(time.RFC3339)

	entries := e.repoEntries()
	for i := range entries {
		entries[i].AddedAt = nowStr
		entries[i].InGraphFor = "0s"
	}

	gr := facts.GraphReceipt{
		GeneratedAt:        nowStr,
		EnolaVersion:       version.Version,
		ServiceCount:       len(e.store.ByKind(facts.KindService)),
		CrossRepoEdgeCount: crossRepoEdgeCount(e.store),
		Coverage:           coverageSummary(e.store),
		Repos:              entries,
	}
	if e.snapshot != nil {
		gr.SnapshotID = e.snapshot.Meta.SnapshotID
		gr.FactCount = e.snapshot.Meta.FactCount
		gr.InsightCount = e.snapshot.Meta.InsightCount
	}
	return gr
}

// WriteGlobalReceipt writes ~/.enola/receipt.json for the current graph. It reads
// any existing receipt to merge forward per-repo membership timestamps (so a repo's
// added_at is preserved across regenerations and a moved commit does not reset it),
// then atomically replaces the file. It never aborts a snapshot: a missing home dir
// is logged and skipped, and a corrupt prior receipt is treated as no prior state.
func (e *Engine) WriteGlobalReceipt() error {
	if e.snapshot == nil {
		return fmt.Errorf("no snapshot generated")
	}

	path, err := globalReceiptPath()
	if err != nil {
		log.Printf("[engine] global receipt skipped: %v", err)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating global receipt dir: %w", err)
	}

	now := time.Now().UTC()
	gr := e.assembleGraphReceipt(now)

	// Merge forward membership timestamps from the prior receipt, if any. Repos
	// present in the prior receipt but absent now are simply omitted: the receipt
	// is rebuilt only from current entries, so departed repos drop out.
	prevByLabel := readPriorGraphReceipt(path)
	gr.Repos = mergeRepoEntries(gr.Repos, prevByLabel, now)

	data, err := json.MarshalIndent(gr, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling global receipt: %w", err)
	}
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("writing global receipt: %w", err)
	}
	log.Printf("[engine] wrote %s (%d repos)", path, len(gr.Repos))
	return nil
}

// mergeRepoEntries carries per-repo membership state forward from a prior receipt
// (keyed by label) onto the freshly-assembled current entries, and stamps each
// entry's derived InGraphFor. For a repo already present, AddedAt is preserved (a
// regeneration is not a re-entry) and a moved commit records CommitChangedAt=now
// WITHOUT resetting AddedAt; an unchanged commit carries the prior CommitChangedAt
// forward. A repo absent from prev keeps its default AddedAt=now. cur already
// excludes departed repos, so they drop out.
func mergeRepoEntries(cur []facts.GraphRepoEntry, prevByLabel map[string]facts.GraphRepoEntry, now time.Time) []facts.GraphRepoEntry {
	nowStr := now.UTC().Format(time.RFC3339)
	for i := range cur {
		c := &cur[i]
		if prev, ok := prevByLabel[c.Label]; ok {
			c.AddedAt = prev.AddedAt
			if prev.Git != nil && c.Git != nil && prev.Git.Commit != c.Git.Commit {
				c.CommitChangedAt = nowStr
			} else {
				c.CommitChangedAt = prev.CommitChangedAt
			}
		}
		c.InGraphFor = inGraphFor(c.AddedAt, now)
	}
	return cur
}

// readPriorGraphReceipt loads the existing global receipt keyed by repo label. A
// missing or corrupt file yields an empty map (the corrupt case is logged), so a
// hand-edited or truncated receipt self-heals rather than failing the write.
func readPriorGraphReceipt(path string) map[string]facts.GraphRepoEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // missing (or unreadable) => no prior state
	}
	var prev facts.GraphReceipt
	if err := json.Unmarshal(data, &prev); err != nil {
		log.Printf("[engine] warning: ignoring corrupt global receipt %s: %v", path, err)
		return nil
	}
	byLabel := make(map[string]facts.GraphRepoEntry, len(prev.Repos))
	for _, r := range prev.Repos {
		byLabel[r.Label] = r
	}
	return byLabel
}

// inGraphFor returns a human-readable duration since addedAt (RFC3339), rounded to
// the second. It falls back to "0s" when addedAt cannot be parsed.
func inGraphFor(addedAt string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, addedAt)
	if err != nil {
		return "0s"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

// writeFileAtomic writes data to a temp file in the destination directory and
// renames it over path, so a concurrent reader never sees a torn/partial receipt.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
