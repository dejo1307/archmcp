package engine

// The global receipt (~/.enola/receipt.json) describes the CURRENT graph and must
// carry per-repo membership forward across regenerations: a repo's added_at is the
// FIRST time it entered the graph, preserved even as the graph is rebuilt and even
// as its commit moves. These tests pin that merge-forward contract plus the write/
// read plumbing and the single-repo fallback.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/facts"
)

func gi(commit string) *facts.GitInfo { return &facts.GitInfo{Commit: commit, Ref: "main"} }

func TestMergeRepoEntries_PreservesAddedAtAndStampsNewRepos(t *testing.T) {
	t0 := "2026-07-01T09:00:00Z"
	now, _ := time.Parse(time.RFC3339, "2026-07-08T09:00:00Z")

	prev := map[string]facts.GraphRepoEntry{
		"A": {Label: "A", AddedAt: t0, Git: gi("x")},
	}
	// Current entries are freshly assembled: AddedAt defaults to now for all.
	cur := []facts.GraphRepoEntry{
		{Label: "A", AddedAt: now.Format(time.RFC3339), Git: gi("x")}, // unchanged commit
		{Label: "B", AddedAt: now.Format(time.RFC3339), Git: gi("q")}, // brand new
	}

	got := mergeRepoEntries(cur, prev, now)
	byLabel := index(got)

	if byLabel["A"].AddedAt != t0 {
		t.Errorf("A.AddedAt: got %q, want preserved %q", byLabel["A"].AddedAt, t0)
	}
	if byLabel["A"].CommitChangedAt != "" {
		t.Errorf("A.CommitChangedAt: got %q, want empty (commit unchanged)", byLabel["A"].CommitChangedAt)
	}
	if byLabel["B"].AddedAt != now.Format(time.RFC3339) {
		t.Errorf("B.AddedAt: got %q, want now %q", byLabel["B"].AddedAt, now.Format(time.RFC3339))
	}
	// InGraphFor is derived: A has been in for 7 days, B for 0.
	if want := (7 * 24 * time.Hour).String(); byLabel["A"].InGraphFor != want {
		t.Errorf("A.InGraphFor: got %q, want %q", byLabel["A"].InGraphFor, want)
	}
	if byLabel["B"].InGraphFor != "0s" {
		t.Errorf("B.InGraphFor: got %q, want 0s", byLabel["B"].InGraphFor)
	}
}

func TestMergeRepoEntries_CommitChangeKeepsAddedAt(t *testing.T) {
	t0 := "2026-07-01T09:00:00Z"
	now1, _ := time.Parse(time.RFC3339, "2026-07-08T09:00:00Z")

	// Pass 1: commit moves x -> y. AddedAt preserved, CommitChangedAt = now1.
	prev := map[string]facts.GraphRepoEntry{"A": {Label: "A", AddedAt: t0, Git: gi("x")}}
	cur := []facts.GraphRepoEntry{{Label: "A", AddedAt: now1.Format(time.RFC3339), Git: gi("y")}}
	got := index(mergeRepoEntries(cur, prev, now1))["A"]

	if got.AddedAt != t0 {
		t.Errorf("AddedAt reset on commit change: got %q, want %q", got.AddedAt, t0)
	}
	if got.CommitChangedAt != now1.Format(time.RFC3339) {
		t.Errorf("CommitChangedAt: got %q, want %q", got.CommitChangedAt, now1.Format(time.RFC3339))
	}

	// Pass 2: commit stays y. CommitChangedAt carries forward (not reset to now2).
	now2, _ := time.Parse(time.RFC3339, "2026-07-09T09:00:00Z")
	prev2 := map[string]facts.GraphRepoEntry{"A": got}
	cur2 := []facts.GraphRepoEntry{{Label: "A", AddedAt: now2.Format(time.RFC3339), Git: gi("y")}}
	got2 := index(mergeRepoEntries(cur2, prev2, now2))["A"]

	if got2.CommitChangedAt != now1.Format(time.RFC3339) {
		t.Errorf("CommitChangedAt should carry forward: got %q, want %q", got2.CommitChangedAt, now1.Format(time.RFC3339))
	}
	if got2.AddedAt != t0 {
		t.Errorf("AddedAt should still be preserved: got %q, want %q", got2.AddedAt, t0)
	}
}

