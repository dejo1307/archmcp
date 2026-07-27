package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestScanFootprint(t *testing.T) {
	repo := t.TempDir()
	enolaDir := filepath.Join(repo, ".enola")
	if err := os.MkdirAll(enolaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Two source files with known sizes.
	srcA := filepath.Join(repo, "a.go")
	srcB := filepath.Join(repo, "pkg", "b.go")
	if err := os.MkdirAll(filepath.Dir(srcB), 0o755); err != nil {
		t.Fatal(err)
	}
	contentA := make([]byte, 400)
	contentB := make([]byte, 400)
	if err := os.WriteFile(srcA, contentA, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcB, contentB, 0o644); err != nil {
		t.Fatal(err)
	}

	// Minimal snapshot.meta.json referencing the two files by relative path.
	meta := map[string]any{
		"repo_path":     repo,
		"fact_count":    42,
		"insight_count": 7,
		"files_parsed":  2,
		"file_hashes": []map[string]string{
			{"path": "a.go"},
			{"path": "pkg/b.go"},
		},
	}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(enolaDir, "snapshot.meta.json"), metaBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	// A facts artifact so the .enola dir has some listed size.
	if err := os.WriteFile(filepath.Join(enolaDir, "facts.jsonl"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	// A rolled-up subdir.
	prev := filepath.Join(enolaDir, "previous")
	if err := os.MkdirAll(prev, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prev, "facts.jsonl"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}

	fp := ScanFootprint(enolaDir)

	if fp.Facts != 42 {
		t.Errorf("Facts: got %d, want 42", fp.Facts)
	}
	if fp.FilesIndexed != 2 {
		t.Errorf("FilesIndexed: got %d, want 2", fp.FilesIndexed)
	}
	if fp.SourceBytes != 800 {
		t.Errorf("SourceBytes: got %d, want 800", fp.SourceBytes)
	}
	if fp.SourceTokens != 800/charsPerToken {
		t.Errorf("SourceTokens: got %d, want %d", fp.SourceTokens, 800/charsPerToken)
	}
	// total = facts.jsonl(100) + meta(len) + previous/(50)
	if fp.TotalBytes < 150 {
		t.Errorf("TotalBytes too small: got %d", fp.TotalBytes)
	}

	// previous/ must appear as a rolled-up dir entry.
	var sawPrev bool
	for _, f := range fp.Files {
		if f.Name == "previous" {
			sawPrev = true
			if !f.IsDir || f.Bytes != 50 {
				t.Errorf("previous entry: got IsDir=%v bytes=%d, want true/50", f.IsDir, f.Bytes)
			}
		}
	}
	if !sawPrev {
		t.Error("expected previous/ subdir in footprint")
	}
}

// The engine's own measurement covers exactly the files that produced facts, so
// it must win outright over anything derived from the walked-file list.
func TestScanFootprintPrefersEngineMeasurement(t *testing.T) {
	repo := t.TempDir()
	enolaDir := filepath.Join(repo, ".enola")
	if err := os.MkdirAll(enolaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.go"), make([]byte, 400), 0o644); err != nil {
		t.Fatal(err)
	}

	meta := map[string]any{
		"repo_path":    repo,
		"source_bytes": 12345,
		"file_hashes":  []map[string]string{{"path": "a.go"}},
	}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(enolaDir, "snapshot.meta.json"), metaBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	fp := ScanFootprint(enolaDir)
	if fp.SourceBytes != 12345 {
		t.Errorf("SourceBytes: got %d, want the engine's 12345", fp.SourceBytes)
	}
}

// file_hashes is the files_SEEN set. Summing it whole counts images, media and
// vendored databases as though an agent would have read them — on a real repo
// that turned a 3.1M-token corpus into 121M. The fallback must filter to source.
func TestScanFootprintFallbackExcludesNonSource(t *testing.T) {
	repo := t.TempDir()
	enolaDir := filepath.Join(repo, ".enola")
	if err := os.MkdirAll(filepath.Join(repo, "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(enolaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.go"), make([]byte, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	// A big binary the walker saw but no extractor ever parsed.
	if err := os.WriteFile(filepath.Join(repo, "uploads", "photo.jpg"), make([]byte, 4_000_000), 0o644); err != nil {
		t.Fatal(err)
	}

	// No source_bytes: an old snapshot, so the fallback path runs.
	meta := map[string]any{
		"repo_path": repo,
		"file_hashes": []map[string]string{
			{"path": "a.go"},
			{"path": "uploads/photo.jpg"},
		},
	}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(enolaDir, "snapshot.meta.json"), metaBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	fp := ScanFootprint(enolaDir)
	if fp.SourceBytes != 400 {
		t.Errorf("SourceBytes: got %d, want 400 (the .jpg must not count as corpus)", fp.SourceBytes)
	}
}

func TestScanFootprintMissingDir(t *testing.T) {
	fp := ScanFootprint(filepath.Join(t.TempDir(), "does-not-exist"))
	if fp.TotalBytes != 0 || fp.Facts != 0 {
		t.Errorf("missing dir should yield zero footprint, got %+v", fp)
	}
}

func TestScanFootprintOldFormatNoFileHashes(t *testing.T) {
	// Old-format meta without file_hashes: source size unknown, but counts and
	// dir sizes still report.
	enolaDir := t.TempDir()
	meta := map[string]any{"fact_count": 10, "files_seen": 5}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(enolaDir, "snapshot.meta.json"), metaBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	fp := ScanFootprint(enolaDir)
	if fp.Facts != 10 {
		t.Errorf("Facts: got %d, want 10", fp.Facts)
	}
	if fp.FilesIndexed != 5 {
		t.Errorf("FilesIndexed should fall back to files_seen: got %d, want 5", fp.FilesIndexed)
	}
	if fp.SourceBytes != 0 {
		t.Errorf("SourceBytes should be 0 without file_hashes, got %d", fp.SourceBytes)
	}
}
