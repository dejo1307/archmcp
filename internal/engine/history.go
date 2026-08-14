package engine

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
	inthistory "github.com/enola-labs/enola/internal/history"
	pkghistory "github.com/enola-labs/enola/pkg/history"
)

// recordHistory appends one revision to the repository's architecture history.
//
// It runs at the END of WriteArtifacts, after every artifact is on disk, and every
// failure inside it is logged and swallowed. A history is a convenience built on top of
// a snapshot; a snapshot that fails because its history could not be written would have
// inverted that relationship.
//
// The delta is computed against previous/, which WriteArtifacts has just rotated, so it
// holds the immediately preceding run — the parent revision by construction. That is the
// same pair diff_snapshot compares under `--baseline=previous`, computed by the same
// function, so the log's counts and the diff's counts cannot drift apart.
func (e *Engine) recordHistory(repoPath string, meta facts.SnapshotMeta, b *snapshotBundle, factsPath string) {
	if !e.cfg.HistoryEnabled() || b.snapshot == nil {
		return
	}
	root, err := pkghistory.Root(repoPath, e.cfg.History.Dir)
	if err != nil {
		log.Printf("[engine] warning: cannot locate history dir: %v", err)
		return
	}

	// The published snapshot carries the meta WITHOUT output hashes; the copy passed in
	// has them. Neither matters to the delta, but the entry should describe the
	// artifacts that were actually written, so record from the passed copy.
	current := *b.snapshot
	current.Meta = meta

	entry := pkghistory.Entry{
		ID:      meta.SnapshotID,
		Repo:    facts.RepoIdentity(meta),
		At:      meta.GeneratedAt,
		Epoch:   pkghistory.Epoch(meta),
		Git:     meta.Git,
		Parents: gitParents(repoPath),
		Repos:   repoRefs(e, b),
		Summary: summarize(&current, previousSideFor(repoPath, e.cfg.Output.Dir)),
	}

	opts := inthistory.Options{
		WorkingKeep: e.cfg.History.WorkingKeep,
		BlobKeep:    e.cfg.History.BlobKeep,
		Contents:    e.historyContents(meta, &current, factsPath),
	}
	recorded, err := inthistory.Append(root, entry, opts)
	if err != nil {
		log.Printf("[engine] warning: could not record history: %v", err)
		return
	}
	if recorded {
		log.Printf("[engine] recorded history revision %s (%s)", entry.Short(), entry.Summary.Headline())
	}
}

// historyContents assembles the revision's storable payload, or nil when blob storage is
// off — in which case the revision is still recorded as a header, and only `show` and
// `diff` on it are unavailable.
//
// factsPath is the facts.jsonl this snapshot just wrote, read back line by line. Reading
// the artifact is what guarantees the stored lines are the ones the snapshot actually
// produced: any path that re-marshals facts here would be a second serialization that
// could disagree with the first, and the disagreement would be written into the history
// rather than caught. It previously received the in-memory buffer the file had been
// written from, which gave the same guarantee — but only by keeping a whole extra copy
// of the serialization alive, 792 MiB of it on a large graph, for a step that is
// skipped entirely when blobs are off.
func (e *Engine) historyContents(meta facts.SnapshotMeta, snap *facts.Snapshot, factsPath string) *inthistory.Contents {
	if !e.cfg.HistoryBlobsEnabled() {
		return nil
	}
	insightLines, err := pkghistory.InsightLines(snap.Insights)
	if err != nil {
		log.Printf("[engine] warning: could not serialize insights for the history: %v", err)
		return nil
	}
	factLines, err := readLines(factsPath)
	if err != nil {
		log.Printf("[engine] warning: could not read %s back for the history: %v", factsPath, err)
		return nil
	}
	return &inthistory.Contents{
		FactLines:    factLines,
		InsightLines: insightLines,
		Receipt:      meta.Receipt(),
	}
}

// readLines reads canonical JSONL into its lines, without the trailing empty element the
// final newline would produce. The scanner buffer matches the one Store.ReadJSONL uses,
// so a fact line this engine can write is a fact line it can read back.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	var lines []string
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// previousSide describes the rotated preceding snapshot WITHOUT its facts: the small
// parts are read into memory, and the facts stay on disk behind a source that streams
// them on demand.
//
// The facts are the whole reason this type exists. summarize needs counts from them,
// not the facts themselves, and loading a kernel-sized previous snapshot to produce
// seven integers cost 386 MiB — at the end of a run, alongside the snapshot it was
// being compared against. Insights and meta are kept whole because they are small and
// the findings comparison genuinely needs their content.
type previousSide struct {
	facts    diff.FactSource
	insights []facts.Insight
	meta     facts.SnapshotMeta
}

