package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummariseSplitsMinorAndMajorAtFivePercent(t *testing.T) {
	// The paper's own worked example: abocomp.dll, 918 commits, top engineer 379 of
	// them (41%), five engineers at or above 5%, twelve below, seventeen in total.
	const total = 918 // 5% of 918 is 45.9, so 46 commits is the major/minor line
	authors := map[string]int{"top": 379}
	for i := 0; i < 4; i++ { // four more majors at 46 == 5.01%
		authors["major"+string(rune('a'+i))] = 46
	}
	for i := 0; i < 11; i++ { // eleven minors at 29 == 3.2%
		authors["minor"+string(rune('a'+i))] = 29
	}
	authors["minorl"] = 36 // the twelfth, at 3.9%, still under the line
	sum := 0
	for _, n := range authors {
		sum += n
	}
	if sum != total {
		t.Fatalf("fixture does not sum to the paper's %d commits: %d", total, sum)
	}
	m := summarise("abocomp", authors, total)

	if m.Total != 17 {
		t.Errorf("Total = %d, want 17", m.Total)
	}
	if m.Major != 5 {
		t.Errorf("Major = %d, want 5", m.Major)
	}
	if m.Minor != 12 {
		t.Errorf("Minor = %d, want 12", m.Minor)
	}
	if m.TopAuthor != "top" {
		t.Errorf("TopAuthor = %q, want %q", m.TopAuthor, "top")
	}
	if m.TopShare < 0.35 || m.TopShare > 0.45 {
		t.Errorf("TopShare = %.3f, want ~0.41", m.TopShare)
	}
}

func TestSummariseTopAuthorIsDeterministicOnTies(t *testing.T) {
	// Two contributors with an identical share must not let map order decide who is
	// named the owner: the same repository would report a different owner per run.
	authors := map[string]int{"zoe": 5, "ada": 5}
	first := summarise("m", authors, 10).TopAuthor
	for i := 0; i < 50; i++ {
		if got := summarise("m", authors, 10).TopAuthor; got != first {
			t.Fatalf("TopAuthor flipped between runs: %q then %q", first, got)
		}
	}
	if first != "ada" {
		t.Errorf("TopAuthor = %q, want the lexicographically first tied author", first)
	}
}

func TestModuleAuthorshipMinorAndMajorAreComplementary(t *testing.T) {
	m := summarise("m", map[string]int{"owner": 99, "drive-by": 1}, 100)
	if !m.IsMajor("owner") || m.IsMinor("owner") {
		t.Error("owner at 99% should be major")
	}
	if !m.IsMinor("drive-by") || m.IsMajor("drive-by") {
		t.Error("contributor at 1% should be minor (under the 5% line)")
	}
	// Somebody with no commits at all holds 0% and is therefore minor.
	if !m.IsMinor("stranger") {
		t.Error("an author absent from the module should read as minor")
	}
}

func TestReadAuthorshipReportsNoGit(t *testing.T) {
	a := ReadAuthorship(t.TempDir(), 100, storeWithModules("pkg/a"))
	if a.Cause != AuthorshipNoGit {
		t.Errorf("Cause = %q, want %q", a.Cause, AuthorshipNoGit)
	}
	if _, ok := a.Module("pkg/a"); ok {
		t.Error("a repository git cannot read must claim no authorship")
	}
}

func TestReadAuthorshipReportsEmptyWindow(t *testing.T) {
	a := ReadAuthorship(t.TempDir(), 0, storeWithModules("pkg/a"))
	if a.Cause != AuthorshipEmpty {
		t.Errorf("Cause = %q, want %q", a.Cause, AuthorshipEmpty)
	}
}

func TestReadAuthorshipAggregatesRealCommits(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_DATE=2020-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2020-01-01T00:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(author, path, body string) {
		t.Helper()
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("-c", "user.name="+author, "-c", "user.email="+author+"@e.test",
			"commit", "-m", "c", "--author", author+" <"+author+"@e.test>")
	}

	git("init", "-q", "-b", "main")
	for i := 0; i < 9; i++ {
		commit("ada", "pkg/auth/a.go", "package auth //"+string(rune('a'+i)))
	}
	commit("zoe", "pkg/auth/b.go", "package auth")

	a := ReadAuthorship(repo, 500, storeWithModules("pkg/auth"))
	if a.Cause != "" {
		t.Fatalf("Cause = %q, want a whole measurement", a.Cause)
	}
	m, ok := a.Module("pkg/auth")
	if !ok {
		t.Fatal("pkg/auth absent from the authorship read")
	}
	if m.Commits != 10 || m.Total != 2 {
		t.Fatalf("Commits/Total = %d/%d, want 10/2", m.Commits, m.Total)
	}
	if m.TopAuthor != "ada" || m.TopShare < 0.89 {
		t.Errorf("owner = %q at %.2f, want ada at 0.90", m.TopAuthor, m.TopShare)
	}
	if !m.IsMajor("ada") || !m.IsMajor("zoe") {
		t.Error("both contributors clear 5% in a ten-commit module")
	}
}

// The mailmap format string is what makes two spellings of one contributor collapse
// into a single share. Asserted directly: nothing else in the aggregation would fail
// if %aN quietly became %an, and the resulting double-counting reads as a real split.
func TestReadAuthorshipUsesMailmapAwareFormat(t *testing.T) {
	src, err := os.ReadFile("authorship.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `"--format=%x00%aN"`) {
		t.Error("git log must use %aN, which applies .mailmap; %an does not")
	}
}
