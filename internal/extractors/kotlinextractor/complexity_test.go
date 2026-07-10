package kotlinextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func kIntProp(t *testing.T, f facts.Fact, key string) int {
	t.Helper()
	v, ok := f.Props[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	t.Fatalf("prop %q is not numeric: %T", key, v)
	return 0
}

func kStrSlice(f facts.Fact, key string) []string {
	v, ok := f.Props[key]
	if !ok {
		return nil
	}
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, a := range s {
			if str, ok := a.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func kContains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}

func TestKtComplexity_ForLoop(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  for (x in items) { use(x) }\n}", false)
	f, ok := findFact(ff, "pkg.r")
	if !ok {
		t.Fatalf("missing pkg.r; got %v", ff)
	}
	if got := kIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := kIntProp(t, f, "loop_count"); got != 1 {
		t.Errorf("loop_count = %d, want 1", got)
	}
	if cil := kStrSlice(f, "calls_in_loop"); !kContains(cil, "pkg.use") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.use", cil)
	}
}

func TestKtComplexity_ForEachLambdaIsLoop(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  items.forEach { g(it) }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if got := kIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1 (forEach lambda is a loop)", got)
	}
	if cil := kStrSlice(f, "calls_in_loop"); !kContains(cil, "pkg.g") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.g", cil)
	}
}

func TestKtComplexity_NestedLambdaIterators(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  outer.forEach { o -> o.items.map { inner(it) } }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if got := kIntProp(t, f, "loop_depth"); got != 2 {
		t.Errorf("loop_depth = %d, want 2 (nested forEach/map)", got)
	}
	if cil := kStrSlice(f, "calls_in_loop"); !kContains(cil, "pkg.inner") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.inner", cil)
	}
}

func TestKtComplexity_IteratorReceiverEvaluatedOnce(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  service.load().forEach { use(it) }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	cil := kStrSlice(f, "calls_in_loop")
	if !kContains(cil, "pkg.use") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.use", cil)
	}
	if kContains(cil, "service.load") {
		t.Errorf("calls_in_loop = %v, must NOT contain service.load (receiver runs once)", cil)
	}
}

func TestKtComplexity_NonIteratorLambdaNotLoop(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  runBlocking { persist() }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("runBlocking { } must not be a loop; loop_depth=%v", f.Props["loop_depth"])
	}
	if cil := kStrSlice(f, "calls_in_loop"); kContains(cil, "pkg.persist") {
		t.Errorf("calls_in_loop = %v, must NOT contain pkg.persist (lambda runs once)", cil)
	}
}

func TestKtComplexity_InLoopMethodCallCaptured(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  items.forEach { dao.insert(it) }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if cil := kStrSlice(f, "calls_in_loop"); !kContains(cil, "dao.insert") {
		t.Errorf("calls_in_loop = %v, want to contain dao.insert", cil)
	}
}

func TestKtComplexity_NestedDeferredLambdaNotInLoop(t *testing.T) {
	// A lambda defined inside an iterator lambda is deferred (runs when invoked,
	// not per element). `handle` must NOT be in calls_in_loop, while `use` (called
	// directly per element) is.
	ff := extractAST(t, "fun r(items: List<Int>) {\n  items.forEach { x ->\n    use(x)\n    register { handle(x) }\n  }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	cil := kStrSlice(f, "calls_in_loop")
	if !kContains(cil, "pkg.use") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.use (per element)", cil)
	}
	if kContains(cil, "pkg.handle") {
		t.Errorf("calls_in_loop = %v, must NOT contain pkg.handle (deferred lambda)", cil)
	}
}

func TestKtComplexity_RecursiveSelf(t *testing.T) {
	ff := extractAST(t, "fun fib(n: Int): Int {\n  if (n < 2) return n\n  return fib(n - 1) + fib(n - 2)\n}", false)
	f, _ := findFact(ff, "pkg.fib")
	v, ok := f.Props["recursive_self"].(bool)
	if !ok || !v {
		t.Errorf("recursive_self = %v (ok=%v), want true", f.Props["recursive_self"], ok)
	}
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("loop_depth should be omitted for a loop-free function, got %v", f.Props["loop_depth"])
	}
}

func TestKtComplexity_WhileLoop(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  while (ready()) { step() }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if got := kIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if cil := kStrSlice(f, "calls_in_loop"); !kContains(cil, "pkg.step") {
		t.Errorf("calls_in_loop = %v, want to contain pkg.step", cil)
	}
}