func TestMergeRepoEntries_DepartedRepoDropped(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-07-08T09:00:00Z")
	prev := map[string]facts.GraphRepoEntry{
		"A": {Label: "A", AddedAt: "2026-07-01T09:00:00Z", Git: gi("x")},
		"B": {Label: "B", AddedAt: "2026-07-01T09:00:00Z", Git: gi("y")},
	}
	cur := []facts.GraphRepoEntry{{Label: "A", AddedAt: now.Format(time.RFC3339), Git: gi("x")}}

	got := mergeRepoEntries(cur, prev, now)
	if len(got) != 1 || got[0].Label != "A" {
		t.Fatalf("departed repo B should be dropped; got %+v", got)
	}
}

// A repo's corpus size is read from its own snapshot metadata, which may be
// momentarily unreadable — a zero reading means "no measurement this time", not
// "this repo has no source". Writing the gap through would silently un-price
// every later query against that repo, so the last known size carries forward.
func TestMergeRepoEntries_SourceBytesCarriedForwardOnZero(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-07-08T09:00:00Z")
	prev := map[string]facts.GraphRepoEntry{
		"A": {Label: "A", AddedAt: "2026-07-01T09:00:00Z", Git: gi("x"), SourceBytes: 872_581_357},
	}
	cur := []facts.GraphRepoEntry{{Label: "A", Git: gi("x"), SourceBytes: 0}}

	got := mergeRepoEntries(cur, prev, now)
	if got[0].SourceBytes != 872_581_357 {
		t.Errorf("SourceBytes: got %d, want the previous 872581357 carried forward", got[0].SourceBytes)
	}
}

// A fresh reading always wins, so a repo that grew or shrank is repriced.
func TestMergeRepoEntries_SourceBytesFreshReadingWins(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-07-08T09:00:00Z")
	prev := map[string]facts.GraphRepoEntry{
		"A": {Label: "A", AddedAt: "2026-07-01T09:00:00Z", Git: gi("x"), SourceBytes: 100},
	}
	cur := []facts.GraphRepoEntry{{Label: "A", Git: gi("x"), SourceBytes: 900}}

	if got := mergeRepoEntries(cur, prev, now); got[0].SourceBytes != 900 {
		t.Errorf("SourceBytes: got %d, want the fresh 900", got[0].SourceBytes)
	}
}

