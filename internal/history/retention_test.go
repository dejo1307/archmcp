package history

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	pkghistory "github.com/enola-labs/enola/pkg/history"
)

func TestAppend_RevisionCapKeepsTheLastN(t *testing.T) {
	root := t.TempDir()
	for i := 1; i <= 8; i++ {
		mustAppend(t, root,
			rev(fmt.Sprintf("aaa%d", i), fmt.Sprintf("2026-08-0%dT10:00:00Z", i), fmt.Sprintf("c%d", i), false),
			Options{RevisionKeep: 5})
	}
	got := readAll(t, root)
	if len(got) != 5 {
		t.Fatalf("want the last 5 revisions, got %d", len(got))
	}
	if got[0].ID != "sha256:aaa4" || got[4].ID != "sha256:aaa8" {
		t.Errorf("want aaa4..aaa8, got %s..%s", got[0].ID, got[4].ID)
	}
	// Seq keeps counting past the drop, so a revision somebody has on screen is never
	// renumbered by retention.
	if got[4].Seq != 8 {
		t.Errorf("newest seq = %d, want 8", got[4].Seq)
	}
}

func TestAppend_NegativeRevisionKeepDisablesTheCap(t *testing.T) {
	root := t.TempDir()
	for i := 1; i <= 8; i++ {
		mustAppend(t, root,
			rev(fmt.Sprintf("aaa%d", i), fmt.Sprintf("2026-08-0%dT10:00:00Z", i), fmt.Sprintf("c%d", i), false),
			Options{RevisionKeep: -1})
	}
	if got := readAll(t, root); len(got) != 8 {
		t.Fatalf("negative keep must retain everything, got %d of 8", len(got))
	}
}

func TestAppend_DefaultRevisionCapIsTwoHundred(t *testing.T) {
	root := t.TempDir()
	for i := 1; i <= DefaultRevisionKeep+3; i++ {
		mustAppend(t, root,
			rev(fmt.Sprintf("id%04d", i), fmt.Sprintf("2026-08-01T10:%02d:%02dZ", i/60, i%60), fmt.Sprintf("c%04d", i), false),
			Options{})
	}
	got := readAll(t, root)
	if len(got) != DefaultRevisionKeep {
		t.Fatalf("want %d revisions under the default cap, got %d", DefaultRevisionKeep, len(got))
	}
	if got[0].ID != "sha256:id0004" {
		t.Errorf("oldest surviving revision = %s, want the fourth", got[0].ID)
	}
}

func TestAppend_StampsTheFormatMarker(t *testing.T) {
	root := t.TempDir()
	mustAppend(t, root, rev("aaa1", "2026-08-01T10:00:00Z", "c1", false), Options{})
	raw, err := os.ReadFile(filepath.Join(root, pkghistory.FormatFileName))
	if err != nil {
		t.Fatalf("the append must stamp the marker: %v", err)
	}
	if string(raw) != "1\n" {
		t.Errorf("marker = %q, want the current format", raw)
	}
}

func TestAppend_RefusesAnUnknownFormat(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, pkghistory.FormatFileName), []byte("9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Append(root, rev("aaa1", "2026-08-01T10:00:00Z", "c1", false), Options{})
	var unknown *pkghistory.UnknownFormatError
	if !errors.As(err, &unknown) {
		t.Fatalf("want the named refuse-and-explain, got %v", err)
	}
	if unknown.Version != "9" {
		t.Errorf("refused version = %q, want 9", unknown.Version)
	}
	if _, statErr := os.Stat(filepath.Join(root, pkghistory.LogFileName)); !os.IsNotExist(statErr) {
		t.Errorf("a refused append must write nothing, stat: %v", statErr)
	}
}

// The store round-trips deterministically: the same appends into two roots
// produce byte-identical logs, and reading a log back and rewriting it
// reproduces it byte for byte — retention rewrites included.
func TestAppend_RoundTripIsByteIdenticalAndDeterministic(t *testing.T) {
	build := func() string {
		root := t.TempDir()
		for i := 1; i <= 8; i++ {
			mustAppend(t, root,
				rev(fmt.Sprintf("aaa%d", i), fmt.Sprintf("2026-08-0%dT10:00:00Z", i), fmt.Sprintf("c%d", i), false),
				Options{RevisionKeep: 5})
		}
		return root
	}
	first, second := build(), build()
	firstLog, err := os.ReadFile(filepath.Join(first, pkghistory.LogFileName))
	if err != nil {
		t.Fatal(err)
	}
	secondLog, err := os.ReadFile(filepath.Join(second, pkghistory.LogFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstLog, secondLog) {
		t.Fatalf("identical appends produced different logs:\n%s\nvs\n%s", firstLog, secondLog)
	}

	entries := readAll(t, first)
	if err := rewrite(filepath.Join(first, pkghistory.LogFileName), entries); err != nil {
		t.Fatal(err)
	}
	rewritten, err := os.ReadFile(filepath.Join(first, pkghistory.LogFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstLog, rewritten) {
		t.Fatalf("read-then-rewrite must reproduce the log byte for byte:\n%s\nvs\n%s", firstLog, rewritten)
	}
}
