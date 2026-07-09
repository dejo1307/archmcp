package rustextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// extractAST runs the tree-sitter walker over src as if it were "pkg/lib.rs"
// belonging to a single crate rooted at "pkg" (a single-crate repo has no
// Cargo.toml prefix directory, so crateDir == dir).
func extractAST(t *testing.T, src string) []facts.Fact {
	t.Helper()
	ff, _ := extractFileAST([]byte(src), "pkg/lib.rs", []crateInfo{{name: "pkg", dir: "pkg"}}, map[string]bool{"pkg": true})
	return ff
}

func findFact(ff []facts.Fact, name string) (facts.Fact, bool) {
	for _, f := range ff {
		if f.Name == name {
			return f, true
		}
	}
	return facts.Fact{}, false
}

func findFactsByKind(ff []facts.Fact, kind string) []facts.Fact {
	var out []facts.Fact
	for _, f := range ff {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

func hasRelation(f facts.Fact, relKind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == relKind && r.Target == target {
			return true
		}
	}
	return false
}

func TestAST_Struct(t *testing.T) {
	ff := extractAST(t, `pub struct User { name: String }`)
	f, ok := findFact(ff, "pkg.User")
	if !ok {
		t.Fatal("expected fact for pkg.User")
	}
	if f.Props["symbol_kind"] != facts.SymbolStruct {
		t.Errorf("symbol_kind = %v, want struct", f.Props["symbol_kind"])
	}
	if f.Props["exported"] != true {
		t.Errorf("exported = %v, want true (pub)", f.Props["exported"])
	}
}

func TestAST_StructNotExported(t *testing.T) {
	ff := extractAST(t, `struct Internal;`)
	f, ok := findFact(ff, "pkg.Internal")
	if !ok {
		t.Fatal("expected fact for pkg.Internal")
	}
	if f.Props["exported"] != false {
		t.Errorf("exported = %v, want false (no pub)", f.Props["exported"])
	}
}

func TestAST_Enum(t *testing.T) {
	ff := extractAST(t, `pub enum Direction { North, South }`)
	f, ok := findFact(ff, "pkg.Direction")
	if !ok {
		t.Fatal("expected fact for pkg.Direction")
	}
	if f.Props["symbol_kind"] != facts.SymbolEnum {
		t.Errorf("symbol_kind = %v, want enum", f.Props["symbol_kind"])
	}
}

func TestAST_Trait(t *testing.T) {
	ff := extractAST(t, `
pub trait Greeter {
    fn greet(&self) -> String;
    fn shout(&self) -> String {
        self.greet()
    }
}
`)
	f, ok := findFact(ff, "pkg.Greeter")
	if !ok {
		t.Fatal("expected fact for pkg.Greeter")
	}
	if f.Props["symbol_kind"] != facts.SymbolInterface {
		t.Errorf("symbol_kind = %v, want interface", f.Props["symbol_kind"])
	}
	// The signature-only method still gets a symbol fact.
	sig, ok := findFact(ff, "pkg.Greeter.greet")
	if !ok {
		t.Fatal("expected fact for pkg.Greeter.greet (signature)")
	}
	if sig.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("trait method symbol_kind = %v, want method", sig.Props["symbol_kind"])
	}
	// The default method's self.greet() call resolves to its trait sibling.
	shout, ok := findFact(ff, "pkg.Greeter.shout")
	if !ok {
		t.Fatal("expected fact for pkg.Greeter.shout")
	}
	if !hasRelation(shout, facts.RelCalls, "pkg.Greeter.greet") {
		t.Errorf("expected RelCalls -> pkg.Greeter.greet for self.greet(), got %+v", shout.Relations)
	}
}

func TestAST_TypeAliasAndConstStatic(t *testing.T) {
	ff := extractAST(t, `
pub type UserId = u64;
pub const MAX_USERS: u32 = 100;
static COUNTER: u32 = 0;
`)
	ta, ok := findFact(ff, "pkg.UserId")
	if !ok || ta.Props["symbol_kind"] != facts.SymbolType {
		t.Errorf("expected pkg.UserId symbol_kind=type, got %+v ok=%v", ta.Props, ok)
	}
	c, ok := findFact(ff, "pkg.MAX_USERS")
	if !ok || c.Props["symbol_kind"] != facts.SymbolConstant || c.Props["exported"] != true {
		t.Errorf("expected pkg.MAX_USERS symbol_kind=constant exported=true, got %+v ok=%v", c.Props, ok)
	}
	s, ok := findFact(ff, "pkg.COUNTER")
	if !ok || s.Props["symbol_kind"] != facts.SymbolVariable || s.Props["exported"] != false {
		t.Errorf("expected pkg.COUNTER symbol_kind=variable exported=false, got %+v ok=%v", s.Props, ok)
	}
}

