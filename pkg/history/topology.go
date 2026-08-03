package history

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Topology is the drawable shape of a history: the observed revisions, and which of them
// preceded which.
//
// enola only ever sees a SPARSE SUBSET of a repository's commits — the ones somebody
// happened to snapshot — so the edges between revisions are not the repository's parent
// links. They have to be derived: revision B follows revision A when A is the nearest
// observed ancestor of B, with everything unobserved in between collapsed away.
//
// That is not a novel idea; it is what git itself does for `git log --graph -- <path>`,
// where filtering leaves gaps and "parent rewriting" closes them. Doing anything simpler
// (drawing the recorded parent commits, or joining consecutive log entries) produces a
// picture that is wrong in exactly the case the graph exists for: two branches snapshotted
// in an interleaved order would be drawn as one line.
type Topology struct {
	// Rows are the revisions in draw order, oldest first.
	Rows []Row
	// Source names how the shape was derived, for a renderer to disclose. A picture
	// assembled from a fallback must not look like one derived from the repository.
	Source TopologySource
}

// TopologySource records which derivation produced a Topology.
type TopologySource string

const (
	// SourceGit — the repository was available and its commit graph was used.
	SourceGit TopologySource = "git"
	// SourceRecordedParents — the repository was unavailable, so each revision's recorded
	// parent commits were used. Weaker: a revision whose parent was never observed has no
	// path to anything, so runs that git would join may appear separate.
	SourceRecordedParents TopologySource = "recorded-parents"
	// SourceTime — no ancestry information at all (a non-git tree, or a repository that
	// has gone away), so revisions are strung together in the order they were taken. It is
	// a TIMELINE, not a history: it says what happened next, never what came from what.
	SourceTime TopologySource = "time"
)

// Row is one revision in a topology, with the rows it descends from.
type Row struct {
	Entry Entry
	// Parents indexes into Topology.Rows — the nearest observed ancestors of this
	// revision. Empty for a root. More than one means a merge, drawn as converging lanes.
	Parents []int
}

// BuildTopology derives the drawable shape of a set of entries.
//
// repoPath may be empty, or name a repository that no longer exists; the derivation degrades
// through the sources above rather than failing, because a history outliving its checkout
// is a normal thing (the whole point is that it remembers what the tree has forgotten) and
// a `log` that refuses to draw anything would be the wrong response to it.
func BuildTopology(entries []Entry, repoPath string) Topology {
	if len(entries) == 0 {
		return Topology{Source: SourceTime}
	}

	parents, source := commitParents(entries, repoPath)
	if source == SourceTime {
		return linearTopology(entries)
	}

	rows := make([]Row, len(entries))
	for i, e := range entries {
		rows[i] = Row{Entry: e}
	}

	// Where each commit's revisions sit, in order. One commit can hold several revisions
	// (a working revision per edit round, then the committed one).
	byCommit := map[string][]int{}
	for i, e := range entries {
		if c := e.Commit(); c != "" {
			byCommit[c] = append(byCommit[c], i)
		}
	}

	for i, e := range entries {
		commit := e.Commit()
		if commit == "" {
			// A revision with no commit at all (a non-git tree) can only be placed in
			// time. Chain it to whatever came immediately before.
			if i > 0 {
				rows[i].Parents = []int{i - 1}
			}
			continue
		}

		// Revisions sharing a commit are one linear run: an agent loop snapshotting the
		// same working tree over and over is a sequence of observations of one state, and
		// drawing them as siblings off the commit would turn a session into a fan.
		if siblings := byCommit[commit]; siblings[0] != i {
			for k, idx := range siblings {
				if idx == i {
					rows[i].Parents = []int{siblings[k-1]}
					break
				}
			}
			continue
		}

		for _, ancestor := range nearestObserved(commit, parents, byCommit) {
			// The LAST revision at the ancestor commit is the one this descends from —
			// it is the most recent observation of that state.
			idxs := byCommit[ancestor]
			rows[i].Parents = append(rows[i].Parents, idxs[len(idxs)-1])
		}
		sort.Ints(rows[i].Parents)
		rows[i].Parents = reduce(rows[i].Parents, rows)
	}

	return Topology{Rows: rows, Source: source}
}

