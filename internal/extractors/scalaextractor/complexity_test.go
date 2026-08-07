package scalaextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func propInt(t *testing.T, f facts.Fact, key string) int {
	t.Helper()
	v, ok := f.Props[key]
	if !ok {
		return 0
	}
	n, ok := v.(int)
	if !ok {
		t.Fatalf("%s: prop %q is %T, want int", f.Name, key, v)
	}
	return n
}

func propStrings(f facts.Fact, key string) []string {
	v, ok := f.Props[key]
	if !ok {
		return nil
	}
	s, _ := v.([]string)
	return s
}

func hasProp(f facts.Fact, key string) bool {
	_, ok := f.Props[key]
	return ok
}

// TestForComprehensionYieldDoesNotScale is the central Phase 2 test, and the reason
// the loop model exists.
//
// The two functions below are syntactically near-identical — both are `for`
// comprehensions with one generator — and mean completely different things. Counting
// the second as a loop would report a per-iteration database call on every effectful
// service in a Scala codebase, which the corpus measurement says is the majority of
// them (60.4% of `for … yield` sites sit in effect-importing files).
func TestForComprehensionYieldDoesNotScale(t *testing.T) {
	src := `package p

object S {
  def iterate(users: List[User]): Unit =
    for (u <- users) { load(u) }

  def bind(): Future[Int] =
    for (a <- fetchA(); b <- fetchB(a)) yield b
}
`
	ff := extractAST(t, "src/S.scala", src)

	it := findFact(t, ff, "src.S.iterate")
	if got := propInt(t, it, "scaling_loop_depth"); got != 1 {
		t.Errorf("for without yield: scaling_loop_depth = %d, want 1", got)
	}
	if got := propStrings(it, "calls_in_scaling_loop"); len(got) != 1 || got[0] != "load" {
		t.Errorf("for without yield: calls_in_scaling_loop = %v, want [load]", got)
	}

	bind := findFact(t, ff, "src.S.bind")
	// It IS repetition-shaped, so loop_depth records it and nesting stays visible…
	if got := propInt(t, bind, "loop_depth"); got != 1 {
		t.Errorf("for with yield: loop_depth = %d, want 1", got)
	}
	// …but it does not scale with input, so no N+1 candidate comes out of it.
	if got := propInt(t, bind, "scaling_loop_depth"); got != 0 {
		t.Errorf("for with yield: scaling_loop_depth = %d, want 0", got)
	}
	if got := propStrings(bind, "calls_in_scaling_loop"); len(got) != 0 {
		t.Errorf("for with yield: calls_in_scaling_loop = %v, want empty", got)
	}
	// The key must still be PRESENT, or the analyzer falls back to calls_in_loop
	// and the discount silently does not apply.
	if !hasProp(bind, "calls_in_scaling_loop") {
		t.Error("for with yield: calls_in_scaling_loop key absent — the analyzer would fall back to the raw list")
	}
	if !hasProp(bind, "scaling_loop_depth") {
		t.Error("for with yield: scaling_loop_depth key absent — the discount would not apply")
	}
}

// TestGeneratorEvaluatedOnce pins that a call in the generator position is not
// per-iteration work: `for (u <- loadUsers())` loads once, however many users
// come back.
func TestGeneratorEvaluatedOnce(t *testing.T) {
	src := `package p

object S {
  def run(): Unit = for (u <- loadUsers()) { process(u) }
}
`
	ff := extractAST(t, "src/S.scala", src)
	f := findFact(t, ff, "src.S.run")
	calls := propStrings(f, "calls_in_scaling_loop")
	for _, c := range calls {
		if c == "loadUsers" {
			t.Errorf("generator call counted as in-loop: %v", calls)
		}
	}
	found := false
	for _, c := range calls {
		if c == "process" {
			found = true
		}
	}
	if !found {
		t.Errorf("body call missing from calls_in_scaling_loop: %v", calls)
	}
}

