package cppextractor

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// --- helpers ---

// extractProject writes the given files (relPath -> content) into a temp dir and
// runs the extractor over them.
func extractProject(t *testing.T, files map[string]string) []facts.Fact {
	t.Helper()
	dir := t.TempDir()
	var rel []string
	for p, content := range files {
		abs := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		rel = append(rel, p)
	}
	sort.Strings(rel) // deterministic ordering for dedup-related assertions
	out, err := New().Extract(context.Background(), dir, rel)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return out
}

func findFact(ff []facts.Fact, name string) (facts.Fact, bool) {
	for _, f := range ff {
		if f.Name == name {
			return f, true
		}
	}
	return facts.Fact{}, false
}

func countByName(ff []facts.Fact, name string) int {
	n := 0
	for _, f := range ff {
		if f.Name == name {
			n++
		}
	}
	return n
}

func factsOfSymbolKind(ff []facts.Fact, sk string) []facts.Fact {
	var out []facts.Fact
	for _, f := range ff {
		if f.Kind == facts.KindSymbol {
			if got, _ := f.Props["symbol_kind"].(string); got == sk {
				out = append(out, f)
			}
		}
	}
	return out
}

func hasRelation(f facts.Fact, kind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == kind && r.Target == target {
			return true
		}
	}
	return false
}

func mustFact(t *testing.T, ff []facts.Fact, name string) facts.Fact {
	t.Helper()
	f, ok := findFact(ff, name)
	if !ok {
		t.Fatalf("expected fact %q, not found. names: %v", name, factNames(ff))
	}
	return f
}

func factNames(ff []facts.Fact) []string {
	var out []string
	for _, f := range ff {
		out = append(out, f.Kind+":"+f.Name)
	}
	sort.Strings(out)
	return out
}

// --- Detect ---

func TestDetect(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"cpp source", map[string]string{"main.cpp": "int main(){}"}, true},
		{"hpp header only", map[string]string{"foo.hpp": "class Foo{};"}, true},
		{"cmake plus header", map[string]string{"CMakeLists.txt": "project(x)", "foo.h": "int x;"}, true},
		{"c only with makefile", map[string]string{"Makefile": "all:", "foo.c": "int x;"}, false},
		{"plain header only", map[string]string{"foo.h": "int x;"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for p, content := range c.files {
				if err := os.WriteFile(filepath.Join(dir, p), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := New().Detect(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Detect = %v, want %v", got, c.want)
			}
		})
	}
}

// --- Symbols & inheritance ---

func TestFreeFunction(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/a.cpp": "int compute(int x) { return x + 1; }",
	})
	f := mustFact(t, ff, "src.compute")
	if sk, _ := f.Props["symbol_kind"].(string); sk != facts.SymbolFunc {
		t.Errorf("symbol_kind = %v, want function", f.Props["symbol_kind"])
	}
	if !hasRelation(f, facts.RelDeclares, "src") {
		t.Errorf("missing declares relation to dir, got %+v", f.Relations)
	}
}

