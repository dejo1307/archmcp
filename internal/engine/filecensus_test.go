package engine_test

// The file census is the walk's account of itself: every visited file lands in
// exactly one bucket, so parsed + excluded + skipped sums back to walked and a
// file the graph silently lost is arithmetically impossible to hide. These
// tests pin the identity on a mixed fixture and the determinism of the buckets.

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/extractors/goextractor"
	"github.com/enola-labs/enola/internal/extractors/rubyextractor"
	"github.com/enola-labs/enola/internal/facts"
)

// censusFixture generates a snapshot over a repo holding one file per bucket:
// a parsed Go file, a Ruby file whose extension is claimed but which yields no
// fact, a binary nothing claims, and an ignored log file.
func censusFixture(t *testing.T) *facts.FileCensus {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module m\n\ngo 1.21\n")
	writeFile(t, filepath.Join(repo, "m.go"), "package m\n\nfunc M() {}\n")
	writeFile(t, filepath.Join(repo, "notes.rb"), "# commentary only, no structure\n")
	writeFile(t, filepath.Join(repo, "blob.bin"), "\x00\x01\x02\n")
	writeFile(t, filepath.Join(repo, "trace.log"), "noise\n")

	cfg := config.Default()
	cfg.Ignore = append(cfg.Ignore, "*.log")
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	eng.RegisterExtractor(rubyextractor.New())
	snap, err := eng.GenerateSnapshot(context.Background(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.Census == nil {
		t.Fatal("snapshot meta carries no census")
	}
	return snap.Meta.Census
}

func TestFileCensus_EveryWalkedFileLandsInExactlyOneBucket(t *testing.T) {
	census := censusFixture(t)
	if !census.Sums() {
		t.Fatalf("parsed %d + ignored %d + by-kind %d + skipped %d != walked %d",
			census.Parsed, census.ExcludedByIgnore, census.ExcludedByKind,
			census.SkippedWithCause, census.FilesWalked)
	}
	if census.ExcludedByIgnore != 1 {
		t.Errorf("excluded_by_ignore = %d, want 1 (trace.log)", census.ExcludedByIgnore)
	}
	if census.ExcludedByKind != 1 || census.ExcludedKinds[".bin"] != 1 {
		t.Errorf("excluded_by_kind = %d kinds %v, want blob.bin recorded under .bin",
			census.ExcludedByKind, census.ExcludedKinds)
	}
	if census.SkippedWithCause != 2 {
		t.Errorf("skipped_with_cause = %d, want 2 (notes.rb and go.mod are claimed and yield nothing)", census.SkippedWithCause)
	}
	var causes []string
	for _, c := range census.TopSkipCauses {
		causes = append(causes, c.Cause)
	}
	joined := strings.Join(causes, "; ")
	if !strings.Contains(joined, "claimed by ruby, no facts emitted") ||
		!strings.Contains(joined, "claimed by go, no facts emitted") {
		t.Errorf("top_skip_causes = %+v, want causes naming ruby (notes.rb) and go (go.mod)", census.TopSkipCauses)
	}
	if census.Parsed < 1 {
		t.Errorf("parsed = %d, want at least m.go", census.Parsed)
	}
}

func TestFileCensus_BucketsAreDeterministic(t *testing.T) {
	first := censusFixture(t)
	second := censusFixture(t)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("census differs between identical runs:\n%+v\n%+v", first, second)
	}
}

// A claimed extension whose extractor never ran must say so, not report a
// parse problem the extractor never had the chance to have.
func TestFileCensus_ClaimedButNotRunNamesTheIdleExtractor(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "go.mod"), "module m\n\ngo 1.21\n")
	writeFile(t, filepath.Join(repo, "m.go"), "package m\n\nfunc M() {}\n")
	writeFile(t, filepath.Join(repo, "lib.rs"), "pub fn f() {}\n")

	eng, err := engine.New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	eng.RegisterExtractor(stubOwner{})
	snap, err := eng.GenerateSnapshot(context.Background(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	census := snap.Meta.Census
	want := "claimed by rust, which did not run this snapshot"
	found := false
	for _, c := range census.TopSkipCauses {
		if c.Cause == want && c.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("causes = %+v, want %q for lib.rs", census.TopSkipCauses, want)
	}
	if census.ExcludedKinds[".rs"] != 0 {
		t.Fatalf("lib.rs is claimed by an enabled extractor and must not count as an unclaimed kind: %v", census.ExcludedKinds)
	}
	if !census.Sums() {
		t.Fatalf("identity broken: %+v", census)
	}
}

// stubOwner claims .rs files under the real rust extractor's name (only names
// on the config's enabled list participate in the census) but never detects,
// standing in for a language extractor present in the build and idle on this
// repository.
type stubOwner struct{}

func (stubOwner) Name() string                 { return "rust" }
func (stubOwner) Detect(string) (bool, error)  { return false, nil }
func (stubOwner) OwnsFile(relFile string) bool { return strings.HasSuffix(relFile, ".rs") }
func (stubOwner) Extract(_ context.Context, _ string, _ []string) ([]facts.Fact, error) {
	return nil, nil
}