// previousSideFor prepares the rotated preceding snapshot, or nil when there is none
// (the first snapshot of a repository, or a history enabled after the fact).
func previousSideFor(repoPath, outputDir string) *previousSide {
	dir := filepath.Join(repoPath, outputDir, PreviousSubdir)
	factsPath := filepath.Join(dir, "facts.jsonl")
	if _, err := os.Stat(factsPath); err != nil {
		return nil
	}
	p := &previousSide{
		facts: diff.JSONLSource(func() (io.ReadCloser, error) { return os.Open(factsPath) }),
	}
	if data, err := os.ReadFile(filepath.Join(dir, "insights.json")); err == nil {
		var ins []facts.Insight
		if err := json.Unmarshal(data, &ins); err == nil {
			p.insights = ins
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "snapshot.meta.json")); err == nil {
		var meta facts.SnapshotMeta
		if err := json.Unmarshal(data, &meta); err == nil {
			p.meta = meta
		}
	}
	return p
}

// summarize reduces a snapshot pair to the counts a log line needs.
//
// With no predecessor the result is marked Initial and carries absolute counts rather
// than a delta: the first snapshot of a repository did not ADD ten thousand facts, it
// found them, and a renderer shown a delta there reports the entire codebase as the work
// of whoever ran enola first.
// It computes the delta by COUNTING rather than by building it. diff.Compute produces
// the same numbers and is still what diff_snapshot uses, but it materialises every
// added, removed and changed fact plus both sides' edge sets to do it — 261 MiB on a
// warm dotnet/runtime run, on top of the 386 MiB of loading the baseline. A summary
// needs none of that; see diff.Counts.
func summarize(current *facts.Snapshot, previous *previousSide) pkghistory.Summary {
	s := pkghistory.Summary{
		FactCount:    len(current.Facts),
		InsightCount: len(current.Insights),
	}
	if previous == nil {
		s.Initial = true
		return s
	}

	counts, err := diff.CountFacts(previous.facts, current.Facts)
	if err != nil {
		// The previous snapshot is unreadable. Record the revision with absolute
		// counts rather than dropping it: a history with a gap is worse than one
		// whose delta is missing, and the cause is logged.
		log.Printf("[engine] warning: could not summarize the delta against previous/: %v", err)
		s.Initial = true
		return s
	}
	s.FactsAdded = counts.FactsAdded
	s.FactsRemoved = counts.FactsRemoved
	s.FactsChanged = counts.FactsChanged
	s.EdgesAdded = counts.EdgesAdded
	s.EdgesRemoved = counts.EdgesRemoved
	// The structural-cause buckets only. The incidental ones exist precisely because a
	// finding can appear without this change causing it, and a log line is the last place
	// that distinction should be dropped.
	findings := diff.ClassifyFindings(previous.insights, current.Insights, counts.TouchedNames)
	s.FindingsNew = findings.New
	s.FindingsResolved = findings.Resolved
	// Not simply !Comparable: elapsed time between two revisions is the normal shape of a
	// timeline, and flagging it would mark most of a release-sampled history as suspect.
	s.Incomparable = diff.CompareMeta(previous.meta, current.Meta).InvalidatesDelta()

	byKind := map[string]int{}
	for kind, n := range counts.AddedByKind {
		byKind[kind] += n
	}
	for kind, n := range counts.RemovedByKind {
		byKind[kind] -= n
	}
	for kind, n := range byKind {
		if n == 0 {
			delete(byKind, kind)
		}
	}
	if len(byKind) > 0 {
		s.ByKind = byKind
	}
	return s
}

// gitParents returns the parent commits of HEAD, for the case where the topology has to
// be drawn without the repository in hand. It is a fallback and not the source of truth:
// enola observes a sparse subset of the commit graph, so the edges BETWEEN observed
// revisions are derived by ancestry at render time, not remembered here.
func gitParents(repoPath string) []string {
	out, err := runGit(repoPath, "rev-list", "--parents", "-n", "1", "HEAD")
	if err != nil {
		return nil
	}
	// "<commit> <parent>…" — the first field is HEAD itself.
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return nil // a root commit has no parents
	}
	return fields[1:]
}

// repoRefs records where each repository of a MULTI-repo graph sat at snapshot time. A
// multi-repo snapshot has no single commit, so a renderer needs the vector; single-repo
// graphs get nil, since Entry.Git already says everything.
func repoRefs(e *Engine, b *snapshotBundle) []pkghistory.RepoRef {
	if len(b.repoPaths) < 2 {
		return nil
	}
	refs := make([]pkghistory.RepoRef, 0, len(b.repoPaths))
	for label, abs := range b.repoPaths {
		r := pkghistory.RepoRef{Label: label}
		if gi := gitInfo(abs, e.cfg.Output.Dir); gi != nil {
			r.Commit, r.Dirty = gi.Commit, gi.Dirty
		}
		refs = append(refs, r)
	}
	// Sorted by label: repoPaths is a map, so an unsorted vector would reorder itself
	// between runs and make two byte-identical graphs produce two different log lines.
	sort.Slice(refs, func(i, j int) bool { return refs[i].Label < refs[j].Label })
	return refs
}