func TestKtComplexity_OverloadDelegationNotRecursion(t *testing.T) {
	// The 1-arg updateItem delegates to the 2-arg overload inside a loop. It shares the
	// name but not the arity, so it must NOT be flagged as self-recursion (the Conductor
	// onChangeStarted / BaseNeighbourListAdapter.updateItem false positive).
	src := "class Adapter {\n" +
		"  fun updateItem(x: Int) {\n    for (i in 0 until 3) { updateItem(i, x) }\n  }\n" +
		"  fun updateItem(i: Int, x: Int) { }\n" +
		"}"
	ff := extractAST(t, src, false)
	f, ok := findFact(ff, "pkg.Adapter.updateItem")
	if !ok {
		t.Fatalf("missing pkg.Adapter.updateItem; got %v", ff)
	}
	if v, _ := f.Props["recursive_self"].(bool); v {
		t.Errorf("overload delegation (updateItem(x) -> updateItem(i,x)) must not be recursive_self")
	}
}

func TestKtComplexity_GenuineRecursionStillArityMatched(t *testing.T) {
	// A same-arity self-call is still recursion (arity check must not suppress real cases).
	ff := extractAST(t, "fun walk(n: Node) {\n  n.children.forEach { walk(it) }\n}", false)
	f, _ := findFact(ff, "pkg.walk")
	if v, _ := f.Props["recursive_self"].(bool); !v {
		t.Errorf("same-arity self-call should still be recursive_self")
	}
}

func TestKtComplexity_ReactiveChainNotLoop(t *testing.T) {
	// An RxJava Single.flatMap { … .map { } } chain runs once per emission, not per
	// element — its map/flatMap must NOT inflate loop_depth (the getInvitableNeighbours
	// O(n³) false positive).
	src := "fun getInvitable(): Single<List<Int>> {\n" +
		"  return service.get()\n" +
		"    .flatMap { g -> service.more().map { toList(it) } }\n" +
		"    .applySchedulers()\n" +
		"}"
	ff := extractAST(t, src, false)
	f, _ := findFact(ff, "pkg.getInvitable")
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("reactive flatMap/map chain must not set loop_depth; got %v", f.Props["loop_depth"])
	}
}

func TestKtComplexity_CollectionMapStillLoop(t *testing.T) {
	// Control: in a NON-reactive function, .map with a lambda is still a collection loop.
	ff := extractAST(t, "fun r() {\n  items.map { use(it) }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if got := kIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("collection map loop_depth = %d, want 1", got)
	}
}

func TestKtComplexity_SuperDelegationNotRecursion(t *testing.T) {
	// A Conductor-style override that calls super.onChangeEnded() and then delegates to
	// a same-arity same-name overload (declared in a parent, invisible here) must NOT be
	// flagged as self-recursion.
	src := "class C {\n" +
		"  override fun onChangeEnded(handler: H, type: T) {\n" +
		"    super.onChangeEnded(handler, type)\n" +
		"    onChangeEnded(appBarVisibility, type)\n" +
		"  }\n" +
		"}"
	ff := extractAST(t, src, false)
	f, ok := findFact(ff, "pkg.C.onChangeEnded")
	if !ok {
		t.Fatalf("missing pkg.C.onChangeEnded; got %v", ff)
	}
	if v, _ := f.Props["recursive_self"].(bool); v {
		t.Errorf("override calling super.<self> + a same-name overload must not be recursive_self")
	}
}

func TestKtComplexity_RetrofitMethodPerformsIO(t *testing.T) {
	// A Retrofit endpoint method is a direct I/O leaf → performs_io.
	src := "interface Api {\n  @GET(\"users\")\n  suspend fun getUsers(): List<User>\n}"
	ff := extractAST(t, src, false)
	f, ok := findFact(ff, "pkg.Api.getUsers")
	if !ok {
		t.Fatalf("missing pkg.Api.getUsers; got %v", ff)
	}
	if v, _ := f.Props["performs_io"].(bool); !v {
		t.Errorf("Retrofit @GET method should be performs_io; props=%v", f.Props)
	}
}