// TestCombinatorLoopClassification pins the three-way split over method names,
// each case chosen because the corpus says it is common.
func TestCombinatorLoopClassification(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantScaling int
		wantLoop    int
	}{
		// Collection-dominant: 8.0% effect share in the corpus.
		{"foreach", `xs.foreach { x => load(x) }`, 1, 1},
		{"map", `xs.map { x => load(x) }`, 1, 1},
		{"filter", `xs.filter { x => check(x) }`, 1, 1},
		{"foldLeft", `xs.foldLeft(0) { (a, x) => load(x) }`, 1, 1},
		// Ambiguous: ~49% effect share. Repetition-shaped, but not scaling.
		{"flatMap", `xs.flatMap { x => load(x) }`, 0, 1},
		{"fold", `opt.fold(0) { x => load(x) }`, 0, 1},
		// Not repetition at all: a by-name block that runs at most once.
		{"synchronized", `lock.synchronized { load(x) }`, 0, 0},
		{"getOrElse", `opt.getOrElse { load(x) }`, 0, 0},
		{"resource_use", `res.use { x => load(x) }`, 0, 0},
		// A combinator name without a block is a method reference, not iteration.
		{"map_no_block", `xs.map(fn)`, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\n\nobject S {\n  def run(): Unit = " + tc.body + "\n}\n"
			ff := extractAST(t, "src/S.scala", src)
			f := findFact(t, ff, "src.S.run")
			if got := propInt(t, f, "scaling_loop_depth"); got != tc.wantScaling {
				t.Errorf("scaling_loop_depth = %d, want %d", got, tc.wantScaling)
			}
			if got := propInt(t, f, "loop_depth"); got != tc.wantLoop {
				t.Errorf("loop_depth = %d, want %d", got, tc.wantLoop)
			}
		})
	}
}

// TestNestedLoopDepth checks that nesting accumulates and that an ambiguous
// construct does not contribute an exponent it cannot justify.
func TestNestedLoopDepth(t *testing.T) {
	src := `package p

object S {
  def quadratic(xs: List[Int]): Unit =
    for (x <- xs) { for (y <- xs) { work(x, y) } }

  def discounted(xs: List[Int]): Unit =
    for (x <- xs) { xs.flatMap { y => work(x, y) } }
}
`
	ff := extractAST(t, "src/S.scala", src)

	q := findFact(t, ff, "src.S.quadratic")
	if got := propInt(t, q, "scaling_loop_depth"); got != 2 {
		t.Errorf("two real loops: scaling_loop_depth = %d, want 2", got)
	}

	// loop_depth 2 (both are repetition), scaling 1 (only the outer one is proven
	// to scale) — so the analyzer reports O(n), flagged as discounted, instead of
	// claiming O(n²) it cannot support.
	d := findFact(t, ff, "src.S.discounted")
	if got := propInt(t, d, "loop_depth"); got != 2 {
		t.Errorf("discounted: loop_depth = %d, want 2", got)
	}
	if got := propInt(t, d, "scaling_loop_depth"); got != 1 {
		t.Errorf("discounted: scaling_loop_depth = %d, want 1", got)
	}
}

// TestWhileAndDoWhileScale — 2,337 corpus sites, none in effect-importing files.
func TestWhileAndDoWhileScale(t *testing.T) {
	src := `package p

object S {
  def a(): Unit = { var i = 0; while (i < 10) { load(i); i += 1 } }
  def b(): Unit = { var i = 0; do { load(i); i += 1 } while (i < 10) }
}
`
	ff := extractAST(t, "src/S.scala", src)
	for _, name := range []string{"src.S.a", "src.S.b"} {
		f := findFact(t, ff, name)
		if got := propInt(t, f, "scaling_loop_depth"); got != 1 {
			t.Errorf("%s: scaling_loop_depth = %d, want 1", name, got)
		}
	}
}

