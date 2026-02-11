package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSourceWindow(t *testing.T) {
	// Create a 10-line temp file
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "line "+string(rune('0'+i)))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		centerLine   int
		contextLines int
		wantStart    int
		wantEnd      int
	}{
		{"center middle", 5, 6, 2, 8},
		{"center at start", 1, 10, 1, 6},
		{"center at end", 10, 10, 5, 10},
		{"context larger than file", 5, 20, 1, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readSourceWindow(path, tt.centerLine, tt.contextLines)
			if err != nil {
				t.Fatalf("readSourceWindow: %v", err)
			}

			outputLines := strings.Split(strings.TrimRight(got, "\n"), "\n")

			// Verify first line starts with expected line number
			firstLine := outputLines[0]
			if !strings.Contains(firstLine, "│") {
				t.Fatalf("expected line number format with │, got: %s", firstLine)
			}

			// Count output lines
			expectedCount := tt.wantEnd - tt.wantStart + 1
			if len(outputLines) != expectedCount {
				t.Errorf("got %d output lines, want %d (lines %d-%d)",
					len(outputLines), expectedCount, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestReadSourceWindow_SingleLineFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.go")
	if err := os.WriteFile(path, []byte("only line"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readSourceWindow(path, 1, 30)
	if err != nil {
		t.Fatalf("readSourceWindow: %v", err)
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line for single-line file, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "only line") {
		t.Errorf("expected output to contain 'only line', got: %s", lines[0])
	}
}

func TestReadSourceWindow_LineNumberFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readSourceWindow(path, 3, 4)
	if err != nil {
		t.Fatalf("readSourceWindow: %v", err)
	}

	// Should have format "   N│ content"
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.Contains(line, "│") {
			t.Errorf("line missing │ separator: %q", line)
		}
	}
}
