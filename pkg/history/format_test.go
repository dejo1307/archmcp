package history

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A store written before the marker existed carries none, and there was only
// ever one format then — so absence is compatibility, never an error.
func TestRead_MissingMarkerIsFormatOne(t *testing.T) {
	root := t.TempDir()
	writeLog(t, root, true, entry("aaa1", "2026-08-01T10:00:00Z", "c0ffee1"))
	got, err := Read(root)
	if err != nil {
		t.Fatalf("a marker-less store must read as format 1, got: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
}

func TestRead_KnownMarkerReads(t *testing.T) {
	root := t.TempDir()
	writeLog(t, root, true, entry("aaa1", "2026-08-01T10:00:00Z", "c0ffee1"))
	if err := os.WriteFile(filepath.Join(root, FormatFileName), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root); err != nil {
		t.Fatalf("the current format must read, got: %v", err)
	}
}

// An unknown marker is a store some newer build wrote. The refusal is typed —
// a caller can tell it from corruption — and the message says what to do,
// because "unsupported" with no way forward is a dead end dressed as an error.
func TestRead_UnknownMarkerRefusesAndExplains(t *testing.T) {
	root := t.TempDir()
	writeLog(t, root, true, entry("aaa1", "2026-08-01T10:00:00Z", "c0ffee1"))
	if err := os.WriteFile(filepath.Join(root, FormatFileName), []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(root)
	var unknown *UnknownFormatError
	if !errors.As(err, &unknown) {
		t.Fatalf("want *UnknownFormatError, got %v", err)
	}
	if unknown.Version != "2" {
		t.Errorf("refused version = %q, want 2", unknown.Version)
	}
	for _, want := range []string{"refusing to read", "upgrade enola", "format \"2\""} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must explain itself with %q", err, want)
		}
	}
}