// TestCyclomatic counts the branch points Scala actually has, including a match's
// arms and a comprehension's guard.
func TestCyclomatic(t *testing.T) {
	cases := []struct{ name, body, want string }{}
	_ = cases

	src := `package p

object S {
  def simple(): Int = 1

  def branchy(x: Int, a: Boolean, b: Boolean): Int = {
    if (a && b) return 1
    x match {
      case 1 => 2
      case n if n > 5 => 3
      case _ => 4
    }
  }
}
`
	ff := extractAST(t, "src/S.scala", src)

	if got := propInt(t, findFact(t, ff, "src.S.simple"), "cyclomatic"); got != 1 {
		t.Errorf("straight-line def: cyclomatic = %d, want 1", got)
	}
	// 1 base + if + && + 3 case arms + 1 guard = 7.
	if got := propInt(t, findFact(t, ff, "src.S.branchy"), "cyclomatic"); got != 7 {
		t.Errorf("branchy: cyclomatic = %d, want 7", got)
	}
}

// TestNestedDefIsolatesMetrics pins that an inner `def` neither inherits its
// parent's loop nesting nor leaks its own back out — otherwise a helper defined
// inside a loop would report the enclosing function's depth as its own.
func TestNestedDefIsolatesMetrics(t *testing.T) {
	src := `package p

object S {
  def outer(xs: List[Int]): Unit = {
    for (x <- xs) {
      def helper(y: Int): Int = y + 1
      use(helper(x))
    }
  }
}
`
	ff := extractAST(t, "src/S.scala", src)

	if got := propInt(t, findFact(t, ff, "src.S.outer"), "scaling_loop_depth"); got != 1 {
		t.Errorf("outer: scaling_loop_depth = %d, want 1", got)
	}
	h := findFact(t, ff, "src.S.helper")
	if got := propInt(t, h, "scaling_loop_depth"); got != 0 {
		t.Errorf("nested def inherited the enclosing loop depth: %d, want 0", got)
	}
	if hasProp(h, "loop_count") {
		t.Errorf("nested def reported loops it does not have: %v", h.Props)
	}
}

// TestLoopFreeBodyOmitsScalingProps keeps facts.jsonl honest on a large repository:
// with no loops the raw and scaling values agree at zero, so the analyzer's fallback
// is identical and the keys would be pure bloat.
func TestLoopFreeBodyOmitsScalingProps(t *testing.T) {
	ff := extractAST(t, "src/S.scala", "package p\n\nobject S {\n  def plain(): Int = compute()\n}\n")
	f := findFact(t, ff, "src.S.plain")
	for _, key := range []string{"loop_depth", "loop_count", "scaling_loop_depth", "calls_in_scaling_loop"} {
		if hasProp(f, key) {
			t.Errorf("loop-free body emitted %q: %v", key, f.Props)
		}
	}
}

// TestOptionReceiverDoesNotScale pins the one confirmed analyze_performance false
// positive on the corpus. A combinator's NAME cannot distinguish iteration from an
// Option chain — `xs.foreach` and `xs.find(p).foreach` differ only in what they are
// applied to — so the receiver is the evidence. A corpus servlet's
// `assetsMappings.find{…}.foreach{…}` was reported O(n²) at high severity, though
// neither combinator can run twice.
func TestOptionReceiverDoesNotScale(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantScaling int
		wantLoop    int
	}{
		// The false positive: both combinators apply to an Option.
		{"find_then_foreach", `xs.find(p).foreach { m => handle(m) }`, 0, 1},
		{"headOption_then_map", `xs.headOption.map { m => handle(m) }`, 0, 1},
		{"map_get_then_foreach", `cache.get(k).foreach { v => handle(v) }`, 0, 1},
		// The control: the same combinator on a real collection still scales.
		{"plain_foreach", `xs.foreach { m => handle(m) }`, 1, 1},
		// A collection-returning receiver does NOT demote the combinator. Depth is 1
		// rather than 2 because `filter(p)` passes a function value instead of a
		// block, and a combinator without a block is a method reference — the same
		// conservative rule `map_no_block` pins above.
		{"filter_then_foreach", `xs.filter(p).foreach { m => handle(m) }`, 1, 1},
		{"nested_collection", `xs.foreach { a => ys.foreach { b => handle(a, b) } }`, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\n\nobject S {\n  def run(): Unit = " + tc.body + "\n}\n"
			ff := extractAST(t, "src/S.scala", src)
			f := findFact(t, ff, "src.S.run")
			if got := propInt(t, f, "scaling_loop_depth"); got != tc.wantScaling {
				t.Errorf("scaling_loop_depth = %d, want %d", got, tc.wantScaling)
			}
			if got := propInt(t, f, "loop_depth"); got != tc.wantLoop {
				t.Errorf("loop_depth = %d, want %d", got, tc.wantLoop)
			}
		})
	}
}

