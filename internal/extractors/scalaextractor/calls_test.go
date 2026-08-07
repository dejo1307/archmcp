package scalaextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func callTargets(f facts.Fact) []string {
	var out []string
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls {
			out = append(out, r.Target)
		}
	}
	return out
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestBareCallQualifiesOnlyToRealMembers is the conservatism test for call
// resolution, and it pins a bug that was live until the member set was collected.
//
// Both calls below are bare. One names a sibling the object declares; the other
// names something imported, inherited, or implicitly available — which is the
// common case in Scala. Qualifying both would invent `src.S.imported` and, because
// the graph is name-keyed, materialize a phantom node that dead-code and impact
// analysis would then reason about.
func TestBareCallQualifiesOnlyToRealMembers(t *testing.T) {
	src := `package p

object S {
  def entry(): Unit = {
    sibling()
    imported()
  }
  def sibling(): Unit = ()
}
`
	ff := extractAST(t, "src/S.scala", src)
	got := callTargets(findFact(t, ff, "src.S.entry"))

	if !containsStr(got, "src.S.sibling") {
		t.Errorf("a declared sibling should qualify to the enclosing type; got %v", got)
	}
	if !containsStr(got, "imported") {
		t.Errorf("an undeclared name should stay bare; got %v", got)
	}
	if containsStr(got, "src.S.imported") {
		t.Errorf("fabricated a member the type never declares; got %v", got)
	}
}

// TestForwardReferenceResolves guards why the member set is collected up front
// rather than as the walk proceeds: Scala bodies routinely call a method declared
// further down the same block.
func TestForwardReferenceResolves(t *testing.T) {
	src := `package p

object S {
  def first(): Unit = later()
  def later(): Unit = ()
}
`
	ff := extractAST(t, "src/S.scala", src)
	if got := callTargets(findFact(t, ff, "src.S.first")); !containsStr(got, "src.S.later") {
		t.Errorf("forward reference did not resolve; got %v", got)
	}
}

// TestReceiverQualifiedCallBindsToDeclaringType pins the object/companion case:
// `Registry.next()` resolves through the merge pass to the type that declares it.
func TestReceiverQualifiedCallBindsToDeclaringType(t *testing.T) {
	ff := runPasses(t, map[string]string{
		"core/Registry.scala": `package com.example.core

object Registry {
  def next(): Long = 1L
}
`,
		"app/Service.scala": `package com.example.app

import com.example.core.Registry

class Service {
  def run(): Long = Registry.next()
}
`,
	}, nil)

	got := callTargets(findFact(t, ff, "app.Service.run"))
	if !containsStr(got, "core.Registry.next") {
		t.Errorf("object call should bind to the declaring type; got %v", got)
	}
}

// TestDeclaredReceiverTypeResolves covers the shape essentially every Scala service
// is built on: a dependency arrives as a constructor parameter with its type written
// down. Scala states that type in the source, so resolving it needs no inference —
// and it is what lets the I/O closure cross the injection boundary instead of
// stopping at the first dependency (see TestIOClosureCrossesInjectedDependency).
func TestDeclaredReceiverTypeResolves(t *testing.T) {
	src := `package p

class S(repo: Repo) {
  def viaField(): Unit = repo.findAll()
  def viaParam(x: Thing): Unit = x.transform()
}
`
	ff := extractAST(t, "src/S.scala", src)

	if got := callTargets(findFact(t, ff, "src.S.viaField")); !containsStr(got, "Repo.findAll") {
		t.Errorf("constructor parameter type not used; got %v", got)
	}
	if got := callTargets(findFact(t, ff, "src.S.viaParam")); !containsStr(got, "Thing.transform") {
		t.Errorf("method parameter type not used; got %v", got)
	}
}

