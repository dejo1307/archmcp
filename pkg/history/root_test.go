package history

import (
	"path/filepath"
	"strings"
	"testing"
)

// The default lives outside the repository. That is the property that lets `enola log`
// run against a checkout the user does not own, and that stops `rm -rf .enola` — a
// routine thing to do, since everything in there is derivable — from deleting the one
// thing that is not.
func TestRoot_DefaultsOutsideTheRepository(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	repo := t.TempDir()

	root, err := Root(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(root, repo) {
		t.Fatalf("the default history root must not be inside the repository: %s", root)
	}
	if !strings.HasPrefix(root, home) {
		t.Fatalf("want the history under the home dir, got %s", root)
	}
	if filepath.Base(root) != "history" {
		t.Errorf("want a directory named history, got %s", root)
	}
}

// Same repository, same root — however it was named on the way in. Without symlink
// resolution a macOS /var path and its /private/var twin give one repository two
// histories, and neither ever shows the other's revisions.
func TestRoot_IsStableAcrossHowTheRepoWasNamed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	repo := t.TempDir()

	direct, err := Root(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	viaDotSegments, err := Root(filepath.Join(repo, "sub", ".."), "")
	if err != nil {
		t.Fatal(err)
	}
	if direct != viaDotSegments {
		t.Errorf("one repository must have one history root:\n  %s\n  %s", direct, viaDotSegments)
	}
}

// Two repositories that merely share a base name must not share a history — the key
// carries a hash of the full path for exactly this.
func TestRoot_DistinguishesRepositoriesWithTheSameName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	parent := t.TempDir()
	a := filepath.Join(parent, "a", "api")
	b := filepath.Join(parent, "b", "api")

	rootA, err := Root(a, "")
	if err != nil {
		t.Fatal(err)
	}
	rootB, err := Root(b, "")
	if err != nil {
		t.Fatal(err)
	}
	if rootA == rootB {
		t.Fatalf("two repositories named api collided on %s", rootA)
	}
}

func TestRoot_Override(t *testing.T) {
	repo := t.TempDir()

	// Relative: the team wants the history to travel with the checkout.
	rel, err := Root(repo, ".enola/history")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo, ".enola", "history"); rel != want {
		t.Errorf("relative override = %s, want %s", rel, want)
	}

	// Absolute: taken as given, e.g. a shared location a CI job publishes from.
	abs := filepath.Join(t.TempDir(), "elsewhere")
	got, err := Root(repo, abs)
	if err != nil {
		t.Fatal(err)
	}
	if got != abs {
		t.Errorf("absolute override = %s, want %s", got, abs)
	}
}