func TestKtComplexity_RoomDaoMethodPerformsIO(t *testing.T) {
	// A Room @Insert DAO method is a direct I/O leaf → performs_io.
	src := "interface Dao {\n  @Insert\n  fun insertAll(items: List<Item>)\n}"
	ff := extractAST(t, src, false)
	f, _ := findFact(ff, "pkg.Dao.insertAll")
	if v, _ := f.Props["performs_io"].(bool); !v {
		t.Errorf("Room @Insert method should be performs_io; props=%v", f.Props)
	}
}

func TestKtComplexity_PlainMethodNotPerformsIO(t *testing.T) {
	// A method with no I/O annotation must NOT be performs_io.
	ff := extractAST(t, "fun helper(x: Int): Int {\n  return x + 1\n}", false)
	f, _ := findFact(ff, "pkg.helper")
	if _, present := f.Props["performs_io"]; present {
		t.Errorf("plain function must not carry performs_io")
	}
}

// --- v98: bounded-loop discounting -------------------------------------------------
//
// Three loop classes, not two (see enola-bughunt improvements/_cross-language.md §3):
//
//	constant  — a fixed iteration count (`0..2`, `listOf(a,b).forEach`): neither scales
//	            with input nor repeats, so it adds no Big-O exponent AND its calls are
//	            not N+1 candidates.
//	infinite  — `while (true)` / `do…while (true)`: driven by events/breaks, so it adds
//	            no exponent in n, but its body DOES run many times, so a per-iteration
//	            DB call is still an N+1 candidate.
//	scaling   — everything else.
//
// scaling_loop_depth counts only `scaling`; calls_in_scaling_loop excludes only
// `constant`.

// ktScalingProps returns (loop_depth, scaling_loop_depth, hasScalingDepthKey).
func ktScalingProps(t *testing.T, src string) (int, int, bool) {
	t.Helper()
	ff := extractAST(t, src, false)
	f, ok := findFact(ff, "pkg.r")
	if !ok {
		t.Fatalf("missing pkg.r; got %v", ff)
	}
	_, has := f.Props["scaling_loop_depth"]
	return kIntProp(t, f, "loop_depth"), kIntProp(t, f, "scaling_loop_depth"), has
}

func TestKtComplexity_ScalingLoopDepth_ConstantRangeDiscounted(t *testing.T) {
	for _, src := range []string{
		"fun r() {\n  for (i in 0..2) { use(i) }\n}",
		"fun r() {\n  for (i in 0 until 3) { use(i) }\n}",
		"fun r() {\n  for (i in 2 downTo 0) { use(i) }\n}",
		"fun r() {\n  for (i in 0..10 step 2) { use(i) }\n}",
		"fun r() {\n  for (x in listOf(1, 2, 3)) { use(x) }\n}",
	} {
		ld, sld, has := ktScalingProps(t, src)
		if ld != 1 || sld != 0 || !has {
			t.Errorf("%s\n  loop_depth=%d scaling_loop_depth=%d present=%v; want 1, 0, true", src, ld, sld, has)
		}
	}
}

func TestKtComplexity_ScalingLoopDepth_VariableRangeNotDiscounted(t *testing.T) {
	for _, src := range []string{
		"fun r(n: Int) {\n  for (i in 0 until n) { use(i) }\n}",
		"fun r(n: Int) {\n  for (i in 0..n) { use(i) }\n}",
		"fun r() {\n  for (x in items) { use(x) }\n}",
	} {
		ld, sld, has := ktScalingProps(t, src)
		if ld != 1 || sld != 1 || !has {
			t.Errorf("%s\n  loop_depth=%d scaling_loop_depth=%d present=%v; want 1, 1, true", src, ld, sld, has)
		}
	}
}

func TestKtComplexity_ScalingLoopDepth_ConstantIteratorReceiverDiscounted(t *testing.T) {
	for _, src := range []string{
		"fun r() {\n  listOf(\"a\", \"b\").forEach { use(it) }\n}",
		"fun r() {\n  listOfNotNull(\"a\", \"b\").forEach { use(it) }\n}",
		"fun r() {\n  setOf(1, 2).forEach { use(it) }\n}",
		"fun r() {\n  STOP_CHARS.forEach { use(it) }\n}",
		"fun r() {\n  Screen.STOP_CHARS.forEach { use(it) }\n}",
		"fun r() {\n  listOf(1, 2).sorted().forEach { use(it) }\n}",
	} {
		ld, sld, has := ktScalingProps(t, src)
		if ld != 1 || sld != 0 || !has {
			t.Errorf("%s\n  loop_depth=%d scaling_loop_depth=%d present=%v; want 1, 0, true", src, ld, sld, has)
		}
	}
}

