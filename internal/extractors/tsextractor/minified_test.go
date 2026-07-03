package tsextractor

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestIsMinifiedSource(t *testing.T) {
	longLine := "var x = \"" + strings.Repeat("z", minifiedLineThreshold+100) + "\";"
	if !isMinifiedSource([]byte(longLine)) {
		t.Errorf("isMinifiedSource(one very long line) = false, want true")
	}
	// A build banner on line 1 followed by a huge minified chunk (the common shape).
	bundle := "/* Build */\n" + longLine + "\nfunction f(){}\n"
	if !isMinifiedSource([]byte(bundle)) {
		t.Errorf("isMinifiedSource(banner + long line) = false, want true")
	}

	ordinary := "export function add(a, b) {\n  return a + b;\n}\n"
	if isMinifiedSource([]byte(ordinary)) {
		t.Errorf("isMinifiedSource(ordinary source) = true, want false")
	}
	// Many short lines, none over the threshold, even if the file is large overall.
	manyLines := strings.Repeat("const x = compute();\n", 500)
	if isMinifiedSource([]byte(manyLines)) {
		t.Errorf("isMinifiedSource(many short lines) = true, want false")
	}
}

func TestExtract_SkipsMinifiedBundle(t *testing.T) {
	longLine := "var bundledLibrary = \"" + strings.Repeat("z", minifiedLineThreshold+100) + "\";"
	files := map[string]string{
		"src/util.ts":             "export function realHelper() {\n  return 1;\n}\n",
		"assets/vendor/bundle.js": longLine,
	}
	got := extractAll(t, files, false)

	// The hand-written symbol is extracted.
	if _, ok := findFact(got, "src.realHelper"); !ok {
		t.Errorf("expected symbol fact for src.realHelper; got %+v", got)
	}
	// The minified bundle contributes no facts at all — no symbols and no module
	// fact for its directory.
	for _, f := range got {
		if strings.Contains(f.File, "assets/vendor") || strings.Contains(f.Name, "assets/vendor") {
			t.Errorf("minified bundle produced a fact it should have been skipped: %+v", f)
		}
	}
	for _, m := range findFactsByKind(got, facts.KindModule) {
		if m.Name == "assets/vendor" {
			t.Errorf("minified-only directory should not emit a module fact; got %+v", m)
		}
	}
}