func TestClassInheritance(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/m.cpp": `
class Base {};
class Derived : public Base {};
`,
	})
	mustFact(t, ff, "src.Base")
	d := mustFact(t, ff, "src.Derived")
	if sk, _ := d.Props["symbol_kind"].(string); sk != facts.SymbolClass {
		t.Errorf("Derived symbol_kind = %v, want class", d.Props["symbol_kind"])
	}
	if !hasRelation(d, facts.RelImplements, "src.Base") {
		t.Errorf("Derived should implement src.Base (canonicalized), got %+v", d.Relations)
	}
}

func TestStructUnionEnum(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/t.cpp": `
struct Point { int x; int y; };
union Value { int i; float f; };
enum Color { Red, Green };
enum class Status { Ok, Err };
`,
	})
	if sk, _ := mustFact(t, ff, "src.Point").Props["symbol_kind"].(string); sk != facts.SymbolStruct {
		t.Errorf("Point should be struct, got %v", sk)
	}
	u := mustFact(t, ff, "src.Value")
	if u.Props["union"] != true {
		t.Errorf("Value should have union=true, got %+v", u.Props)
	}
	if sk, _ := mustFact(t, ff, "src.Color").Props["symbol_kind"].(string); sk != facts.SymbolEnum {
		t.Errorf("Color should be enum, got %v", sk)
	}
	s := mustFact(t, ff, "src.Status")
	if s.Props["scoped"] != true {
		t.Errorf("Status (enum class) should have scoped=true, got %+v", s.Props)
	}
}

func TestInlineMethod(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/c.cpp": `
class C {
public:
  void m() {}
};
`,
	})
	f := mustFact(t, ff, "src.C::m")
	if sk, _ := f.Props["symbol_kind"].(string); sk != facts.SymbolMethod {
		t.Errorf("C::m symbol_kind = %v, want method", f.Props["symbol_kind"])
	}
	if f.Props["receiver"] != "C" {
		t.Errorf("C::m receiver = %v, want C", f.Props["receiver"])
	}
}

// --- Header/source split (the key C++ challenge) ---

func TestOutOfLineMethodMergesAcrossFiles(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"sub/c.h": `
class C {
public:
  void m();
  void helper();
};
`,
		"sub/c.cpp": `
void C::m() { helper(); }
void C::helper() {}
`,
	})

	// Exactly one class fact and one fact per method (header decl merged with def).
	if n := countByName(ff, "sub.C"); n != 1 {
		t.Errorf("expected exactly 1 sub.C class fact, got %d", n)
	}
	if n := countByName(ff, "sub.C::m"); n != 1 {
		t.Errorf("expected exactly 1 sub.C::m fact, got %d", n)
	}

	m := mustFact(t, ff, "sub.C::m")
	// Definition (with body) wins for File.
	if !strings.HasSuffix(m.File, "c.cpp") {
		t.Errorf("sub.C::m File = %q, want the .cpp definition", m.File)
	}
	// The body's call to a sibling method is preserved and resolved.
	if !hasRelation(m, facts.RelCalls, "sub.C::helper") {
		t.Errorf("sub.C::m should call sub.C::helper, got %+v", m.Relations)
	}
	if m.Props["receiver"] != "C" {
		t.Errorf("sub.C::m receiver = %v, want C", m.Props["receiver"])
	}
}

// --- Namespaces ---

func TestNamespaceNesting(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/n.cpp": `
namespace a { namespace b { void f() {} } }
namespace c::d { void g() {} }
namespace { void anon() {} }
`,
	})
	mustFact(t, ff, "src.a::b::f")
	mustFact(t, ff, "src.c::d::g")
	mustFact(t, ff, "src.anon") // anonymous namespace contributes no scope component
}

func TestNamespacedFunctionMergesAcrossFiles(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"x/e.h": `
namespace E {
  int getOrder(int t);
}
`,
		"x/e.cpp": `
namespace E {}
int E::getOrder(int t) { return t; }
`,
	})
	if n := countByName(ff, "x.E::getOrder"); n != 1 {
		t.Errorf("expected exactly 1 x.E::getOrder fact, got %d: %v", n, factNames(ff))
	}
	f := mustFact(t, ff, "x.E::getOrder")
	if !strings.HasSuffix(f.File, "e.cpp") {
		t.Errorf("x.E::getOrder File = %q, want the .cpp definition", f.File)
	}
}

// --- Call graph ---

func TestCallGraph(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/g.cpp": `
class Msg { public: static void Error(const char *s); };
class Worker {
public:
  void run();
  void step();
};
void Worker::run() {
  this->step();
  Msg::Error("boom");
  Worker w = Worker();
}
`,
	})
	run := mustFact(t, ff, "src.Worker::run")
	if !hasRelation(run, facts.RelCalls, "src.Worker::step") {
		t.Errorf("run should call src.Worker::step, got %+v", run.Relations)
	}
	if !hasRelation(run, facts.RelCalls, "src.Msg::Error") {
		t.Errorf("run should call src.Msg::Error (canonicalized), got %+v", run.Relations)
	}
	if !hasRelation(run, facts.RelInstantiates, "src.Worker") {
		t.Errorf("run should instantiate src.Worker, got %+v", run.Relations)
	}
}

// --- Templates ---

func TestTemplates(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/tpl.cpp": `
template <class T> class Box { T value; };
class IntBox : public Box<int> {};
`,
	})
	box := mustFact(t, ff, "src.Box")
	if box.Props["templated"] != true {
		t.Errorf("Box should be templated, got %+v", box.Props)
	}
	ib := mustFact(t, ff, "src.IntBox")
	// Template args stripped; base resolves to src.Box.
	if !hasRelation(ib, facts.RelImplements, "src.Box") {
		t.Errorf("IntBox should implement src.Box (args stripped), got %+v", ib.Relations)
	}
}

// --- Includes ---

func TestIncludes(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"lib/other.h": "class Other {};",
		"lib/use.cpp": `
#include <vector>
#include "other.h"
void use() {}
`,
	})
	// System include produces no dependency fact.
	for _, f := range ff {
		if f.Kind == facts.KindDependency {
			if inc, _ := f.Props["include"].(string); strings.Contains(inc, "vector") {
				t.Errorf("system include <vector> should not yield a dependency fact")
			}
		}
	}
	// Quoted include resolves to the declaring module dir.
	var found bool
	for _, f := range ff {
		if f.Kind == facts.KindDependency {
			if inc, _ := f.Props["include"].(string); inc == "other.h" {
				found = true
				if !hasRelation(f, facts.RelImports, "lib") {
					t.Errorf("other.h dependency should import module 'lib', got %+v", f.Relations)
				}
				if f.Props["source"] != "internal" {
					t.Errorf("other.h should be internal, got %v", f.Props["source"])
				}
			}
		}
	}
	if !found {
		t.Errorf("expected a dependency fact for #include \"other.h\"")
	}
}

// --- Forward declarations & operators ---

func TestForwardDeclarationEmitsNoSymbol(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/f.cpp": `
class Fwd;
class Real { Fwd *p; };
`,
	})
	if _, ok := findFact(ff, "src.Fwd"); ok {
		t.Errorf("forward declaration src.Fwd should not emit a symbol")
	}
	mustFact(t, ff, "src.Real")
}

func TestOperatorOverload(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/o.cpp": `
class V {
public:
  bool operator==(const V &o) const;
};
`,
	})
	// Should not crash; the operator method is named and carries the receiver.
	ops := factsOfSymbolKind(ff, facts.SymbolMethod)
	var found bool
	for _, f := range ops {
		if strings.Contains(f.Name, "operator==") && f.Props["receiver"] == "V" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected V::operator== method fact, got %v", factNames(ff))
	}
}

func TestFieldVsMethod(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/fm.cpp": `
class C {
  int x;
  const int y = 3;
  void m();
};
`,
	})
	if sk, _ := mustFact(t, ff, "src.C::x").Props["symbol_kind"].(string); sk != facts.SymbolVariable {
		t.Errorf("C::x should be variable, got %v", sk)
	}
	if sk, _ := mustFact(t, ff, "src.C::y").Props["symbol_kind"].(string); sk != facts.SymbolConstant {
		t.Errorf("C::y (const) should be constant, got %v", sk)
	}
	if sk, _ := mustFact(t, ff, "src.C::m").Props["symbol_kind"].(string); sk != facts.SymbolMethod {
		t.Errorf("C::m should be method, got %v", sk)
	}
}

// --- Preprocessor guards ---

func TestPreprocGuardedClass(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/p.cpp": `
#ifdef FEATURE_X
class Guarded { int z; };
#endif

#if defined(HAVE_FOO)
namespace ns { void wrapped() {} }
#endif
`,
	})
	mustFact(t, ff, "src.Guarded")     // proves #ifdef recursion
	mustFact(t, ff, "src.ns::wrapped") // proves #if + namespace recursion
}

// --- Module facts ---

func TestModuleFacts(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"core/a.cpp": "void a(){}",
		"util/b.cpp": "void b(){}",
	})
	for _, dir := range []string{"core", "util"} {
		f, ok := findFact(ff, dir)
		if !ok || f.Kind != facts.KindModule {
			t.Errorf("expected module fact for %q", dir)
		}
	}
}

// --- Integration smoke tests against real repos (skipped when absent) ---

// collectCppFiles returns repo-relative paths of C++ files under the given
// subdirectories (or the whole repo when subdirs is empty).
func collectCppFiles(t *testing.T, repo string, subdirs ...string) []string {
	t.Helper()
	roots := subdirs
	if len(roots) == 0 {
		roots = []string{"."}
	}
	var rel []string
	for _, sub := range roots {
		root := filepath.Join(repo, sub)
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			name := d.Name()
			if isCFile(name) {
				return nil
			}
			if isUnambiguousCppExt(name) || strings.EqualFold(filepath.Ext(name), ".h") {
				if r, err := filepath.Rel(repo, path); err == nil {
					rel = append(rel, r)
				}
			}
			return nil
		})
	}
	return rel
}

func TestIntegrationGetdp(t *testing.T) {
	const repo = "/Users/dejan/development/cpp/getdp"
	if _, err := os.Stat(repo); err != nil {
		t.Skip("getdp test repo not present")
	}
	files := collectCppFiles(t, repo)
	ff, err := New().Extract(context.Background(), repo, files)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	classes := factsOfSymbolKind(ff, facts.SymbolClass)
	if len(classes) < 30 {
		t.Errorf("expected >= 30 classes in getdp, got %d", len(classes))
	}

	mm := mustFact(t, ff, "src/interface.MacroManager")
	if sk, _ := mm.Props["symbol_kind"].(string); sk != facts.SymbolClass {
		t.Errorf("MacroManager should be a class, got %v", sk)
	}
	inst := mustFact(t, ff, "src/interface.MacroManager::Instance")
	if inst.Props["receiver"] != "MacroManager" {
		t.Errorf("MacroManager::Instance receiver = %v, want MacroManager", inst.Props["receiver"])
	}
	if inst.Props["static"] != true {
		t.Errorf("MacroManager::Instance should be static, got %+v", inst.Props)
	}
	// MacroManagerStack is forward-declared in the header and defined in the .cpp;
	// dedup should yield exactly one class fact, not a phantom from the forward decl.
	if n := countByName(ff, "src/interface.MacroManagerStack"); n != 1 {
		t.Errorf("expected exactly 1 MacroManagerStack fact, got %d", n)
	}
}

func TestIntegrationGmsh(t *testing.T) {
	const repo = "/Users/dejan/development/cpp/gmsh"
	if _, err := os.Stat(repo); err != nil {
		t.Skip("gmsh test repo not present")
	}
	files := collectCppFiles(t, repo, "Geo", "Numeric", "Common")
	ff, err := New().Extract(context.Background(), repo, files)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	mustFact(t, ff, "Geo.GModel")

	// Out-of-line methods collapse to one fact each, located in the .cpp definition.
	for _, m := range []string{"Geo.GModel::setFileName", "Geo.GModel::current", "Geo.GModel::findByName"} {
		if n := countByName(ff, m); n != 1 {
			t.Errorf("expected exactly 1 %q, got %d", m, n)
		}
		f := mustFact(t, ff, m)
		if !strings.HasSuffix(f.File, "GModel.cpp") {
			t.Errorf("%s File = %q, want GModel.cpp definition", m, f.File)
		}
	}

	// Inheritance edges.
	if !hasRelation(mustFact(t, ff, "Geo.GEdge"), facts.RelImplements, "Geo.GEntity") {
		t.Errorf("GEdge should implement GEntity")
	}

	// Namespaced functions: single fact, defined in the .cpp.
	gp := mustFact(t, ff, "Numeric.ElementType::getParentType")
	if !strings.HasSuffix(gp.File, "ElementType.cpp") {
		t.Errorf("ElementType::getParentType File = %q, want ElementType.cpp", gp.File)
	}

	// Sanity: a non-trivial number of facts and no hang.
	if len(ff) < 2000 {
		t.Errorf("expected a large fact count from gmsh Geo/Numeric/Common, got %d", len(ff))
	}
}