func TestKtComplexity_ScalingLoopDepth_VariableIteratorReceiverNotDiscounted(t *testing.T) {
	// A local `val ratings = listOf(...)` must NOT be constant-folded: the receiver is a
	// mixed-case identifier at the call site, so it stays scaling. (AddEntryScreen.kt:650.)
	for _, src := range []string{
		"fun r() {\n  items.forEach { use(it) }\n}",
		"fun r() {\n  val ratings = listOf(1, 2)\n  ratings.forEach { use(it) }\n}",
		"fun r() {\n  service.load().forEach { use(it) }\n}",
	} {
		ld, sld, has := ktScalingProps(t, src)
		if ld != 1 || sld != 1 || !has {
			t.Errorf("%s\n  loop_depth=%d scaling_loop_depth=%d present=%v; want 1, 1, true", src, ld, sld, has)
		}
	}
}

// The SkillRadarChart shape (HcpPerformanceScreen.kt:1330): a constant outer loop
// wrapping a scaling inner one. loop_depth stays 2; the Big-O exponent drops to 1.
func TestKtComplexity_ScalingLoopDepth_ConstantOuterScalingInner(t *testing.T) {
	src := "fun r(sides: Int) {\n  for (ring in 1..6) {\n    for (i in 0 until sides) { use(i) }\n  }\n}"
	ld, sld, has := ktScalingProps(t, src)
	if ld != 2 || sld != 1 || !has {
		t.Fatalf("loop_depth=%d scaling_loop_depth=%d present=%v; want 2, 1, true", ld, sld, has)
	}
}

func TestKtComplexity_ScalingLoopDepth_ConstantInnerScalingOuter(t *testing.T) {
	src := "fun r() {\n  items.forEach { row ->\n    for (star in 1..5) { use(star) }\n  }\n}"
	ld, sld, has := ktScalingProps(t, src)
	if ld != 2 || sld != 1 || !has {
		t.Fatalf("loop_depth=%d scaling_loop_depth=%d present=%v; want 2, 1, true", ld, sld, has)
	}
}

// An infinite loop adds no factor of n, so it is discounted from scaling_loop_depth —
// exactly like a constant loop. It differs only for calls_in_scaling_loop (below).
func TestKtComplexity_ScalingLoopDepth_InfiniteLoopDiscounted(t *testing.T) {
	for _, src := range []string{
		"fun r() {\n  while (true) { use(1) }\n}",
		"fun r() {\n  do { use(1) } while (true)\n}",
	} {
		ld, sld, has := ktScalingProps(t, src)
		if ld != 1 || sld != 0 || !has {
			t.Errorf("%s\n  loop_depth=%d scaling_loop_depth=%d present=%v; want 1, 0, true", src, ld, sld, has)
		}
	}
}

func TestKtComplexity_ScalingLoopDepth_ConditionalWhileNotDiscounted(t *testing.T) {
	src := "fun r(n: Int) {\n  while (n > 0) { use(n) }\n}"
	ld, sld, has := ktScalingProps(t, src)
	if ld != 1 || sld != 1 || !has {
		t.Fatalf("loop_depth=%d scaling_loop_depth=%d present=%v; want 1, 1, true", ld, sld, has)
	}
}

// scaling_loop_depth is absent when there is no loop at all — presence must mean
// "this extractor computed it", so perf's HasScalingDepth fallback stays correct.
func TestKtComplexity_ScalingLoopDepth_AbsentWithoutLoops(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  use(1)\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if _, has := f.Props["scaling_loop_depth"]; has {
		t.Fatalf("scaling_loop_depth must be absent when the function has no loops")
	}
}

// A constant loop still counts as a loop (cyclomatic) and a loop construct
// (loop_count) — only the Big-O exponent is discounted. Mirrors goextractor.
func TestKtComplexity_LoopCountAndCyclomaticUnchangedByBounding(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  for (i in 0..2) { use(i) }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if got := kIntProp(t, f, "loop_count"); got != 1 {
		t.Errorf("loop_count = %d, want 1", got)
	}
	if got := kIntProp(t, f, "cyclomatic"); got != 2 {
		t.Errorf("cyclomatic = %d, want 2", got)
	}
	if got := kIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
}

