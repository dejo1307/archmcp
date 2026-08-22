package rubydex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func requireLibrary(t *testing.T) *Library {
	t.Helper()
	path, installed := Installed()
	if !installed {
		t.Skipf("the Rubydex library is not in the cache (%s); run `%s`", path, FetchHint)
	}
	lib, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

// The fixture is the reference script's: a three-class hierarchy with a
// mixin, a cross-class call, a same-class call, and a class reopened under a
// constant alias, so every emission and every named skip fires once.
func writeFixture(t *testing.T) string {
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

func TestCollect_EmitsTheReferenceScriptsFacts(t *testing.T) {
	lib := requireLibrary(t)
	repo := writeFixture(t)
	result := Collect(context.Background(), lib, repo)
	if result.Refusal != "" {
		t.Fatalf("refused: %s", result.Refusal)
	}
	if result.Census.FilesSeen != 5 {
		t.Errorf("census = %+v, want the five workspace files seen", result.Census)
	}
	want := map[string]struct{ relation, target, level string }{
		"rubydex-ancestor: Invoice -> Auditable":       {"implements", "Auditable", "resolved"},
		"rubydex-ancestor: Invoice -> Base":            {"implements", "Base", "resolved"},
		"rubydex-call: Ledger#record -> Invoice.count": {"calls", "Invoice.count", "constant-receiver"},
		"rubydex-ref: Invoice -> Auditable":            {"depends_on", "Auditable", "resolved"},
		"rubydex-ref: Invoice -> Base":                 {"depends_on", "Base", "resolved"},
		"rubydex-ref: Ledger#record -> Invoice":        {"depends_on", "Invoice", "resolved"},
		"rubydex-ref: app/models/named.rb -> Base":     {"depends_on", "Base", "resolved"},
	}
	got := map[string]struct{ relation, target, level string }{}
	for _, f := range result.Facts {
		if f.Kind != "dependency" || len(f.Relations) != 1 || f.Line < 1 {
			t.Errorf("%s: every fact is a dependency with one relation and a one-based line: %+v", f.Name, f)
		}
		got[f.Name] = struct{ relation, target, level string }{f.Relations[0].Kind, f.Relations[0].Target, f.Props["resolution_level"].(string)}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("facts differ\n got: %v\nwant: %v", got, want)
	}
	var aliasSkips, enclosingSkips int
	for _, cause := range result.Census.SkipCauses {
		switch cause.Cause {
		case "declaration is a constant alias, not a class":
			aliasSkips = cause.Count
		case "receiver is the lexical enclosing class":
			enclosingSkips = cause.Count
		}
	}
	if aliasSkips != 1 {
		t.Errorf("census = %+v, want the reopened alias counted once", result.Census)
	}
	if enclosingSkips != 1 {
		t.Errorf("census = %+v, want the same-class call counted once", result.Census)
	}
}

func TestCollect_IsDeterministic(t *testing.T) {
	lib := requireLibrary(t)
	repo := writeFixture(t)
	first := Collect(context.Background(), lib, repo)
	second := Collect(context.Background(), lib, repo)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two runs over the same tree must agree")
	}
}

func TestCollect_NoGemfileIsARefusalInTheCensus(t *testing.T) {
	lib := requireLibrary(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "a.rb"), []byte("class A; end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := Collect(context.Background(), lib, repo)
	if result.Refusal != "" || len(result.Facts) != 0 || len(result.Census.SkipCauses) != 1 || result.Census.SkipCauses[0].Cause != "no Gemfile: not a workspace Rubydex can index" {
		t.Fatalf("result = %+v, want a named refusal in the census and nothing emitted", result)
	}
}

func TestPlainName(t *testing.T) {
	for in, want := range map[string]string{"Invoice::<Invoice>": "Invoice", "total()": "total", "Billing::Charge": "Billing::Charge"} {
		if got := plainName(in); got != want {
			t.Errorf("plainName(%q) = %q, want %q", in, got, want)
		}
	}
	if !sameLexicalOwner("Invoice#total", "Invoice") || sameLexicalOwner("Ledger#record", "Invoice") {
		t.Error("the lexical owner is the name before the member separator")
	}
}