func TestAST_ImplInherentMethods(t *testing.T) {
	ff := extractAST(t, `
pub struct Counter { n: u32 }
impl Counter {
    pub fn new() -> Self {
        Counter { n: 0 }
    }
    pub fn increment(&mut self) {
        self.n += 1;
    }
}
`)
	newFn, ok := findFact(ff, "pkg.Counter.new")
	if !ok {
		t.Fatal("expected fact for pkg.Counter.new")
	}
	if newFn.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("symbol_kind = %v, want method", newFn.Props["symbol_kind"])
	}
	if newFn.Props["static"] != true {
		t.Errorf("static = %v, want true (no self param)", newFn.Props["static"])
	}
	incr, ok := findFact(ff, "pkg.Counter.increment")
	if !ok {
		t.Fatal("expected fact for pkg.Counter.increment")
	}
	if incr.Props["static"] == true {
		t.Errorf("increment has &mut self, should not be static")
	}
	if incr.Props["receiver"] != "Counter" {
		t.Errorf("receiver = %v, want Counter", incr.Props["receiver"])
	}
}

func TestAST_ImplTraitForType_EmitsImplements(t *testing.T) {
	ff := extractAST(t, `
pub struct Wrapper { n: u32 }
impl std::fmt::Display for Wrapper {
    fn fmt(&self) {}
}
`)
	w, ok := findFact(ff, "pkg.Wrapper")
	if !ok {
		t.Fatal("expected fact for pkg.Wrapper")
	}
	// The type/impl are on the same fact only after applyImplements runs (a
	// rust.go-level post-pass); extractFileAST alone returns the observation
	// via the second return value, not yet attached.
	if hasRelation(w, facts.RelImplements, "Display") {
		t.Error("extractFileAST alone must not attach implements; that's applyImplements' job")
	}
	_, impls := extractFileAST([]byte(`
pub struct Wrapper { n: u32 }
impl std::fmt::Display for Wrapper {
    fn fmt(&self) {}
}
`), "pkg/lib.rs", []crateInfo{{name: "pkg", dir: "pkg"}}, map[string]bool{"pkg": true})
	if len(impls) != 1 || impls[0].typeName != "pkg.Wrapper" || impls[0].traitName != "Display" {
		t.Errorf("impls = %+v, want [{pkg.Wrapper Display}]", impls)
	}
}

func TestApplyImplements_AttachesAcrossFiles(t *testing.T) {
	crates := []crateInfo{{name: "pkg", dir: "pkg"}}
	dirs := map[string]bool{"pkg": true}
	typeFacts, _ := extractFileAST([]byte(`pub struct Wrapper;`), "pkg/types.rs", crates, dirs)
	_, impls := extractFileAST([]byte(`
impl std::fmt::Display for Wrapper {
    fn fmt(&self) {}
}
`), "pkg/display.rs", crates, dirs)

	all := append([]facts.Fact{}, typeFacts...)
	applyImplements(all, impls)

	w, ok := findFact(all, "pkg.Wrapper")
	if !ok {
		t.Fatal("expected fact for pkg.Wrapper")
	}
	if !hasRelation(w, facts.RelImplements, "Display") {
		t.Errorf("expected RelImplements -> Display attached from a different file's impl block, got %+v", w.Relations)
	}
	// Exactly one Wrapper symbol fact — no duplicate created for the impl.
	if got := len(findFactsByKind(all, facts.KindSymbol)); got != 1 {
		t.Errorf("expected exactly 1 symbol fact (no duplicate from the impl block), got %d", got)
	}
}

func TestAST_BareCallSameDir(t *testing.T) {
	ff := extractAST(t, `
fn caller() {
    helper();
}
fn helper() {}
`)
	c, ok := findFact(ff, "pkg.caller")
	if !ok {
		t.Fatal("expected fact for pkg.caller")
	}
	if !hasRelation(c, facts.RelCalls, "pkg.helper") {
		t.Errorf("expected RelCalls -> pkg.helper, got %+v", c.Relations)
	}
}

func TestAST_CapitalizedBareCall_Instantiates(t *testing.T) {
	ff := extractAST(t, `
pub struct Foo { n: u32 }
fn make() -> Foo {
    Foo::new()
}
`)
	// Foo::new() is a scoped_identifier call whose leading path ("Foo") is not
	// the enclosing type, so it falls to calleeOther; the trailing name "new"
	// is lowercase, so this exercises the plain bare-constructor form instead.
	ff2 := extractAST(t, `
pub struct Bar;
fn make() {
    let b = Bar();
}
`)
	m, ok := findFact(ff2, "pkg.make")
	if !ok {
		t.Fatal("expected fact for pkg.make")
	}
	if !hasRelation(m, facts.RelInstantiates, "Bar") {
		t.Errorf("expected RelInstantiates -> Bar, got %+v", m.Relations)
	}
	_ = ff
}

func TestAST_SelfMethodCall(t *testing.T) {
	ff := extractAST(t, `
pub struct Service;
impl Service {
    fn a(&self) {
        self.b();
    }
    fn b(&self) {}
}
`)
	a, ok := findFact(ff, "pkg.Service.a")
	if !ok {
		t.Fatal("expected fact for pkg.Service.a")
	}
	if !hasRelation(a, facts.RelCalls, "pkg.Service.b") {
		t.Errorf("expected RelCalls -> pkg.Service.b for self.b(), got %+v", a.Relations)
	}
}

