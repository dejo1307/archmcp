package cppextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// These tests exercise the pure-C path (tree-sitter-c grammar). They reuse the
// helpers in cpp_test.go (extractProject, mustFact, hasRelation, ...).

func TestCFunctionAndCallGraph(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/a.c": `
int helper(int x) { return x + 1; }
int compute(int x) { return helper(x) + helper(x); }
`,
	})
	h := mustFact(t, ff, "src.helper")
	if sk, _ := h.Props["symbol_kind"].(string); sk != facts.SymbolFunc {
		t.Errorf("helper symbol_kind = %v, want function", sk)
	}
	if h.Props["language"] != langC {
		t.Errorf("helper language = %v, want c", h.Props["language"])
	}
	c := mustFact(t, ff, "src.compute")
	if !hasRelation(c, facts.RelCalls, "src.helper") {
		t.Errorf("compute should call src.helper, got %+v", c.Relations)
	}
}

func TestCStructTypedef(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/t.c": `
struct point { int x; int y; };
typedef struct point point_t;
`,
	})
	p := mustFact(t, ff, "src.point")
	if sk, _ := p.Props["symbol_kind"].(string); sk != facts.SymbolStruct {
		t.Errorf("point should be struct, got %v", sk)
	}
	if p.Props["language"] != langC {
		t.Errorf("point language = %v, want c", p.Props["language"])
	}
	a := mustFact(t, ff, "src.point_t")
	if sk, _ := a.Props["symbol_kind"].(string); sk != facts.SymbolType {
		t.Errorf("point_t should be a type alias, got %v", sk)
	}
}

func TestCEnumNotScoped(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/e.c": `enum color { RED, GREEN, BLUE };`,
	})
	e := mustFact(t, ff, "src.color")
	if sk, _ := e.Props["symbol_kind"].(string); sk != facts.SymbolEnum {
		t.Errorf("color should be enum, got %v", sk)
	}
	if e.Props["scoped"] == true {
		t.Errorf("plain C enum must not be scoped, got %+v", e.Props)
	}
}

func TestCQuotedInclude(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"lib/bar.h": "int bar(void);",
		"lib/use.c": `
#include <stdio.h>
#include "bar.h"
int use(void) { return bar(); }
`,
	})
	var found bool
	for _, f := range ff {
		if f.Kind != facts.KindDependency {
			continue
		}
		inc, _ := f.Props["include"].(string)
		if inc == "stdio.h" {
			t.Errorf("system include <stdio.h> should not yield a dependency fact")
		}
		if inc == "bar.h" {
			found = true
			if f.Props["language"] != langC {
				t.Errorf("bar.h include language = %v, want c", f.Props["language"])
			}
			if f.Props["source"] != "internal" {
				t.Errorf("bar.h should be internal, got %v", f.Props["source"])
			}
			if !hasRelation(f, facts.RelImports, "lib") {
				t.Errorf("bar.h should import module 'lib', got %+v", f.Relations)
			}
		}
	}
	if !found {
		t.Errorf("expected a dependency fact for #include \"bar.h\"")
	}
}

// TestCDesignatedAndRangeInitializers proves the grammar choice matters: C
// designated and GCC range-designated initializers must not abort parsing of
// the enclosing function (tree-sitter-cpp would error on the range form).
func TestCDesignatedAndRangeInitializers(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/init.c": `
struct cfg { int a; int b; };
static const int table[8] = { [0 ... 3] = 1, [4 ... 7] = 2 };
int build(void) {
	struct cfg c = { .a = 1, .b = 2 };
	return c.a + table[0];
}
`,
	})
	// The enclosing function must still be extracted.
	mustFact(t, ff, "src.build")
}

// TestCKeywordIdentifiers is the decisive test: kernel C reuses C++ keywords as
// ordinary identifiers (new, try, class, delete, private, ...). tree-sitter-cpp
// errors on these; tree-sitter-c parses them cleanly. The symbols must appear.
func TestCKeywordIdentifiers(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/kw.c": `
int try;
struct s { int new; int class; int delete; };
void *private(void) { return 0; }
int namespace(int this_) { return this_; }
`,
	})
	mustFact(t, ff, "src.s")
	p := mustFact(t, ff, "src.private")
	if sk, _ := p.Props["symbol_kind"].(string); sk != facts.SymbolFunc {
		t.Errorf("private should be a function, got %v", sk)
	}
	mustFact(t, ff, "src.namespace")
}

// TestCFunctionPointerOpsTable is the kernel case: functions referenced only via
// a struct's function-pointer fields (`.read = foo`) must get an inbound edge
// from the ops-table variable, so they aren't reported as dead code.
func TestCFunctionPointerOpsTable(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/fops.c": `
static int proc_reg_read(void) { return 0; }
static int proc_reg_open(void) { return 0; }
static const struct file_operations proc_reg_file_ops = {
	.read = proc_reg_read,
	.open = proc_reg_open,
};
`,
	})
	ops := mustFact(t, ff, "src.proc_reg_file_ops")
	if sk, _ := ops.Props["symbol_kind"].(string); sk != facts.SymbolConstant {
		t.Errorf("ops table should be a constant (has const), got %v", sk)
	}
	if !hasRelation(ops, facts.RelCalls, "src.proc_reg_read") {
		t.Errorf("ops table should reference proc_reg_read, got %+v", ops.Relations)
	}
	if !hasRelation(ops, facts.RelCalls, "src.proc_reg_open") {
		t.Errorf("ops table should reference proc_reg_open, got %+v", ops.Relations)
	}
}

// TestCFunctionPointerAddressOf covers the `= &func` form for a function-pointer
// variable (whose declarator findFunctionDeclarator would otherwise mistake for a
// prototype).
func TestCFunctionPointerAddressOf(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/cb.c": `
static int handler(void) { return 0; }
static int (*g_cb)(void) = &handler;
`,
	})
	cb := mustFact(t, ff, "src.g_cb")
	if !hasRelation(cb, facts.RelCalls, "src.handler") {
		t.Errorf("g_cb should reference handler via &handler, got %+v", cb.Relations)
	}
}

// TestCFunctionPointerNested covers function refs inside a nested initializer_list.
func TestCFunctionPointerNested(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/cfg.c": `
static int a(void) { return 0; }
static int b(void) { return 0; }
static const struct outer cfg = { .ops = { .x = a, .y = b } };
`,
	})
	cfg := mustFact(t, ff, "src.cfg")
	if !hasRelation(cfg, facts.RelCalls, "src.a") || !hasRelation(cfg, facts.RelCalls, "src.b") {
		t.Errorf("cfg should reference both a and b, got %+v", cfg.Relations)
	}
}

// TestCCallbackArgReference is the second kernel case: a function passed as a call
// argument (callback registration) must get an inbound edge from the caller, so it
// is not reported as dead.
func TestCCallbackArgReference(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/reg.c": `
static int my_show(void) { return 0; }
static int my_init(void) { return register_thing("x", my_show); }
`,
	})
	init := mustFact(t, ff, "src.my_init")
	if !hasRelation(init, facts.RelCalls, "src.my_show") {
		t.Errorf("my_init should reference my_show passed as a callback arg, got %+v", init.Relations)
	}
}

// TestCCallbackArgAddressOf covers `&func` as a call argument.
func TestCCallbackArgAddressOf(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/irq.c": `
static int handler(void) { return 0; }
static int setup(void) { return request_irq(1, &handler, 0); }
`,
	})
	setup := mustFact(t, ff, "src.setup")
	if !hasRelation(setup, facts.RelCalls, "src.handler") {
		t.Errorf("setup should reference handler passed via &handler, got %+v", setup.Relations)
	}
}

// TestCArgRefNonFunctionDropped guards the funcNames filter: an argument identifier
// that does not name a real function must NOT produce a RelCalls edge (otherwise a
// data argument could spuriously mark a same-named function as used).
func TestCArgRefNonFunctionDropped(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/u.c": `
static int use(int v) { return v; }
static int caller(void) { int local = 3; return use(local); }
`,
	})
	caller := mustFact(t, ff, "src.caller")
	// The real call to use() is kept; the data argument `local` is not a function.
	if !hasRelation(caller, facts.RelCalls, "src.use") {
		t.Errorf("caller should keep the real call to use, got %+v", caller.Relations)
	}
	if hasRelation(caller, facts.RelCalls, "src.local") {
		t.Errorf("non-function argument `local` must not become a call edge, got %+v", caller.Relations)
	}
}

// TestCArgRefAllCapsSuppressed: an UPPER_SNAKE macro argument must not create an edge.
func TestCArgRefAllCapsSuppressed(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/m.c": `
#define FLAG 1
static int FLAG_fn(void) { return 0; }
static int caller(void) { return take(FLAG); }
`,
	})
	caller := mustFact(t, ff, "src.caller")
	if hasRelation(caller, facts.RelCalls, "src.FLAG") {
		t.Errorf("UPPER_SNAKE argument FLAG must not produce a call edge, got %+v", caller.Relations)
	}
}

// TestCInitializerNonFunctionDropped: a struct field initialized with a lowercase
// NON-function global must not produce a call edge (funcNames filter applies to
// initializer refs too).
func TestCInitializerNonFunctionDropped(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/d.c": `
static int real_fn(void) { return 0; }
static const struct d cfg = { .fn = real_fn, .data = some_global };
`,
	})
	cfg := mustFact(t, ff, "src.cfg")
	if !hasRelation(cfg, facts.RelCalls, "src.real_fn") {
		t.Errorf("cfg should reference real_fn, got %+v", cfg.Relations)
	}
	if hasRelation(cfg, facts.RelCalls, "src.some_global") {
		t.Errorf("non-function initializer value some_global must not become a call edge, got %+v", cfg.Relations)
	}
}

// TestCInitializerNoFalseEdges guards against over-emitting: macro constants
// (UPPER_SNAKE), strings, and numbers in initializers must not become edges.
func TestCInitializerNoFalseEdges(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/c.c": `
#define MAX 8
static const struct cfg c = { .count = MAX, .name = "hi", .n = 3 };
`,
	})
	cf := mustFact(t, ff, "src.c")
	if hasRelation(cf, facts.RelCalls, "src.MAX") {
		t.Errorf("UPPER_SNAKE macro constant MAX must not produce a call edge, got %+v", cf.Relations)
	}
	for _, r := range cf.Relations {
		if r.Kind == facts.RelCalls && (r.Target == "src.hi" || r.Target == "src.n" || r.Target == "src.count" || r.Target == "src.name") {
			t.Errorf("unexpected call edge to %q (designator/literal leaked)", r.Target)
		}
	}
}

// TestCStaticIsFilePrivate verifies C `static` functions are not exported.
func TestCStaticIsFilePrivate(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"src/s.c": `
static int hidden(void) { return 0; }
int visible(void) { return hidden(); }
`,
	})
	h := mustFact(t, ff, "src.hidden")
	if h.Props["exported"] != false {
		t.Errorf("static C function should have exported=false, got %v", h.Props["exported"])
	}
	if h.Props["static"] != true {
		t.Errorf("hidden should be marked static, got %+v", h.Props)
	}
	v := mustFact(t, ff, "src.visible")
	if v.Props["exported"] != true {
		t.Errorf("non-static C function should be exported=true, got %v", v.Props["exported"])
	}
}

// TestCSameDirHeaderSourceMerge confirms a C prototype (foo.h) and its
// definition (foo.c) in the same directory collapse to a single symbol.
func TestCSameDirHeaderSourceMerge(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"m/foo.h": "int foo(int x);",
		"m/foo.c": "int foo(int x) { return x; }",
	})
	if n := countByName(ff, "m.foo"); n != 1 {
		t.Fatalf("expected a single merged m.foo symbol, got %d", n)
	}
	f := mustFact(t, ff, "m.foo")
	if f.Props["has_body"] != true {
		t.Errorf("merged m.foo should take the definition's has_body=true, got %+v", f.Props)
	}
	if f.Props["language"] != langC {
		t.Errorf("m.foo language = %v, want c", f.Props["language"])
	}
}

// TestHeaderLangAttribution is the regression guard for the Linux-kernel failure
// mode: a stray C++ file in one subtree must NOT flip headers in a sibling C
// subtree to the C++ grammar. The C header here uses `class` as an identifier,
// which only parses under tree-sitter-c.
func TestHeaderLangAttribution(t *testing.T) {
	ff := extractProject(t, map[string]string{
		"tools/x.cpp":   "namespace n { class W {}; }",
		"kernel/y.c":    "int y(void) { return 0; }",
		"include/z.h":   "struct z { int class; int new; };",
		"include/zfn.h": "int zfn(void);",
	})
	// kernel/y.c is C.
	if f := mustFact(t, ff, "kernel.y"); f.Props["language"] != langC {
		t.Errorf("kernel/y.c language = %v, want c", f.Props["language"])
	}
	// include/z.h is routed to C (no C++ sibling in its subtree); the keyword-as-
	// identifier struct proves it: under the C++ grammar this would fail to parse.
	z := mustFact(t, ff, "include.z")
	if z.Props["language"] != langC {
		t.Errorf("include/z.h language = %v, want c (must not be flipped by tools/x.cpp)", z.Props["language"])
	}
	// tools/x.cpp stays C++.
	if f := mustFact(t, ff, "tools.n::W"); f.Props["language"] != langCpp {
		t.Errorf("tools/x.cpp class W language = %v, want cpp", f.Props["language"])
	}
}
