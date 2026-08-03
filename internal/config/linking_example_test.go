package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLinkingExample_FromDocsParsesAndApplies keeps docs/EXTENDING.md honest.
//
// A config example is the one kind of documentation a reader copies verbatim, so it is the
// one most expensive to get wrong — and the easiest to get wrong silently, since a
// misspelled YAML key unmarshals to the zero value without complaint and the setting just
// never takes effect. This is the example from "Tuning without code", copied as written.
//
// If you edit that example, edit this. If this fails, the docs are lying.
func TestLinkingExample_FromDocsParsesAndApplies(t *testing.T) {
	const example = `
linking:
  framework_conventions:
    add: [BaseViewController, AppDelegate]
  generic_path_segments:
    remove: [status]
  non_contract_paths:
    add: ["/generated/"]
  thresholds:
    min_shared_symbols: 5
`
	dir := t.TempDir()
	path := filepath.Join(dir, "enola.yaml")
	if err := os.WriteFile(path, []byte(example), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("the documented example does not load: %v", err)
	}

	v, err := cfg.LinkingVocab()
	if err != nil {
		t.Fatalf("the documented example does not apply: %v", err)
	}

	if !v.FrameworkConventions["BaseViewController"] || !v.FrameworkConventions["AppDelegate"] {
		t.Error("framework_conventions.add did not take effect")
	}
	if !v.FrameworkConventions["ApplicationController"] {
		t.Error("an additive overlay discarded the defaults")
	}
	if v.GenericPathSegments["status"] {
		t.Error("generic_path_segments.remove did not take effect")
	}
	if !v.GenericPathSegments["health"] {
		t.Error("removing one segment disturbed another")
	}
	var found bool
	for _, p := range v.NonContractPaths {
		if p == "/generated/" {
			found = true
		}
	}
	if !found {
		t.Error("non_contract_paths.add did not take effect")
	}
	if v.Thresholds.MinSharedSymbols != 5 {
		t.Errorf("min_shared_symbols = %d, want 5", v.Thresholds.MinSharedSymbols)
	}
	// An omitted threshold must keep its default rather than collapsing to zero — the
	// reason ThresholdOverlay uses pointers.
	if v.Thresholds.MinSharedSegments == 0 {
		t.Error("an omitted threshold collapsed to zero instead of keeping its default")
	}
}