// --- v99: calls_in_scaling_loop counts REPEATED loops, not just scaling ones --------

// ktCallProps returns (calls_in_loop, calls_in_scaling_loop, hasScalingCallsKey).
func ktCallProps(t *testing.T, src string) ([]string, []string, bool) {
	t.Helper()
	ff := extractAST(t, src, false)
	f, ok := findFact(ff, "pkg.r")
	if !ok {
		t.Fatalf("missing pkg.r; got %v", ff)
	}
	_, has := f.Props["calls_in_scaling_loop"]
	return kStrSlice(f, "calls_in_loop"), kStrSlice(f, "calls_in_scaling_loop"), has
}

// A call inside a constant loop runs a fixed number of times — not an N+1 candidate.
// The key must still be PRESENT (and empty), or perf.scalingLoopCalls() falls back to
// the unfiltered calls_in_loop and the discount is silently defeated.
func TestKtComplexity_CallsInScalingLoop_ConstantExcluded(t *testing.T) {
	inLoop, inScaling, has := ktCallProps(t, "fun r() {\n  listOf(1, 2).forEach { dao.insert(it) }\n}")
	if !kContains(inLoop, "dao.insert") {
		t.Fatalf("calls_in_loop = %v, want it to contain dao.insert", inLoop)
	}
	if !has {
		t.Fatalf("calls_in_scaling_loop key must be present even when empty")
	}
	if len(inScaling) != 0 {
		t.Fatalf("calls_in_scaling_loop = %v, want empty", inScaling)
	}
}

// The regression this whole split exists to prevent: `while (true) { repo.getById(id) }`
// is a chain-walk / retry loop. It adds no Big-O exponent, but the DB call still runs
// once per iteration, so it MUST remain an N+1 candidate.
// Cf. golf's OrganizationRepository.GetOrganizationPath (high-severity, true positive).
func TestKtComplexity_CallsInScalingLoop_InfiniteLoopCallsRetained(t *testing.T) {
	inLoop, inScaling, has := ktCallProps(t, "fun r() {\n  while (true) { repo.getById(id) }\n}")
	if !has {
		t.Fatalf("calls_in_scaling_loop key must be present")
	}
	if !kContains(inLoop, "repo.getById") {
		t.Fatalf("calls_in_loop = %v, want it to contain repo.getById", inLoop)
	}
	if !kContains(inScaling, "repo.getById") {
		t.Fatalf("calls_in_scaling_loop = %v, want it to retain repo.getById "+
			"(an infinite loop repeats, even though it does not scale with n)", inScaling)
	}
}

func TestKtComplexity_CallsInScalingLoop_ScalingRetained(t *testing.T) {
	_, inScaling, _ := ktCallProps(t, "fun r() {\n  items.forEach { dao.insert(it) }\n}")
	if !kContains(inScaling, "dao.insert") {
		t.Fatalf("calls_in_scaling_loop = %v, want it to contain dao.insert", inScaling)
	}
}

// Mixed: only the constant loop's call is filtered out.
func TestKtComplexity_CallsInScalingLoop_MixedLoops(t *testing.T) {
	src := "fun r() {\n  listOf(1, 2).forEach { dao.insertConst(it) }\n  items.forEach { dao.insertScaling(it) }\n}"
	inLoop, inScaling, _ := ktCallProps(t, src)
	if !kContains(inLoop, "dao.insertConst") || !kContains(inLoop, "dao.insertScaling") {
		t.Fatalf("calls_in_loop = %v, want both", inLoop)
	}
	if kContains(inScaling, "dao.insertConst") {
		t.Errorf("calls_in_scaling_loop = %v, must not contain the constant-loop call", inScaling)
	}
	if !kContains(inScaling, "dao.insertScaling") {
		t.Errorf("calls_in_scaling_loop = %v, want dao.insertScaling", inScaling)
	}
}

// calls_in_scaling_loop is absent when calls_in_loop is absent — no loop calls at all.
func TestKtComplexity_CallsInScalingLoop_AbsentWithoutLoopCalls(t *testing.T) {
	ff := extractAST(t, "fun r() {\n  for (i in 0..2) { i + 1 }\n}", false)
	f, _ := findFact(ff, "pkg.r")
	if _, has := f.Props["calls_in_scaling_loop"]; has {
		t.Fatalf("calls_in_scaling_loop must be absent when calls_in_loop is")
	}
}
