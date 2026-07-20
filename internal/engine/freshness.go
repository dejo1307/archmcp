package engine

import (
	"path/filepath"
	"time"

	"github.com/enola-labs/enola/internal/facts"
)

// RepoChange reports that a repository's code has moved since its snapshot was
// taken, and why.
type RepoChange struct {
	Label  string
	Reason string // e.g. "commit moved", "uncommitted changes"
}

// Staleness summarizes whether the loaded graph is out of date. It is ADVISORY
// only — used to surface a warning banner. Nothing here regenerates a snapshot.
type Staleness struct {
	GeneratedAt time.Time     // when the graph was generated (zero if unknown)
	Age         time.Duration // now - GeneratedAt (zero if GeneratedAt unknown)
	TooOld      bool          // Age exceeded the checked maxAge
	Changed     []RepoChange  // repos whose git state moved since the snapshot
}

// Stale reports whether any staleness signal fired.
func (s Staleness) Stale() bool { return s.TooOld || len(s.Changed) > 0 }

// Staleness judges the freshness of the loaded graph against maxAge and each repo's
// CURRENT VCS state, without regenerating anything. A repo counts as changed when
// its git HEAD has moved since the snapshot, or its working tree is NEWLY dirty
// (a tree that was already dirty at snapshot time is ignored — that state was
// captured by the extractors). Age is measured from the graph-wide generated_at in
// ~/.enola/receipt.json, falling back to the in-memory snapshot meta for a
// single-repo graph. Repos without git contribute only to the age signal.
func (e *Engine) Staleness(maxAge time.Duration, now time.Time) Staleness {
	var st Staleness

	gr, err := LoadGlobalReceipt()
	if err == nil && gr.GeneratedAt != "" {
		if t, perr := time.Parse(time.RFC3339, gr.GeneratedAt); perr == nil {
			st.GeneratedAt = t
		}
	}
	if st.GeneratedAt.IsZero() {
		// No (or unparseable) global receipt: use the loaded snapshot's own time.
		if snap := e.Snapshot(); snap != nil && snap.Meta.GeneratedAt != "" {
			if t, perr := time.Parse(time.RFC3339, snap.Meta.GeneratedAt); perr == nil {
				st.GeneratedAt = t
			}
		}
	}
	if !st.GeneratedAt.IsZero() {
		st.Age = now.Sub(st.GeneratedAt)
		st.TooOld = st.Age > maxAge
	}

	for _, r := range e.stalenessEntries(gr, err) {
		if r.Path == "" || r.Git == nil {
			continue // non-git or unknown: covered by the age signal only
		}
		cur := gitInfo(r.Path)
		if cur == nil {
			continue
		}
		switch {
		case cur.Commit != r.Git.Commit:
			st.Changed = append(st.Changed, RepoChange{Label: r.Label, Reason: "commit moved"})
		case !r.Git.Dirty && cur.Dirty:
			st.Changed = append(st.Changed, RepoChange{Label: r.Label, Reason: "uncommitted changes"})
		}
	}
	return st
}

// stalenessEntries returns the per-repo entries to check: the global receipt's repo
// list when available, otherwise a single synthetic entry from the in-memory
// snapshot meta (single-repo / no global receipt).
func (e *Engine) stalenessEntries(gr *facts.GraphReceipt, grErr error) []facts.GraphRepoEntry {
	if grErr == nil && gr != nil && len(gr.Repos) > 0 {
		return gr.Repos
	}
	snap := e.Snapshot()
	if snap == nil || snap.Meta.RepoPath == "" {
		return nil
	}
	return []facts.GraphRepoEntry{{
		Label: filepath.Base(snap.Meta.RepoPath),
		Path:  snap.Meta.RepoPath,
		Git:   snap.Meta.Git,
	}}
}
