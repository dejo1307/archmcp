package engine

import (
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
// captured by the extractors). Repos without git contribute only to the age signal.
//
// Everything it judges is scoped to the repos actually in the loaded graph. Age comes
// from the loaded snapshot's own generated_at; the machine-wide
// ~/.enola/receipt.json is consulted only as a fallback, and only when it describes a
// loaded repo. That scoping is load-bearing, not defensive: the receipt is shared by
// every enola process on the machine, so a sibling agent terminal's snapshot routinely
// makes it describe a different graph entirely.
func (e *Engine) Staleness(maxAge time.Duration, now time.Time) Staleness {
	var st Staleness

	gr, err := LoadGlobalReceipt()
	snap, loaded := e.loadedGraph()

	// Age comes from the LOADED snapshot's own generated_at. That field is
	// authoritative: it is stamped on every generate and restored verbatim from
	// snapshot.meta.json on restart, so it always describes the graph being served.
	if snap != nil && snap.Meta.GeneratedAt != "" {
		if t, perr := time.Parse(time.RFC3339, snap.Meta.GeneratedAt); perr == nil {
			st.GeneratedAt = t
		}
	}
	// The graph-wide receipt is only a fallback, and only when it actually describes
	// a repo in the loaded graph. ~/.enola/receipt.json is shared by every enola
	// process on the machine and describes whichever generated last, so preferring it
	// unconditionally reported a sibling terminal's timestamp as this graph's age.
	if st.GeneratedAt.IsZero() && err == nil && gr != nil && gr.GeneratedAt != "" && describesAny(gr, loaded) {
		if t, perr := time.Parse(time.RFC3339, gr.GeneratedAt); perr == nil {
			st.GeneratedAt = t
		}
	}
	if !st.GeneratedAt.IsZero() {
		st.Age = now.Sub(st.GeneratedAt)
		st.TooOld = st.Age > maxAge
	}

	for _, r := range e.stalenessEntries(gr, err, snap, loaded) {
		if r.Path == "" || r.Git == nil {
			continue // non-git or unknown: covered by the age signal only
		}
		// Same outputDir exclusion as the snapshot-time capture in receipt.go. The two
		// MUST agree: recording "clean" while reading the live tree as dirty (because
		// enola's own output dir is untracked) would make the arm below fire on every
		// single snapshot.
		cur := gitInfo(r.Path, e.cfg.Output.Dir)
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

// loadedGraph returns the published snapshot together with the canonical absolute
// paths of the repos that graph actually holds. Both come from ONE bundle load, so
// repoPaths and snapshot are read as a consistent pair (the same reason
// ResolveFactFile loads once).
//
// GenerateSnapshot only builds repoPaths in append mode — a single-repo snapshot
// leaves it nil — so fall back to the snapshot's own RepoPath. An empty result means
// nothing is loaded, which callers must treat as "cannot verify", never as "matches".
func (e *Engine) loadedGraph() (*facts.Snapshot, map[string]bool) {
	b := e.current.Load()
	paths := make(map[string]bool, len(b.repoPaths)+1)
	for _, p := range b.repoPaths {
		if p != "" {
			paths[canonicalRepoPath(p)] = true
		}
	}
	if len(paths) == 0 && b.snapshot != nil && b.snapshot.Meta.RepoPath != "" {
		paths[canonicalRepoPath(b.snapshot.Meta.RepoPath)] = true
	}
	return b.snapshot, paths
}

// describesAny reports whether the receipt names at least one repo that is in the
// loaded graph. Paths are compared canonically because a receipt records the path as
// the writing server resolved it (macOS /var vs /private/var).
func describesAny(gr *facts.GraphReceipt, loaded map[string]bool) bool {
	if gr == nil || len(loaded) == 0 {
		return false
	}
	for _, r := range gr.Repos {
		if r.Path != "" && loaded[canonicalRepoPath(r.Path)] {
			return true
		}
	}
	return false
}

// stalenessEntries returns the per-repo entries to check, restricted to repos that
// are actually in the loaded graph. The global receipt supplies the snapshot-time git
// baseline, but it is machine-wide — every enola process on the box overwrites it with
// its OWN repo list — so taking it verbatim meant judging the freshness of a repo this
// server had never loaded, in both directions: a false "commit moved" for a sibling
// terminal's repo, and silence about the loaded repo because it was never checked.
// Filtering here is what makes the wrong verdict unrepresentable rather than merely
// unlikely. Falls back to a single synthetic entry from the snapshot meta.
func (e *Engine) stalenessEntries(gr *facts.GraphReceipt, grErr error, snap *facts.Snapshot, loaded map[string]bool) []facts.GraphRepoEntry {
	if grErr == nil && gr != nil && len(gr.Repos) > 0 && len(loaded) > 0 {
		kept := make([]facts.GraphRepoEntry, 0, len(gr.Repos))
		for _, r := range gr.Repos {
			if r.Path != "" && loaded[canonicalRepoPath(r.Path)] {
				kept = append(kept, r)
			}
		}
		if len(kept) > 0 {
			return kept
		}
	}
	if snap == nil || snap.Meta.RepoPath == "" {
		return nil
	}
	return []facts.GraphRepoEntry{{
		// The label the facts actually carry — the restore path keys its repoPaths map
		// off this entry, so a directory-derived guess would restore a graph nothing
		// could be looked up in.
		Label: snap.Meta.Label(),
		Path:  snap.Meta.RepoPath,
		Git:   snap.Meta.Git,
	}}
}
