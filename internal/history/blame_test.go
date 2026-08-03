package history

import (
	"fmt"
	"testing"

	pkghistory "github.com/enola-labs/enola/pkg/history"
)

// blameFixture records a sequence of graph states and returns the log.
func blameFixture(t *testing.T, root string, states [][]string) []pkghistory.Entry {
	t.Helper()
	for i, s := range states {
		storeRevision(t, root, fmt.Sprintf("%08d", i), fmt.Sprintf("commit%d", i), s, Options{WorkingKeep: -1})
	}
	entries, err := pkghistory.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// The question the whole timeline exists to answer.
func TestBlame_FindsWhenSomethingAppearedAndWhenItWent(t *testing.T) {
	root := t.TempDir()
	entries := blameFixture(t, root, [][]string{
		{factLine("A", 10)},
		{factLine("A", 10), factLine("B", 20)}, // B arrives
		{factLine("A", 10), factLine("B", 20)},
		{factLine("A", 10)}, // B goes
	})

	b, err := pkghistory.BlameLines(root, entries, "\"B\"", pkghistory.BlameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Events) != 2 {
		t.Fatalf("want an arrival and a departure, got %d events", len(b.Events))
	}
	if len(b.Events[0].Added) != 1 || b.Events[0].Entry.Seq != entries[1].Seq {
		t.Errorf("the arrival is at the wrong revision: %+v", b.Events[0].Entry.Seq)
	}
	if len(b.Events[1].Removed) != 1 || b.Events[1].Entry.Seq != entries[3].Seq {
		t.Errorf("the departure is at the wrong revision: %+v", b.Events[1].Entry.Seq)
	}
	if _, ok := b.Introduced(); !ok {
		t.Error("Introduced() found nothing despite an arrival")
	}
	if b.Present() {
		t.Error("B was removed, so nothing matching should remain")
	}
}

// THE correctness property, and the reason blame reconstructs forward instead of reading
// each stored patch on its own.
//
// A segment BASE is a patch against the empty set, so every line in the graph appears as an
// addition inside it. A blame that read patches independently would report every symbol in
// the repository as "introduced" at each segment boundary — on a long history, most of its
// answers would be that artifact rather than an answer.
func TestBlame_ASegmentBoundaryIsNotAnIntroduction(t *testing.T) {
	root := t.TempDir()

	// Revision 0 introduces A. Revision 1 changes NOTHING about A but opens a new segment
	// (a different epoch), so A is re-added inside that base.
	storeRevision(t, root, "aaaa0000", "c0", []string{factLine("A", 10)}, Options{WorkingKeep: -1})

	e := pkghistory.Entry{
		ID: "sha256:bbbb1111", Repo: "github.com/org/repo",
		At: "2026-08-03T11:00:00Z", Epoch: "a-different-epoch",
		Git: gitAt("c1"),
	}
	if _, err := Append(root, e, Options{
		WorkingKeep: -1,
		Contents:    contents(e.ID, []string{factLine("A", 10)}, nil),
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := pkghistory.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if entries[1].Blob.Member != 1 {
		t.Fatalf("the fixture did not open a new segment: %+v", entries[1].Blob)
	}

	b, err := pkghistory.BlameLines(root, entries, "\"A\"", pkghistory.BlameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Events) != 1 {
		t.Fatalf("A arrived once and never changed; the segment base must not read as a second arrival (got %d events)", len(b.Events))
	}
	if b.Events[0].Entry.Seq != entries[0].Seq {
		t.Errorf("the arrival was attributed to the wrong revision")
	}
}

// A revision that changed a thousand unrelated things must produce no event, or a blame
// degenerates into a log.
func TestBlame_IgnoresUnrelatedChurn(t *testing.T) {
	root := t.TempDir()
	entries := blameFixture(t, root, [][]string{
		{factLine("Target", 10), factLine("X", 1)},
		{factLine("Target", 10), factLine("Y", 2), factLine("Z", 3)},
	})

	b, err := pkghistory.BlameLines(root, entries, "Target", pkghistory.BlameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Events) != 1 {
		t.Fatalf("want only Target's own arrival, got %d events", len(b.Events))
	}
}

// Findings are a separate haystack: "when did this cycle appear" is a different question
// from "when did this symbol", and they live in different patches.
func TestBlame_SearchesFindingsWhenAsked(t *testing.T) {
	root := t.TempDir()
	for i, ins := range [][]string{
		nil,
		{`{"title":"Dependency cycle: a -> b -> a","source":"cycles"}`},
	} {
		e := pkghistory.Entry{
			ID: fmt.Sprintf("sha256:%08d", i), Repo: "github.com/org/repo",
			At: fmt.Sprintf("2026-08-03T1%d:00:00Z", i), Epoch: "epoch1", Git: gitAt(fmt.Sprintf("c%d", i)),
		}
		c := contents(e.ID, []string{factLine("A", 10)}, ins)
		if _, err := Append(root, e, Options{WorkingKeep: -1, Contents: c}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := pkghistory.Read(root)
	if err != nil {
		t.Fatal(err)
	}

	// The default haystack is facts, and the finding is not in it.
	facts, err := pkghistory.BlameLines(root, entries, "Dependency cycle", pkghistory.BlameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Events) != 0 {
		t.Errorf("a finding must not be found among the facts: %+v", facts.Events)
	}

	found, err := pkghistory.BlameLines(root, entries, "Dependency cycle", pkghistory.BlameOptions{Findings: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := found.Introduced(); !ok || got.Seq != entries[1].Seq {
		t.Errorf("the cycle's first appearance was not found: %+v", found.Events)
	}
}

// A blame that silently searched half the history would answer "never" for something it
// simply could not see, so what it could not read is counted and reported.
func TestBlame_CountsWhatItCouldNotRead(t *testing.T) {
	root := t.TempDir()
	entries := blameFixture(t, root, [][]string{{factLine("A", 10)}, {factLine("A", 10), factLine("B", 20)}})

	// A header-only revision, as retention leaves behind.
	entries = append(entries, pkghistory.Entry{ID: "sha256:cccc2222", At: "2026-08-03T12:00:00Z"})

	b, err := pkghistory.BlameLines(root, entries, "\"B\"", pkghistory.BlameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Scanned != 2 || b.Skipped != 1 {
		t.Errorf("want scanned=2 skipped=1, got scanned=%d skipped=%d", b.Scanned, b.Skipped)
	}
}

func TestBlame_FirstOnlyStopsAtTheArrival(t *testing.T) {
	root := t.TempDir()
	entries := blameFixture(t, root, [][]string{
		{factLine("A", 10)},
		{factLine("A", 10), factLine("B", 20)},
		{factLine("A", 10)},
		{factLine("A", 10), factLine("B", 20)},
	})

	b, err := pkghistory.BlameLines(root, entries, "\"B\"", pkghistory.BlameOptions{FirstOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Events) != 1 {
		t.Fatalf("want to stop at the first arrival, got %d events", len(b.Events))
	}
	if b.Scanned == len(entries) {
		t.Error("--first should not have needed to read the whole history")
	}
}

func TestBlame_EmptyPatternIsRejected(t *testing.T) {
	if _, err := pkghistory.BlameLines(t.TempDir(), nil, "", pkghistory.BlameOptions{}); err == nil {
		t.Error("blame needs something to look for")
	}
}

// The most natural way to ask about an EDGE is to type the arrow, and Go's encoding/json
// escapes `>` for HTML safety — so the stored line never contains a literal `>`. Without
// putting the query into the same encoding, the central question blame exists for answers
// "never" and gives no hint why.
func TestBlame_FindsAnEdgeWrittenWithARealArrow(t *testing.T) {
	root := t.TempDir()
	// A dependency fact as the engine stores it, arrow escaped exactly as encoding/json
	// writes it.
	dep := `{"kind":"dependency","name":"pkg/command -\u003e pkg/history","file":"pkg/command/log.go","line":14}`
	entries := blameFixture(t, root, [][]string{
		{factLine("A", 10)},
		{factLine("A", 10), dep},
	})

	b, err := pkghistory.BlameLines(root, entries, "pkg/command -> pkg/history", pkghistory.BlameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Introduced(); !ok {
		t.Fatalf("an edge typed with a real arrow was not found: %+v", b.Events)
	}
}
