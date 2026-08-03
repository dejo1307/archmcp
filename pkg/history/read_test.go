package history

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/pkg/facts"
)

// entry builds a minimal committed entry for a test.
func entry(id, at, commit string) Entry {
	return Entry{
		ID:   "sha256:" + id,
		Repo: "github.com/org/repo",
		At:   at,
		Git:  &facts.GitInfo{Commit: commit, Ref: "main"},
	}
}

// writeLog writes entries as a log file, optionally without the final newline.
func writeLog(t *testing.T, root string, complete bool, entries ...Entry) {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	out := b.String()
	if !complete {
		out = strings.TrimSuffix(out, "\n")
	}
	if err := os.WriteFile(filepath.Join(root, LogFileName), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRead_MissingLogIsNotAFailure(t *testing.T) {
	// Every repository starts here, so it must be a state a caller can branch on rather
	// than an error it has to string-match.
	_, err := Read(t.TempDir())
	if !errors.Is(err, ErrNoHistory) {
		t.Fatalf("want ErrNoHistory, got %v", err)
	}
}

func TestRead_RoundTrip(t *testing.T) {
	root := t.TempDir()
	want := []Entry{
		entry("aaa1", "2026-08-01T10:00:00Z", "c0ffee1"),
		entry("bbb2", "2026-08-02T10:00:00Z", "c0ffee2"),
	}
	want[1].Summary = Summary{FactsAdded: 3, EdgesAdded: 1, FactCount: 100}
	writeLog(t, root, true, want...)

	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != want[0].ID || got[1].Summary.FactsAdded != 3 {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

// A crash or a full disk mid-append leaves a partial final line. Refusing to read the
// other entries because of it would make one interrupted write permanently destroy the
// readability of the whole history.
func TestParse_TruncatedFinalLineIsDropped(t *testing.T) {
	root := t.TempDir()
	writeLog(t, root, true, entry("aaa1", "2026-08-01T10:00:00Z", "c1"))
	f, err := os.OpenFile(filepath.Join(root, LogFileName), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"sha256:bbb2","at":"2026-08-0`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	got, err := Read(root)
	if err != nil {
		t.Fatalf("a truncated tail must not fail the read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want the 1 complete entry, got %d", len(got))
	}
}

// The converse: garbage with valid entries AFTER it was not an interrupted write, so it
// is corruption. Skipping it silently would hide data loss behind a log that still
// renders — the reader would see a shorter history and no reason to doubt it.
func TestParse_MalformedLineInTheMiddleIsAnError(t *testing.T) {
	good, err := json.Marshal(entry("aaa1", "2026-08-01T10:00:00Z", "c1"))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("{not json}\n" + string(good) + "\n")

	if _, err := Parse(data, "log.jsonl"); err == nil {
		t.Fatal("corruption before a valid entry must be reported, not skipped")
	}
}

// A complete file (trailing newline present) whose last line is garbage is corruption
// too: the writer finished, so nothing was interrupted.
func TestParse_MalformedFinalLineOfACompleteFileIsAnError(t *testing.T) {
	if _, err := Parse([]byte("{not json}\n"), "log.jsonl"); err == nil {
		t.Fatal("a complete file ending in garbage must be reported")
	}
}

// Merging two machines' logs of one repository is the property the format is shaped to
// have (see the package doc): content-addressed identity, portable repo identity, and
// Seq as local bookkeeping. Exercised now, with no remote in existence, because it is
// what pins those three rules in place while they are still cheap to keep.
func TestMerge_UnionsAndDedupsByID(t *testing.T) {
	shared := entry("aaa1", "2026-08-01T10:00:00Z", "c1")
	laptop := []Entry{shared, entry("bbb2", "2026-08-02T10:00:00Z", "c2")}
	ci := []Entry{shared, entry("ccc3", "2026-08-03T10:00:00Z", "c3")}
	// Seq values that disagree between the machines, which is the normal case.
	laptop[0].Seq, laptop[1].Seq = 1, 2
	ci[0].Seq, ci[1].Seq = 7, 8

	got := Merge(laptop, ci)

	if len(got) != 3 {
		t.Fatalf("want 3 distinct revisions, got %d", len(got))
	}
	for i, wantID := range []string{"sha256:aaa1", "sha256:bbb2", "sha256:ccc3"} {
		if got[i].ID != wantID {
			t.Errorf("position %d: want %s, got %s", i, wantID, got[i].ID)
		}
		if got[i].Seq != i+1 {
			t.Errorf("Seq must be renumbered locally, got %d at position %d", got[i].Seq, i)
		}
	}
}

func TestMerge_IsOrderedByTimeNotByInputOrder(t *testing.T) {
	// The later-timestamped entry arrives first, as it would from a machine whose log
	// was fetched second.
	got := Merge(
		[]Entry{entry("bbb2", "2026-08-02T10:00:00Z", "c2")},
		[]Entry{entry("aaa1", "2026-08-01T10:00:00Z", "c1")},
	)
	if got[0].ID != "sha256:aaa1" {
		t.Fatalf("want the older revision first, got %s", got[0].ID)
	}
}

func TestResolve(t *testing.T) {
	entries := []Entry{
		entry("aaa1111", "2026-08-01T10:00:00Z", "c0ffee1111"),
		entry("bbb2222", "2026-08-02T10:00:00Z", "deadbeef22"),
		entry("ccc3333", "2026-08-03T10:00:00Z", "deadbeef22"), // same commit, later observation
	}
	for i := range entries {
		entries[i].Seq = i + 1
	}
	entries[0].Refs = []string{"baseline"}

	for _, tc := range []struct {
		sel  string
		want string
	}{
		{"", "sha256:ccc3333"},
		{"latest", "sha256:ccc3333"},
		{"HEAD", "sha256:ccc3333"},
		{"HEAD~2", "sha256:aaa1111"},
		{"@2", "sha256:bbb2222"},
		{"bbb2", "sha256:bbb2222"},
		{"sha256:bbb2", "sha256:bbb2222"},
		{"baseline", "sha256:aaa1111"},
		// One commit can hold several revisions (an edit round per snapshot), so a
		// commit resolves to the newest observation at it rather than reporting an
		// ambiguity the user cannot act on.
		{"deadbeef", "sha256:ccc3333"},
	} {
		got, err := Resolve(entries, tc.sel)
		if err != nil {
			t.Errorf("Resolve(%q): %v", tc.sel, err)
			continue
		}
		if got.ID != tc.want {
			t.Errorf("Resolve(%q) = %s, want %s", tc.sel, got.ID, tc.want)
		}
	}
}

func TestResolve_RejectsWhatItCannotIdentify(t *testing.T) {
	entries := []Entry{entry("aaa1111", "2026-08-01T10:00:00Z", "c1")}
	for _, sel := range []string{"HEAD~9", "@42", "zzz", "nothing-like-this"} {
		if _, err := Resolve(entries, sel); err == nil {
			t.Errorf("Resolve(%q) must fail rather than guess", sel)
		}
	}
}

func TestResolve_AmbiguousPrefixNamesTheCandidates(t *testing.T) {
	entries := []Entry{
		entry("abcd111", "2026-08-01T10:00:00Z", "c1"),
		entry("abcd222", "2026-08-02T10:00:00Z", "c2"),
	}
	_, err := Resolve(entries, "abcd")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("want an ambiguity error listing the candidates, got %v", err)
	}
}

func TestResolve_EmptyHistory(t *testing.T) {
	if _, err := Resolve(nil, "latest"); !errors.Is(err, ErrNoHistory) {
		t.Fatalf("want ErrNoHistory, got %v", err)
	}
}

// RFC3339 sorts lexically only when every timestamp shares one UTC offset, and a history
// that mixes live and backfilled revisions never does: the recorder stamps UTC, a backfill
// stamps each commit's own committer date with its author's offset.
//
// Seen on a Rust repository carrying +03:00 and +02:00, where the lexically-first revision
// is forty minutes LATER than the real first — enough to put the wrong revision at the start
// of the timeline, and from there to mislabel which one began the history.
func TestSortedByTime_ComparesInstantsNotStrings(t *testing.T) {
	// 20:27+03:00 is 17:27 UTC; 20:05+02:00 is 18:05 UTC. Chronologically the first is
	// earlier; lexically it is not.
	earlier := entry("aaa", "2026-06-25T20:27:26+03:00", "c1")
	later := entry("bbb", "2026-06-25T20:05:30+02:00", "c2")

	got := SortedByTime([]Entry{later, earlier})
	if got[0].ID != earlier.ID {
		t.Errorf("want the chronologically earlier revision first, got %s (%s) before %s (%s)",
			got[0].ID, got[0].At, got[1].ID, got[1].At)
	}
}

// The same rule for Merge, and it matters more there: two machines is exactly where offsets
// differ.
func TestMerge_OrdersByInstant(t *testing.T) {
	got := Merge(
		[]Entry{entry("bbb", "2026-06-25T20:05:30+02:00", "c2")},
		[]Entry{entry("aaa", "2026-06-25T20:27:26+03:00", "c1")},
	)
	if got[0].ID != "sha256:aaa" {
		t.Errorf("want the chronologically earlier revision first, got %s at %s", got[0].ID, got[0].At)
	}
}

// An unparseable timestamp is still a revision. Placing it where its text says beats
// dropping it or guessing an end to sort it to.
func TestSortedByTime_ToleratesAnUnreadableTimestamp(t *testing.T) {
	got := SortedByTime([]Entry{
		entry("bbb", "2026-06-26T10:00:00Z", "c2"),
		entry("zzz", "not a timestamp", "c3"),
		entry("aaa", "2026-06-25T10:00:00Z", "c1"),
	})
	if len(got) != 3 {
		t.Fatalf("want every revision kept, got %d", len(got))
	}
}