// TestUntypedReceiverStaysShort pins the deliberate limit that remains. When the
// source does NOT write the type down — an inferred local, a chained expression —
// there is nothing to resolve without a compiler, and Scala's implicit conversions
// and extension methods make guessing actively wrong. The edge stays a short name:
// enough for dead-code matching to see the method used, no more.
func TestUntypedReceiverStaysShort(t *testing.T) {
	src := `package p

class S {
  def run(xs: List[Thing]): Unit = {
    val inferred = build()
    inferred.mutate()
    xs.head.transform()
  }
}
`
	ff := extractAST(t, "src/S.scala", src)
	got := callTargets(findFact(t, ff, "src.S.run"))
	for _, want := range []string{"mutate", "transform"} {
		if !containsStr(got, want) {
			t.Errorf("expected short-name call %q; got %v", want, got)
		}
	}
	for _, bad := range []string{"inferred.mutate", "head.transform"} {
		if containsStr(got, bad) {
			t.Errorf("invented a type for an untyped receiver: %q in %v", bad, got)
		}
	}
}

// TestIOClosureCrossesInjectedDependency is the payoff for typed receivers, and the
// case that decides whether the N+1 signal works on realistic Scala at all: the loop
// calls a service method, which calls an injected repository, which reaches the
// database. Every hop but the last looks innocent.
func TestIOClosureCrossesInjectedDependency(t *testing.T) {
	ff := runPasses(t, map[string]string{
		"app/Repo.scala": `package com.example.app

class Repo {
  def find(id: Long): Row = db.run(byId(id))
  def byId(id: Long): Long = id
}
`,
		"app/Service.scala": `package com.example.app

class Service(repo: Repo) {
  def one(id: Long): Row = repo.find(id)
  def many(ids: List[Long]): Unit = for (id <- ids) { one(id) }
}
`,
	}, nil)

	if findFact(t, ff, "app.Service.one").Props["performs_io"] != true {
		t.Error("performs_io did not cross the injected dependency")
	}
	many := findFact(t, ff, "app.Service.many")
	if got := propStrings(many, "calls_in_scaling_loop"); len(got) != 1 || got[0] != "app.Service.one" {
		t.Errorf("in-loop callee not recorded canonically: %v", got)
	}
	// The analyzer joins those two: a scaling loop whose callee performs I/O.
	if many.Props["performs_io"] != true {
		t.Error("performs_io did not reach the looping caller")
	}
}

// TestStructuralNamesAreNotRecordedAsCallees keeps the analyzer's evidence readable:
// `foreach` and `synchronized` are the loop, not work done inside it, and recording
// them buries the real per-iteration callee under language machinery.
func TestStructuralNamesAreNotRecordedAsCallees(t *testing.T) {
	src := `package p

object S {
  def run(xs: List[Int]): Unit =
    xs.foreach { a => xs.foreach { b => lock.synchronized { combine(a, b) } } }
  def combine(a: Int, b: Int): Unit = ()
}
`
	ff := extractAST(t, "src/S.scala", src)
	got := propStrings(findFact(t, ff, "src.S.run"), "calls_in_scaling_loop")
	for _, bad := range []string{"foreach", "synchronized"} {
		if containsStr(got, bad) {
			t.Errorf("structural construct %q recorded as an in-loop callee: %v", bad, got)
		}
	}
	if !containsStr(got, "src.S.combine") {
		t.Errorf("real per-iteration callee missing: %v", got)
	}
}

// TestApplyFormIsInstantiation covers Scala's dominant construction syntax. Without
// it, `new` alone under-reports instantiation badly — in Scala 3, `new` is rare.
func TestApplyFormIsInstantiation(t *testing.T) {
	ff := runPasses(t, map[string]string{
		"core/User.scala": `package com.example.core

case class User(id: Long)
`,
		"app/Service.scala": `package com.example.app

import com.example.core.User

class Service {
  def make(): User = User(1L)
}
`,
	}, nil)

	f := findFact(t, ff, "app.Service.make")
	if !hasRelation(f, facts.RelInstantiates, "core.User") {
		t.Errorf("apply-form construction should instantiate the case class; got %+v", f.Relations)
	}
}