// The field is additive: a receipt written before it existed must still parse,
// leaving the size unknown rather than failing the read.
func TestReadPriorGraphReceipt_ReceiptWithoutSourceBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	old := `{"repos":[{"label":"A","path":"/tmp/a","added_at":"2026-07-01T09:00:00Z","fact_count":7}]}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readPriorGraphReceipt(path)
	entry, ok := got["A"]
	if !ok {
		t.Fatal("pre-existing receipt failed to parse")
	}
	if entry.SourceBytes != 0 || entry.FactCount != 7 {
		t.Errorf("got %+v, want SourceBytes=0 (unknown) and the existing fields intact", entry)
	}
}

func TestInGraphFor(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-07-08T09:00:00Z")
	if got := inGraphFor("2026-07-08T06:00:00Z", now); got != (3 * time.Hour).String() {
		t.Errorf("inGraphFor: got %q, want 3h0m0s", got)
	}
	if got := inGraphFor("not-a-time", now); got != "0s" {
		t.Errorf("inGraphFor(bad): got %q, want 0s", got)
	}
	// A future added_at (clock skew) clamps to 0 rather than a negative duration.
	if got := inGraphFor("2026-07-08T12:00:00Z", now); got != "0s" {
		t.Errorf("inGraphFor(future): got %q, want 0s", got)
	}
}

func TestWriteAtomicAndReadPrior_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	orig := facts.GraphReceipt{
		GeneratedAt: "2026-07-08T09:00:00Z",
		Repos: []facts.GraphRepoEntry{
			{Label: "A", AddedAt: "2026-07-01T09:00:00Z", Git: gi("x")},
		},
	}
	data, _ := json.MarshalIndent(orig, "", "  ")
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	byLabel := readPriorGraphReceipt(path)
	if got := byLabel["A"].AddedAt; got != "2026-07-01T09:00:00Z" {
		t.Errorf("round-tripped AddedAt: got %q", got)
	}

	// A corrupt file self-heals to "no prior" (empty map), not an error/panic.
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readPriorGraphReceipt(path); len(got) != 0 {
		t.Errorf("corrupt receipt should yield no prior state, got %+v", got)
	}
	// A missing file also yields no prior state.
	if got := readPriorGraphReceipt(filepath.Join(t.TempDir(), "nope.json")); len(got) != 0 {
		t.Errorf("missing receipt should yield no prior state, got %+v", got)
	}
}

func TestWriteGlobalReceipt_SingleRepoFallbackAndPersistence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // os.UserHomeDir() resolves ~ from $HOME on unix

	eng, err := New(config.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Two facts tagged with the repo label; RepoPaths() stays nil so the single-repo
	// fallback (label = base of snapshot RepoPath) must kick in.
	eng.store.Add(
		facts.Fact{Kind: facts.KindModule, Name: "m1", Repo: "myrepo"},
		facts.Fact{Kind: facts.KindSymbol, Name: "s1", Repo: "myrepo"},
	)
	eng.SetSnapshot(&facts.Snapshot{Meta: facts.SnapshotMeta{
		RepoPath: filepath.Join("/tmp", "some", "myrepo"), SnapshotID: "sha256:abc", FactCount: 2, InsightCount: 0,
	}})

	if err := eng.WriteGlobalReceipt(); err != nil {
		t.Fatalf("WriteGlobalReceipt: %v", err)
	}

	path := filepath.Join(home, ".enola", "receipt.json")
	first := readReceiptFile(t, path)
	if len(first.Repos) != 1 || first.Repos[0].Label != "myrepo" {
		t.Fatalf("single-repo fallback: want one repo labelled myrepo, got %+v", first.Repos)
	}
	if first.Repos[0].FactCount != 2 {
		t.Errorf("fact_count: got %d, want 2", first.Repos[0].FactCount)
	}
	if first.Repos[0].AddedAt == "" {
		t.Errorf("added_at should be set")
	}
	if first.SnapshotID != "sha256:abc" || first.FactCount != 2 {
		t.Errorf("graph-level fields: got id=%q fact_count=%d", first.SnapshotID, first.FactCount)
	}
	firstAdded := first.Repos[0].AddedAt

	// Regenerate and rewrite: added_at must be preserved (repo did not re-enter).
	eng.Snapshot().Meta.SnapshotID = "sha256:def"
	if err := eng.WriteGlobalReceipt(); err != nil {
		t.Fatalf("WriteGlobalReceipt (2nd): %v", err)
	}
	second := readReceiptFile(t, path)
	if second.Repos[0].AddedAt != firstAdded {
		t.Errorf("added_at not preserved across regenerations: %q != %q", second.Repos[0].AddedAt, firstAdded)
	}
	if second.SnapshotID != "sha256:def" {
		t.Errorf("snapshot_id should update: got %q", second.SnapshotID)
	}
}

func TestCrossRepoEdgeCount(t *testing.T) {
	st := facts.NewStore()
	// Three repos as service nodes; A->B and A->C are the cross-repo edges, carried
	// as depends_on relations on A's service node. Plenty of ordinary KindDependency
	// facts also exist and must NOT be counted.
	st.Add(
		facts.Fact{Kind: facts.KindService, Name: "A", Repo: "A", Relations: []facts.Relation{
			{Kind: facts.RelDependsOn, Target: "B"},
			{Kind: facts.RelDependsOn, Target: "C"},
		}},
		facts.Fact{Kind: facts.KindService, Name: "B", Repo: "B"},
		facts.Fact{Kind: facts.KindService, Name: "C", Repo: "C"},
		facts.Fact{Kind: facts.KindDependency, Name: "some/import", Repo: "A"},
		facts.Fact{Kind: facts.KindDependency, Name: "other/import", Repo: "B"},
	)
	if got := crossRepoEdgeCount(st); got != 2 {
		t.Errorf("crossRepoEdgeCount: got %d, want 2 (must not count ordinary dependency facts)", got)
	}

	// Single-repo graph: no service nodes => zero cross-repo edges.
	single := facts.NewStore()
	single.Add(facts.Fact{Kind: facts.KindSymbol, Name: "s", Repo: "solo"})
	if got := crossRepoEdgeCount(single); got != 0 {
		t.Errorf("crossRepoEdgeCount(single-repo): got %d, want 0", got)
	}
}

// index keys entries by label for order-independent assertions.
func index(entries []facts.GraphRepoEntry) map[string]facts.GraphRepoEntry {
	m := make(map[string]facts.GraphRepoEntry, len(entries))
	for _, e := range entries {
		m[e.Label] = e
	}
	return m
}

func readReceiptFile(t *testing.T, path string) facts.GraphReceipt {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var gr facts.GraphReceipt
	if err := json.Unmarshal(data, &gr); err != nil {
		t.Fatalf("unmarshaling receipt: %v", err)
	}
	return gr
}