// TestOptionChainCallIsNotAnN1Candidate is the consequence that matters: with the
// receiver demoted, a call inside the chain stops being offered as per-iteration I/O.
func TestOptionChainCallIsNotAnN1Candidate(t *testing.T) {
	src := `package p

object S {
  def lookup(k: String): Unit =
    cache.get(k).foreach { v => db.run(store(v)) }
}
`
	f := findFact(t, extractAST(t, "src/S.scala", src), "src.S.lookup")
	if got := propStrings(f, "calls_in_scaling_loop"); len(got) != 0 {
		t.Errorf("an Option chain offered an N+1 candidate: %v", got)
	}
	// The raw list still records it, so nesting stays visible in the evidence.
	if got := propStrings(f, "calls_in_loop"); len(got) == 0 {
		t.Error("the call should still be recorded in the raw in-loop list")
	}
}

// TestTraitAbstractnessIsMeasured pins the package-metrics correction. A Scala trait
// carries implementations, and the idiom leans on it: a mixin with a self-type and a
// concrete body is the ordinary way to compose a service. Counting every trait as an
// abstraction read one corpus package — sixteen controller traits whose bodies ARE
// the REST API — as A=1.00 and reported it "useless".
func TestTraitAbstractnessIsMeasured(t *testing.T) {
	src := `package p

/** A real abstraction: members declared, none implemented. */
trait Repo {
  def find(id: Long): Option[Row]
  val name: String
}

/** A mixin: every member has a body. This is an implementation, not an abstraction. */
trait Routes extends Base {
  get("/api/users") { listUsers() }
  def listUsers(): List[Row] = Nil
}

/** Partly abstract still counts as an abstraction — one unimplemented member is
  * enough to make it something another type must satisfy. */
trait Partial {
  def mustImplement(): Int
  def provided(): Int = 1
}

/** An abstract TYPE member with no right-hand side is abstract; an alias is not. */
trait WithAbstractType { type Out }
trait WithAlias { type Out = String }
`
	ff := extractAST(t, "src/S.scala", src)

	for name, want := range map[string]bool{
		"src.Repo":             true,
		"src.Routes":           false,
		"src.Partial":          true,
		"src.WithAbstractType": true,
		"src.WithAlias":        false,
	} {
		f := findFact(t, ff, name)
		if f.Props["symbol_kind"] != facts.SymbolInterface {
			t.Errorf("%s: symbol_kind = %v, want interface", name, f.Props["symbol_kind"])
		}
		// Declared explicitly either way — package metrics treats the prop as
		// authoritative, so its ABSENCE would silently fall back to "interfaces are
		// abstract" and the demotion would not happen.
		got, ok := f.Props["abstract"]
		if !ok {
			t.Errorf("%s: abstract prop absent; the metric would assume true", name)
			continue
		}
		if got != want {
			t.Errorf("%s: abstract = %v, want %v", name, got, want)
		}
	}
}

// TestCaseClassIsADataHolder pins the second half: package metrics reads the marker
// to stop advising "extract interfaces" on a package that is mostly value carriers.
func TestCaseClassIsADataHolder(t *testing.T) {
	src := `package p

final case class Age(age: Long)
class Service(dep: Dep)
case object Marker
`
	ff := extractAST(t, "src/S.scala", src)

	if got := findFact(t, ff, "src.Age").Props["data_holder"]; got != true {
		t.Errorf("case class: data_holder = %v, want true", got)
	}
	if hasProp(findFact(t, ff, "src.Service"), "data_holder") {
		t.Error("a plain class must not be marked a data holder")
	}
	// A `case object` is a singleton, not a carrier of fields.
	if hasProp(findFact(t, ff, "src.Marker"), "data_holder") {
		t.Error("a case object must not be marked a data holder")
	}
}