// TestEffectConstructorIsNotInstantiation guards the apply-form rule against the
// idiom it would otherwise misread: `Future { … }` and `IO { … }` are capitalized
// applications too, and treating them as construction would attach an instantiates
// edge to a type the repository does not declare, on almost every effectful method.
func TestEffectConstructorIsNotInstantiation(t *testing.T) {
	src := `package p

object S {
  def run() = Future { work() }
}
`
	ff := extractAST(t, "src/S.scala", src)
	f := findFact(t, ff, "src.S.run")
	for _, r := range f.Relations {
		if r.Kind == facts.RelInstantiates && r.Target == "Future" {
			t.Errorf("effect constructor read as instantiation: %+v", f.Relations)
		}
	}
}

// TestRecursiveSelf pins direct recursion detection, which the analyzer reports
// separately from nesting.
func TestRecursiveSelf(t *testing.T) {
	src := `package p

object S {
  def walk(n: Int): Int = if (n <= 0) 0 else walk(n - 1)
  def flat(n: Int): Int = n
}
`
	ff := extractAST(t, "src/S.scala", src)
	if findFact(t, ff, "src.S.walk").Props["recursive_self"] != true {
		t.Error("direct recursion not detected")
	}
	if hasProp(findFact(t, ff, "src.S.flat"), "recursive_self") {
		t.Error("non-recursive function marked recursive")
	}
}

// TestIODirectAndTransitiveClosure pins the signal that turns an in-loop wrapper
// call into a detectable N+1: the loop's callee looks innocent, and only the
// closure over the call graph says it eventually reaches the database.
func TestIODirectAndTransitiveClosure(t *testing.T) {
	ff := runPasses(t, map[string]string{
		"app/Repo.scala": `package com.example.app

class Repo {
  def load(id: Long): User = db.run(query(id))
  def query(id: Long): Query = Query(id)
}
`,
		"app/Service.scala": `package com.example.app

class Service(repo: Repo) {
  def wrapper(id: Long): User = fetch(id)
  def fetch(id: Long): User = loadOne(id)
  def loadOne(id: Long): User = Repo.load(id)
  def pure(x: Int): Int = x + 1
}
`,
	}, nil)

	// The direct sink: `db.run(...)` is Slick's entire surface.
	if findFact(t, ff, "app.Repo.load").Props["io_direct"] != true {
		t.Error("db.run not recognised as direct I/O")
	}
	// …and it propagates back up two wrapper hops.
	for _, name := range []string{"app.Service.loadOne", "app.Service.fetch", "app.Service.wrapper"} {
		if findFact(t, ff, name).Props["performs_io"] != true {
			t.Errorf("%s: performs_io not propagated through the call graph", name)
		}
	}
	// A function that reaches no sink must stay clean, or the signal means nothing.
	if hasProp(findFact(t, ff, "app.Service.pure"), "performs_io") {
		t.Error("performs_io leaked onto a function that does no I/O")
	}
}

// TestGenericIONamesNeedAnIOReceiver pins the precision gate. `run`, `execute` and
// `update` name hundreds of in-memory operations in a large codebase; admitting them
// unqualified would tag most of the repository as doing I/O, which carries the same
// information as tagging none of it.
func TestGenericIONamesNeedAnIOReceiver(t *testing.T) {
	src := `package p

object S {
  def viaDb(): Unit = db.run(q)
  def inMemory(): Unit = pipeline.run(q)
  def alsoInMemory(): Unit = counter.update(1)
}
`
	ff := extractAST(t, "src/S.scala", src)
	if findFact(t, ff, "src.S.viaDb").Props["io_direct"] != true {
		t.Error("db.run should be I/O")
	}
	if hasProp(findFact(t, ff, "src.S.inMemory"), "io_direct") {
		t.Error("pipeline.run must not be read as I/O — `run` alone is far too common")
	}
	if hasProp(findFact(t, ff, "src.S.alsoInMemory"), "io_direct") {
		t.Error("counter.update must not be read as I/O")
	}
}
