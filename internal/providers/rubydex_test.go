package providers

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func rubydexScript(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "examples", "providers", "ruby", "rubydex", "enola_rubydex_provider.rb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reference provider missing: %v", err)
	}
	return path
}

// requireRubydex skips with a named reason when the machine cannot run the
// provider: the gem is a dependency enola does not ship, and the seam's own
// contract for that case is a named skip rather than a failure.
func requireRubydex(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby is not on PATH; the rubydex provider cannot run here")
	}
	if err := exec.Command("ruby", "-e", `require "rubydex"`).Run(); err != nil {
		t.Skip("this ruby cannot load the rubydex gem; the rubydex provider cannot run here")
	}
}

// The fixture is a three-class hierarchy with a mixin, a cross-class call and
// a same-class call, written so every emission rule fires exactly once and the
// omissions (enclosing-class receiver, built-in ancestors) are checkable.
func writeRubydexFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	files := map[string]string{
		"Gemfile":                 "source \"https://rubygems.org\"\n",
		"app/models/base.rb":      "class Base\nend\n",
		"app/models/auditable.rb": "module Auditable\n  def audit; end\nend\n",
		"app/models/named.rb":     "Named = Base\nclass Named\n  def go; end\nend\n",
		"app/models/invoice.rb": "class Invoice < Base\n" +
			"  include Auditable\n" +
			"  def total\n" +
			"    sum\n" +
			"  end\n" +
			"  def sum; end\n" +
			"end\n",
		"app/services/ledger.rb": "class Ledger\n" +
			"  def record(invoice)\n" +
			"    Invoice.count\n" +
			"  end\n" +
			"end\n",
	}
	for rel, body := range files {
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestRubydexProvider_GoldenThroughTheSeam(t *testing.T) {
	requireRubydex(t)
	repo := writeRubydexFixture(t)
	ff, records := Run(context.Background(), []Provider{{
		Name:            "rubydex",
		Command:         []string{"ruby", rubydexScript(t)},
		ExpectedVersion: "0.1.0",
	}}, repo, nil)
	if len(records) != 1 || records[0].Skipped {
		t.Fatalf("census = %+v, want a clean run", records)
	}
	if records[0].Version != "0.1.0" {
		t.Errorf("reported version = %q", records[0].Version)
	}
	if records[0].Census == nil || records[0].Census.FilesSeen != 5 {
		t.Errorf("census = %+v, want the five workspace files seen", records[0].Census)
	}
	var aliasSkips int
	for _, cause := range records[0].Census.SkipCauses {
		if cause.Cause == "declaration is a constant alias, not a class" {
			aliasSkips = cause.Count
		}
	}
	if aliasSkips != 1 {
		t.Errorf("census = %+v, want the reopened alias counted as one named skip", records[0].Census)
	}

	byName := map[string]int{}
	for i, f := range ff {
		byName[f.Name] = i
		if f.Kind != "dependency" {
			t.Errorf("%s: kind %q, every rubydex fact is a dependency", f.Name, f.Kind)
		}
		if !strings.HasPrefix(f.Name, "rubydex-") {
			t.Errorf("%s: a rubydex fact must carry its prefix so it cannot collide with an extractor identity", f.Name)
		}
	}
	want := []struct{ name, relation, target, level string }{
		{"rubydex-ancestor: Invoice -> Auditable", "implements", "Auditable", LevelResolved},
		{"rubydex-ancestor: Invoice -> Base", "implements", "Base", LevelResolved},
		{"rubydex-call: Ledger#record -> Invoice.count", "calls", "Invoice.count", LevelConstantReceiver},
		{"rubydex-ref: Invoice -> Auditable", "depends_on", "Auditable", LevelResolved},
		{"rubydex-ref: Invoice -> Base", "depends_on", "Base", LevelResolved},
		{"rubydex-ref: Ledger#record -> Invoice", "depends_on", "Invoice", LevelResolved},
	}
	for _, w := range want {
		i, ok := byName[w.name]
		if !ok {
			t.Errorf("missing %q among %d facts", w.name, len(ff))
			continue
		}
		f := ff[i]
		if len(f.Relations) != 1 || f.Relations[0].Kind != w.relation || f.Relations[0].Target != w.target {
			t.Errorf("%s: relations = %+v, want one %s edge to %s", w.name, f.Relations, w.relation, w.target)
		}
		if f.Props[PropResolutionLevel] != w.level {
			t.Errorf("%s: level = %v, want %s", w.name, f.Props[PropResolutionLevel], w.level)
		}
		if f.Line == 0 {
			t.Errorf("%s: line must be 1-based, got 0", w.name)
		}
	}
	for name := range byName {
		if strings.Contains(name, "Invoice#total -> Invoice") {
			t.Errorf("the enclosing-class receiver is the extractor's to say, got %q", name)
		}
		for _, builtIn := range []string{"-> Object", "-> Kernel", "-> BasicObject"} {
			if strings.HasSuffix(name, builtIn) {
				t.Errorf("built-in ancestors are omitted, got %q", name)
			}
		}
	}
	for _, f := range ff {
		if strings.HasPrefix(f.Name, "rubydex-ancestor: Invoice -> Base") && f.Props["ancestor_distance"] != float64(2) {
			t.Errorf("Base sits behind the mixin in resolution order, distance = %v", f.Props["ancestor_distance"])
		}
	}
}

func TestRubydexProvider_OutputIsSortedAndDeterministic(t *testing.T) {
	requireRubydex(t)
	repo := writeRubydexFixture(t)
	script := rubydexScript(t)
	run := func() []byte {
		out, err := exec.Command("ruby", script, repo).Output()
		if err != nil {
			t.Fatalf("provider run failed: %v", err)
		}
		return out
	}
	first, second := run(), run()
	if !bytes.Equal(first, second) {
		t.Fatalf("provider output differs across identical runs:\n%s\nvs\n%s", first, second)
	}
	lines := strings.Split(strings.TrimSpace(string(first)), "\n")
	if !sort.StringsAreSorted(lines) {
		t.Errorf("output lines are not sorted:\n%s", first)
	}
}

// The seam's contract when the tool is absent: the provider is a named skip in
// the census and the snapshot proceeds without it.
func TestRubydexProvider_MissingGemIsANamedSkip(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby is not on PATH")
	}
	repo := writeRubydexFixture(t)
	_, records := Run(context.Background(), []Provider{{
		Name:            "rubydex",
		Command:         []string{"ruby", "-e", `require "a-gem-nobody-installed"`, rubydexScript(t)},
		ExpectedVersion: "0.1.0",
	}}, repo, nil)
	if len(records) != 1 || !records[0].Skipped || records[0].Reason == "" {
		t.Fatalf("census = %+v, want a named skip", records)
	}
}

// A repository without a Gemfile is not a workspace Rubydex can index. The
// provider says so in its census and contributes nothing, rather than failing
// the run on every non-Ruby member of a cluster.
func TestRubydexProvider_NoGemfileIsARefusalNotAFailure(t *testing.T) {
	requireRubydex(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "index.js"), []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ff, records := Run(context.Background(), []Provider{{
		Name:            "rubydex",
		Command:         []string{"ruby", rubydexScript(t)},
		ExpectedVersion: "0.1.0",
	}}, repo, nil)
	if len(ff) != 0 {
		t.Fatalf("nothing to index must emit nothing, got %d facts", len(ff))
	}
	if len(records) != 1 || records[0].Skipped {
		t.Fatalf("census = %+v, want a clean run that contributed nothing", records)
	}
	if records[0].Census == nil || len(records[0].Census.SkipCauses) != 1 || !strings.Contains(records[0].Census.SkipCauses[0].Cause, "no Gemfile") {
		t.Fatalf("the census must name the refusal: %+v", records[0].Census)
	}
}