// nearestObserved walks back through the commit graph from commit and returns the observed
// commits first reached on each path — the rewritten parents.
//
// Breadth-first and stopping at the first observed commit on every path, so an unobserved
// stretch of any length collapses to one edge. A commit unreachable in the parent map (a
// shallow clone's boundary, a commit that has been garbage-collected) simply contributes no
// edge, which is the honest outcome: nothing is known about what preceded it.
func nearestObserved(commit string, parents map[string][]string, observed map[string][]int) []string {
	var found []string
	seen := map[string]bool{commit: true}
	queue := append([]string(nil), parents[commit]...)

	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if seen[c] {
			continue
		}
		seen[c] = true
		if _, ok := observed[c]; ok {
			found = append(found, c)
			continue // stop this path; anything beyond is reachable THROUGH this revision
		}
		queue = append(queue, parents[c]...)
	}
	sort.Strings(found)
	return found
}

// reduce removes any parent reachable from another parent, so a merge whose two sides share
// an observed ancestor does not also draw a direct edge to it.
//
// Without this, snapshotting a branch, its base and the merge produces an edge from the
// merge straight back to the base as well as through the branch — a triangle that says the
// merge descends from the base twice.
func reduce(candidates []int, rows []Row) []int {
	if len(candidates) < 2 {
		return candidates
	}
	redundant := map[int]bool{}
	for _, from := range candidates {
		for _, other := range candidates {
			if from != other && reachable(from, other, rows) {
				redundant[other] = true
			}
		}
	}
	out := candidates[:0]
	for _, c := range candidates {
		if !redundant[c] {
			out = append(out, c)
		}
	}
	return out
}

// reachable reports whether to is an ancestor of from within the already-built rows.
func reachable(from, to int, rows []Row) bool {
	seen := map[int]bool{}
	stack := []int{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == to {
			return true
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, rows[n].Parents...)
	}
	return false
}

// linearTopology strings revisions together in the order they were taken — the fallback
// when nothing is known about ancestry.
func linearTopology(entries []Entry) Topology {
	rows := make([]Row, len(entries))
	for i, e := range entries {
		rows[i] = Row{Entry: e}
		if i > 0 {
			rows[i].Parents = []int{i - 1}
		}
	}
	return Topology{Rows: rows, Source: SourceTime}
}

// commitParents assembles the commit->parents map the reduction walks, preferring the
// repository and falling back to what the entries themselves recorded.
func commitParents(entries []Entry, repoPath string) (map[string][]string, TopologySource) {
	var commits []string
	for _, e := range entries {
		if c := e.Commit(); c != "" {
			commits = append(commits, c)
		}
	}
	if len(commits) == 0 {
		return nil, SourceTime
	}

	if repoPath != "" {
		if parents, err := gitParents(repoPath, commits); err == nil && len(parents) > 0 {
			return parents, SourceGit
		}
	}

	// Fall back to the parents each revision recorded at snapshot time. Enough to join
	// revisions whose parent commit was itself observed, and no help at all across a gap —
	// which is exactly why it is the fallback and not the primary.
	parents := map[string][]string{}
	recorded := false
	for _, e := range entries {
		if c := e.Commit(); c != "" && len(e.Parents) > 0 {
			parents[c] = e.Parents
			recorded = true
		}
	}
	if !recorded {
		return nil, SourceTime
	}
	return parents, SourceRecordedParents
}

// gitParents returns commit -> parents for every ancestor of the given commits.
//
// One `git rev-list` rather than a query per pair: ancestry between N revisions needs
// O(N^2) pairwise questions and one walk answers all of them. --topo-order is not required
// for correctness here (the map is consulted by lookup, not in sequence) but keeps the
// output stable, which makes a failure reproducible.
//
// Commits the repository no longer has — a rewritten branch, a dropped stash, a shallow
// clone's boundary — are passed with --ignore-missing so their absence prunes those paths
// instead of failing the whole walk. A history outlives the commits it describes; that is
// what it is for.
func gitParents(repoPath string, commits []string) (map[string][]string, error) {
	args := append([]string{"-C", repoPath, "rev-list", "--parents", "--topo-order", "--ignore-missing"}, dedupe(commits)...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("walking the commit graph: %w", err)
	}

	parents := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		parents[fields[0]] = fields[1:]
	}
	return parents, nil
}

func dedupe(ss []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
