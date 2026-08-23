package rubydex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/facts"
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

// A qualified read arrives from the engine as one reference per segment. The
// dependency is the leaf, carried with the file that defines it; the segments
// before it are its path, never dependencies of their own, so a prefix that is
// reopened in many files cannot land the read on one of them. A reference that
// resolves to nothing is kept as a fact with its cause rather than dropped.
func TestCollect_EmitsOneDependencyPerResolvedLeaf(t *testing.T) {
	lib := requireLibrary(t)
	repo := t.TempDir()
	files := map[string]string{
		"Gemfile":            "source \"https://rubygems.org\"\n",
		"lib/foo.rb":         "module Foo\nend\n",
		"lib/foo/version.rb": "module Foo\n  VERSION = \"1\"\nend\n",
		"lib/foo/cli.rb":     "module Foo\n  class CLI\n  end\nend\n",
		"lib/named.rb":       "class Base; end\nNamed = Base\n",
		"lib/use.rb": "class Use\n" +
			"  def v\n" +
			"    Foo::VERSION\n" +
			"    Named\n" +
			"    Missing::Deep\n" +
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
	result := Collect(context.Background(), lib, repo)
	byName := map[string]facts.Fact{}
	for _, f := range result.Facts {
		byName[f.Name] = f
	}
	if _, prefix := byName["rubydex-ref: Use#v -> Foo"]; prefix {
		t.Errorf("the prefix of a qualified read must not be a dependency of its own: %v", byName)
	}
	leaf, ok := byName["rubydex-ref: Use#v -> Foo::VERSION"]
	if !ok || len(leaf.Relations) != 1 || leaf.Relations[0].Target != "Foo::VERSION" {
		t.Fatalf("the leaf is the dependency: %+v", leaf)
	}
	if leaf.Props["target_file"] != "lib/foo/version.rb" {
		t.Errorf("target_file = %v, want the file defining the leaf", leaf.Props["target_file"])
	}
	if !reflect.DeepEqual(leaf.Props["path_prefixes"], []string{"Foo"}) {
		t.Errorf("path_prefixes = %v, want the segments before the leaf", leaf.Props["path_prefixes"])
	}
	alias, ok := byName["rubydex-ref: Use#v -> Named"]
	if !ok || len(alias.Relations) != 0 || alias.Props["resolution_cause"] != "alias" {
		t.Errorf("a read of a constant alias is a named miss, not an edge: %+v", alias)
	}
	missing, ok := byName["rubydex-ref: Use#v -> Missing::Deep"]
	if !ok || len(missing.Relations) != 0 || missing.Props["resolution_cause"] != "unresolved" {
		t.Errorf("an unresolved read is a named miss with its written path: %+v", missing)
	}
	if _, prefix := byName["rubydex-ref: Use#v -> Missing"]; prefix {
		t.Error("an unresolved prefix folds into its leaf like a resolved one")
	}
	causes := map[string]int{}
	for _, cause := range result.Census.SkipCauses {
		causes[cause.Cause] = cause.Count
	}
	if causes["unresolved constant reference"] != 1 || causes["reference resolves to a constant alias"] != 1 {
		t.Errorf("census = %+v, want the two misses counted by cause", result.Census)
	}
}

// A name defined in several files carries no target_file: the engine names the
// declaration, not which reopening a read lands on, and a guess here would be
// the bug restated.
func TestCollect_ReopenedLeafCarriesNoTargetFile(t *testing.T) {
	lib := requireLibrary(t)
	repo := t.TempDir()
	files := map[string]string{
		"Gemfile":        "source \"https://rubygems.org\"\n",
		"lib/foo.rb":     "module Foo\nend\n",
		"lib/foo/cli.rb": "module Foo\n  class CLI\n  end\nend\n",
		"lib/use.rb":     "class Use\n  def v\n    Foo\n  end\nend\n",
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
	result := Collect(context.Background(), lib, repo)
	for _, f := range result.Facts {
		if f.Name != "rubydex-ref: Use#v -> Foo" {
			continue
		}
		if _, carried := f.Props["target_file"]; carried {
			t.Errorf("a read of a module reopened in two files must not pick one: %+v", f.Props)
		}
		return
	}
	t.Fatal("the bare read of a reopened module is still a dependency on the name")
}

// The shape that hung the provider on a Rails monolith: `<LibDDWAF>` at
// lib_ddwaf.rb:262 spans to line 267, so its end column belongs to another
// line and, at 25 against a start column of 27, satisfies the adjacency
// arithmetic against itself. Before the identity and same-line conditions the
// walk returned it as its own predecessor and never terminated, which cost a
// cluster regeneration 26 minutes and 6.7GB before it was killed.
func TestPathPrefixes_AReferenceIsNeverItsOwnPredecessor(t *testing.T) {
	leaf := &constantReference{
		id:       1,
		location: Location{URI: "file:///w/lib_ddwaf.rb", StartLine: 262, EndLine: 267, StartColumn: 27, EndColumn: 25},
	}
	byLine := map[string][]*constantReference{
		leaf.location.URI + "\x00262": {leaf},
	}
	c := &collector{declarationNames: map[uint64]string{}}

	walked := make(chan []string, 1)
	go func() { walked <- c.pathPrefixes(byLine, leaf) }()

	select {
	case prefixes := <-walked:
		if len(prefixes) != 0 {
			t.Fatalf("a reference on its own line has no prefixes, got %v", prefixes)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pathPrefixes did not return: the reference was accepted as its own predecessor")
	}
}