func TestAST_SelfStaticCall(t *testing.T) {
	ff := extractAST(t, `
pub struct Service;
impl Service {
    fn a() {
        Self::b();
    }
    fn b() {}
}
`)
	a, ok := findFact(ff, "pkg.Service.a")
	if !ok {
		t.Fatal("expected fact for pkg.Service.a")
	}
	if !hasRelation(a, facts.RelCalls, "pkg.Service.b") {
		t.Errorf("expected RelCalls -> pkg.Service.b for Self::b(), got %+v", a.Relations)
	}
}

func TestAST_MethodCallOnOtherReceiver_ShortNameEdge(t *testing.T) {
	ff := extractAST(t, `
fn run(repo: Repo) {
    repo.save();
}
`)
	r, ok := findFact(ff, "pkg.run")
	if !ok {
		t.Fatal("expected fact for pkg.run")
	}
	if !hasRelation(r, facts.RelCalls, "save") {
		t.Errorf("expected short-name RelCalls -> save for repo.save(), got %+v", r.Relations)
	}
}

func TestAST_UnrelatedTypeMethodCall_NoFalseSelfEdge(t *testing.T) {
	// A sibling-name collision across two different impl blocks (both declare
	// "run") must not resolve Other::run() to the wrong type's method.
	ff := extractAST(t, `
struct A;
impl A {
    fn run(&self) {}
}
struct B;
impl B {
    fn call(&self) {
        A::run();
    }
    fn run(&self) {}
}
`)
	c, ok := findFact(ff, "pkg.B.call")
	if !ok {
		t.Fatal("expected fact for pkg.B.call")
	}
	if hasRelation(c, facts.RelCalls, "pkg.B.run") {
		t.Errorf("A::run() must not resolve to pkg.B.run, got %+v", c.Relations)
	}
	if hasRelation(c, facts.RelCalls, "pkg.A.run") {
		t.Errorf("A::run() must not be resolved without type inference, got %+v", c.Relations)
	}
	// Best-effort short-name fallback only.
	if !hasRelation(c, facts.RelCalls, "run") {
		t.Errorf("expected short-name RelCalls -> run, got %+v", c.Relations)
	}
}

func TestAST_NestedModQualifiesSymbolName(t *testing.T) {
	ff := extractAST(t, `
mod inner {
    pub struct Nested;
}
`)
	if _, ok := findFact(ff, "pkg.inner.Nested"); !ok {
		t.Errorf("expected fact for pkg.inner.Nested, got %+v", ff)
	}
}

func TestAST_ModDeclarationNoBody_NoFact(t *testing.T) {
	// `mod foo;` declares another file; it must not itself produce a fact.
	ff := extractAST(t, `mod foo;`)
	if len(ff) != 0 {
		t.Errorf("expected no facts for a bodyless mod declaration, got %+v", ff)
	}
}

func TestAST_UseDependencyFacts(t *testing.T) {
	ff := extractAST(t, `
use std::collections::HashMap;
use serde::{Deserialize, Serialize};
use self::helper::run;
use super::shared::Config;
`)
	deps := findFactsByKind(ff, facts.KindDependency)
	if len(deps) != 5 {
		t.Fatalf("expected 5 dependency facts (serde::{Deserialize,Serialize} expands to two), got %d: %+v", len(deps), deps)
	}

	check := func(raw, wantSource string) {
		t.Helper()
		for _, d := range deps {
			if d.Name == "pkg -> "+raw {
				if d.Props["source"] != wantSource {
					t.Errorf("%s: source = %v, want %s", raw, d.Props["source"], wantSource)
				}
				return
			}
		}
		t.Errorf("no dependency fact found for %q", raw)
	}
	check("std::collections::HashMap", "stdlib")
	check("serde::Deserialize", "external")
	check("serde::Serialize", "external")
	check("self::helper::run", "internal")
	check("super::shared::Config", "internal")
}

func TestAST_UseSelfResolvesToKnownSubdirectory(t *testing.T) {
	ff, _ := extractFileAST([]byte(`use self::helper::run;`), "pkg/lib.rs",
		[]crateInfo{{name: "pkg", dir: "pkg"}},
		map[string]bool{"pkg": true, "pkg/helper": true})
	deps := findFactsByKind(ff, facts.KindDependency)
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependency fact, got %d", len(deps))
	}
	if !hasRelation(deps[0], facts.RelImports, "pkg/helper") {
		t.Errorf("expected RelImports -> pkg/helper (known submodule dir), got %+v", deps[0].Relations)
	}
}

func TestAST_ExternCrate(t *testing.T) {
	ff := extractAST(t, `extern crate serde;`)
	deps := findFactsByKind(ff, facts.KindDependency)
	if len(deps) != 1 || deps[0].Props["source"] != "external" {
		t.Errorf("expected 1 external dependency fact for extern crate serde, got %+v", deps)
	}
}
