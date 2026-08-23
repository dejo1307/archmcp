package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/facts"
)

// A repository with one line committed in 2024, one in 2026 and one never
// committed, graded by a rule dated 2025 against an empty history store: the
// 2024 line is reported, the 2026 line graded, the uncommitted line undated.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(nil, "init", "-q")
	run(nil, "config", "user.email", "t@example.com")
	run(nil, "config", "user.name", "Zoë Müller")
	write := func(lines ...string) {
		if err := os.WriteFile(filepath.Join(dir, "app/models/order.rb"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "app/models"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("class Order", "  render :old", "end")
	old := []string{"GIT_AUTHOR_DATE=2024-03-01T12:00:00Z", "GIT_COMMITTER_DATE=2024-03-01T12:00:00Z"}
	run(old, "add", ".")
	run(old, "commit", "-q", "-m", "old")
	write("class Order", "  render :old", "  render :fresh", "end")
	fresh := []string{"GIT_AUTHOR_DATE=2026-02-01T12:00:00Z", "GIT_COMMITTER_DATE=2026-02-01T12:00:00Z"}
	run(fresh, "add", ".")
	run(fresh, "commit", "-q", "-m", "fresh")
	write("class Order", "  render :old", "  render :fresh", "  render :uncommitted", "end")
	return dir
}

func breach(title string, line int) facts.Insight {
	in := dated(title, "2025-01-01")
	in.Evidence = append(in.Evidence, facts.Evidence{File: "app/models/order.rb", Line: line})
	return in
}

var noHistory RevisionAt = func(time.Time) (*facts.Snapshot, string, string, bool) { return nil, "", "", false }

func TestApplyTime_GitDatesWitnessesTheStoreCannot(t *testing.T) {
	repo := fixtureRepo(t)
	reader := NewBlameReader(repo, filepath.Join(repo, ".enola"))
	v := Verdict{Status: StatusRegression, Failures: []facts.Insight{
		breach("Constraint models-do-not-render violated: Order old", 2),
		breach("Constraint models-do-not-render violated: Order fresh", 3),
		breach("Constraint models-do-not-render violated: Order uncommitted", 4),
	}}
	out := ApplyTime(v, nil, noHistory, reader.Age)
	if len(out.Advisories) != 1 || !strings.Contains(out.Advisories[0].Description, "Last changed 2024-03-01 by git's author date, before the rule's date") {
		t.Fatalf("advisories = %+v", out.Advisories)
	}
	if len(out.Failures) != 2 {
		t.Fatalf("failures = %+v", out.Failures)
	}
	if !strings.Contains(out.Failures[0].Description, "Last changed 2026-02-01 by git's author date, after the rule's date") {
		t.Fatalf("fresh = %s", out.Failures[0].Description)
	}
	if !strings.Contains(out.Failures[1].Description, "Git could not date the witness line (uncommitted)") {
		t.Fatalf("uncommitted = %s", out.Failures[1].Description)
	}
	var causes []string
	for _, d := range out.Descriptive {
		causes = append(causes, d.Title)
	}
	if len(causes) != 1 || !strings.Contains(causes[0], "could not be dated by git: the witness line is uncommitted") {
		t.Fatalf("descriptive = %v", causes)
	}
	if err := reader.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".enola", "blame_cache.json")); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
}

// A second reader on an unchanged file answers from the cache without git:
// the tree's .git is hidden and the blob key alone finds the remembered date.
func TestBlameReader_RemembersByBlob(t *testing.T) {
	repo := fixtureRepo(t)
	first := NewBlameReader(repo, filepath.Join(repo, ".enola"))
	if at, cause := first.Age("app/models/order.rb", 2, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)); cause != "" || at.Year() != 2024 {
		t.Fatalf("first read: %v %q", at, cause)
	}
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}
	second := NewBlameReader(repo, filepath.Join(repo, ".enola"))
	second.gitOK = new(bool)
	*second.gitOK = true
	if err := os.Rename(filepath.Join(repo, ".git"), filepath.Join(repo, ".git-hidden")); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(filepath.Join(repo, ".git-hidden"), filepath.Join(repo, ".git")) }()
	if at, cause := second.Age("app/models/order.rb", 2, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)); cause != "" || at.Year() != 2024 {
		t.Fatalf("the cache must answer an unchanged blob with git hidden: %v %q", at, cause)
	}
}

func TestBlameReader_NoGitIsNamed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.rb"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := NewBlameReader(dir, filepath.Join(dir, ".enola"))
	if _, cause := reader.Age("a.rb", 1, time.Now()); cause != AgeNoGit {
		t.Fatalf("cause = %q", cause)
	}
}

func TestBlameReader_ShallowBoundaryNewerThanTheDateIsUnknown(t *testing.T) {
	origin := fixtureRepo(t)
	if out, err := exec.Command("git", "-C", origin, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if out, err := exec.Command("git", "-C", origin, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "tip").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	clone := filepath.Join(t.TempDir(), "shallow")
	if out, err := exec.Command("git", "clone", "-q", "--depth", "1", "file://"+origin, clone).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	reader := NewBlameReader(clone, filepath.Join(clone, ".enola"))
	if _, cause := reader.Age("app/models/order.rb", 2, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)); cause != AgeShallow {
		t.Fatalf("cause = %q, want shallow: the boundary commit is today, newer than the rule's date", cause)
	}
	if at, cause := reader.Age("app/models/order.rb", 2, time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)); cause != "" || at.IsZero() {
		t.Fatalf("a boundary older than the date is a real before: %v %q", at, cause)
	}
}
