package rubyextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// --- helpers (symbolsByName / hasCall live in ruby_test.go) ---

func rbIntProp(t *testing.T, f facts.Fact, key string) int {
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

func rbStrSlice(f facts.Fact, key string) []string {
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

func rbContains(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}

// --- tests ---

func TestRbComplexity_EachBlockIsLoop(t *testing.T) {
	src := `class Worker
  def run(users)
    users.each do |u|
      notify(u)
    end
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/worker.rb", false, false))["Worker#run"]
	if got := rbIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := rbIntProp(t, f, "loop_count"); got != 1 {
		t.Errorf("loop_count = %d, want 1", got)
	}
	if cil := rbStrSlice(f, "calls_in_loop"); !rbContains(cil, "notify") {
		t.Errorf("calls_in_loop = %v, want to contain notify", cil)
	}
}

func TestRbComplexity_NestedIterators(t *testing.T) {
	src := `class Worker
  def run(users)
    users.each do |u|
      u.posts.each do |p|
        log(p)
      end
    end
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/worker.rb", false, false))["Worker#run"]
	if got := rbIntProp(t, f, "loop_depth"); got != 2 {
		t.Errorf("loop_depth = %d, want 2", got)
	}
	if cil := rbStrSlice(f, "calls_in_loop"); !rbContains(cil, "log") {
		t.Errorf("calls_in_loop = %v, want to contain log", cil)
	}
}

func TestRbComplexity_ConstantBoundLoopNoDepth(t *testing.T) {
	// A constant-bounded iterator runs a fixed number of times, so it is a loop
	// (cyclomatic) but adds NO scaling depth.
	for _, tc := range []struct{ name, body string }{
		{"integer.times", "6.times { |i| work(i) }"},
		{"literal array", "[:a, :b, :c].each { |x| work(x) }"},
		{"word array", "%w[a b c].each { |x| work(x) }"},
		{"screaming constant", "STOP_CHARS.each { |c| work(c) }"},
	} {
		src := "class Worker\n  def run\n    " + tc.body + "\n  end\nend\n"
		f := symbolsByName(extractFileAST([]byte(src), "app/worker.rb", false, false))["Worker#run"]
		if _, present := f.Props["loop_depth"]; present {
			t.Errorf("%s: loop_depth = %v, want unset (constant-bounded loop)", tc.name, f.Props["loop_depth"])
		}
		if got := rbIntProp(t, f, "loop_count"); got != 1 {
			t.Errorf("%s: loop_count = %d, want 1 (still a loop for cyclomatic)", tc.name, got)
		}
	}
}

func TestRbComplexity_LiteralChainLoopIsBounded(t *testing.T) {
	// A bounded literal behind a trailing chain method stays bounded: the inner
	// `[a, b].compact.all?` is ≤2 elements, so nesting it in a scaling outer loop is
	// O(n), not O(n²).
	src := `class Gate
  def valid?(items)
    items.each do |it|
      [it.a, it.b].compact.all? do |url|
        allowed?(url)
      end
    end
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/gate.rb", false, false))["Gate#valid?"]
	if got := rbIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1 (literal-chain inner loop is bounded)", got)
	}

	// A chained bounded literal as an outer loop adds no depth at all.
	src2 := `class Gate
  def run
    [1, 2].map { |x| x }.each { |y| work(y) }
  end
end
`
	g := symbolsByName(extractFileAST([]byte(src2), "app/gate.rb", false, false))["Gate#run"]
	if _, present := g.Props["loop_depth"]; present {
		t.Errorf("loop_depth = %v, want unset ([1,2].map.each is bounded)", g.Props["loop_depth"])
	}
}

func TestRbComplexity_ConstantInnerLoopDoesNotMultiply(t *testing.T) {
	// A scaling outer loop with a CONSTANT inner loop is O(n), not O(n²): only the
	// outer contributes depth, but per-iteration I/O is still measured against n.
	src := `class Worker
  def run(users)
    users.each do |u|
      STOP_CHARS.each do |c|
        u.save
      end
    end
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/worker.rb", false, false))["Worker#run"]
	if got := rbIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1 (constant inner loop must not multiply)", got)
	}
	if cil := rbStrSlice(f, "calls_in_loop"); !rbContains(cil, "save") {
		t.Errorf("calls_in_loop = %v, want to contain save (per-iteration I/O still flagged)", cil)
	}
}

func TestRbComplexity_IteratorReceiverEvaluatedOnce(t *testing.T) {
	// User.where(...) is the iterator's receiver — evaluated once, NOT per element.
	// Mailer.deliver(u) runs inside the block, so it is the in-loop call.
	src := `class Worker
  def run
    User.where(active: true).each do |u|
      Mailer.deliver(u)
    end
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/worker.rb", true, false))["Worker#run"]
	if !hasCall(f, "User.where") || !hasCall(f, "Mailer.deliver") {
		t.Errorf("expected call edges to User.where and Mailer.deliver; relations=%v", f.Relations)
	}
	cil := rbStrSlice(f, "calls_in_loop")
	if !rbContains(cil, "Mailer.deliver") {
		t.Errorf("calls_in_loop = %v, want to contain Mailer.deliver", cil)
	}
	if rbContains(cil, "User.where") {
		t.Errorf("calls_in_loop = %v, must NOT contain User.where (iterator receiver runs once)", cil)
	}
}

func TestRbComplexity_WhileLoop(t *testing.T) {
	src := `class Worker
  def run
    while pending?
      process
    end
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/worker.rb", false, false))["Worker#run"]
	if got := rbIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1", got)
	}
	if got := rbIntProp(t, f, "loop_count"); got != 1 {
		t.Errorf("loop_count = %d, want 1", got)
	}
	if cil := rbStrSlice(f, "calls_in_loop"); !rbContains(cil, "process") {
		t.Errorf("calls_in_loop = %v, want to contain process", cil)
	}
}

func TestRbComplexity_RecursiveSelf(t *testing.T) {
	src := `class Calc
  def fib(n)
    return n if n < 2
    fib(n - 1) + fib(n - 2)
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/calc.rb", false, false))["Calc#fib"]
	v, ok := f.Props["recursive_self"].(bool)
	if !ok || !v {
		t.Errorf("recursive_self = %v (ok=%v), want true", f.Props["recursive_self"], ok)
	}
	// 1 (base) + `return n if n < 2` (if_modifier) = 2
	if got := rbIntProp(t, f, "cyclomatic"); got != 2 {
		t.Errorf("cyclomatic = %d, want 2", got)
	}
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("loop_depth should be omitted for a loop-free method, got %v", f.Props["loop_depth"])
	}
}

func TestRbComplexity_SuperIsNotRecursion(t *testing.T) {
	// `super` climbs the inheritance chain and terminates — it is not self-recursion.
	// It was the dominant recursion false positive (every override calling super).
	src := `class ShadowUser < Base
  def hood_id
    deprecate(__method__)
    super
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/shadow_user.rb", false, false))["ShadowUser#hood_id"]
	if v, _ := f.Props["recursive_self"].(bool); v {
		t.Errorf("recursive_self = true for a method whose only self-name call is `super`; want unset")
	}
	// The `super` call edge must still be recorded (dead-code marks the ancestor used).
	if !hasCall(f, "hood_id") {
		t.Errorf("super should still record a call edge to the same-named ancestor; relations = %v", f.Relations)
	}
}

func TestRbComplexity_DelegationIsNotRecursion(t *testing.T) {
	// A same-named call on an explicit, non-self receiver (SimpleDelegator/decorator,
	// `@delegate.render`) is a call on a DIFFERENT object, not self-recursion. A
	// receiverless self-call still is.
	delegating := `class Presenter
  def render(x)
    @delegate_object.render(x)
  end
end
`
	f := symbolsByName(extractFileAST([]byte(delegating), "app/presenter.rb", false, false))["Presenter#render"]
	if v, _ := f.Props["recursive_self"].(bool); v {
		t.Errorf("recursive_self = true for a delegated same-named call; want unset")
	}

	genuine := `class Walker
  def render(node)
    render(node.child)
  end
end
`
	g := symbolsByName(extractFileAST([]byte(genuine), "app/walker.rb", false, false))["Walker#render"]
	if v, ok := g.Props["recursive_self"].(bool); !ok || !v {
		t.Errorf("recursive_self = %v (ok=%v) for a receiverless self-call; want true", g.Props["recursive_self"], ok)
	}
}

func TestRbComplexity_SelfClassSiblingIsNotRecursion(t *testing.T) {
	// An instance method delegating to the same-named CLASS method
	// (`self.class.photo_url`) calls a DIFFERENT method — not recursion. But plain
	// `self.foo` (same object, same method) still is.
	sibling := `class Presenter
  def photo_url(user)
    self.class.photo_url(user)
  end
end
`
	f := symbolsByName(extractFileAST([]byte(sibling), "app/presenter.rb", false, false))["Presenter#photo_url"]
	if v, _ := f.Props["recursive_self"].(bool); v {
		t.Errorf("recursive_self = true for a self.class sibling delegation; want unset")
	}

	selfDispatch := `class Worker
  def run
    self.run
  end
end
`
	g := symbolsByName(extractFileAST([]byte(selfDispatch), "app/worker.rb", false, false))["Worker#run"]
	if v, ok := g.Props["recursive_self"].(bool); !ok || !v {
		t.Errorf("recursive_self = %v (ok=%v) for `self.run`; want true", g.Props["recursive_self"], ok)
	}
}

func TestRbComplexity_TryDispatchOnOtherReceiverIsNotRecursion(t *testing.T) {
	// `object.try(:ben?)` dispatches `ben?` to a DIFFERENT object — not recursion.
	src := `class AuthorType
  def ben?
    !!object.try(:ben?)
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/author_type.rb", false, false))["AuthorType#ben?"]
	if v, _ := f.Props["recursive_self"].(bool); v {
		t.Errorf("recursive_self = true for `object.try(:ben?)` on another receiver; want unset")
	}
}

func TestRbComplexity_InLoopAssociationRead(t *testing.T) {
	// The classic N+1: a no-arg association read inside an iterator block. It must
	// land in calls_in_loop (for the perf metric) but NOT become a graph edge, and
	// a plain attribute read (u.name) must be excluded by the cheap-method stoplist.
	src := `class Report
  def run(users)
    users.each do |u|
      u.posts
      puts u.name
    end
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/report.rb", false, false))["Report#run"]
	cil := rbStrSlice(f, "calls_in_loop")
	if !rbContains(cil, "posts") {
		t.Errorf("calls_in_loop = %v, want to contain posts (association read)", cil)
	}
	if rbContains(cil, "name") {
		t.Errorf("calls_in_loop = %v, must NOT contain name (cheap attribute)", cil)
	}
	// Metrics-only: no RelCalls graph edge for the no-arg instance call.
	if hasCall(f, "posts") || hasCall(f, "u.posts") {
		t.Errorf("u.posts must not become a call edge; relations=%v", f.Relations)
	}
}

func TestRbComplexity_InLoopAssociationChain(t *testing.T) {
	// u.posts.count — the inner association read is captured via normal recursion.
	src := `class Report
  def run(users)
    users.each { |u| total += u.posts.count }
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/report.rb", false, false))["Report#run"]
	if cil := rbStrSlice(f, "calls_in_loop"); !rbContains(cil, "posts") {
		t.Errorf("calls_in_loop = %v, want to contain posts", cil)
	}
}

func TestRbAssociationFactCarriesName(t *testing.T) {
	src := `class User < ApplicationRecord
  has_many :posts
  belongs_to :account
end
`
	result := extractFileAST([]byte(src), "app/models/user.rb", true, false)
	var gotPosts, gotAccount bool
	for _, f := range result {
		if f.Kind != facts.KindDependency {
			continue
		}
		if _, ok := f.Props["association_kind"]; !ok {
			continue
		}
		switch f.Props["association"] {
		case "posts":
			gotPosts = true
		case "account":
			gotAccount = true
		}
	}
	if !gotPosts || !gotAccount {
		t.Errorf("association facts missing Props[\"association\"] (posts=%v account=%v)", gotPosts, gotAccount)
	}
}

func TestRbComplexity_NonIteratorBlockNotLoop(t *testing.T) {
	// transaction takes a block but runs it once — it is NOT a loop.
	src := `class Worker
  def run
    ActiveRecord::Base.transaction do
      persist
    end
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/worker.rb", true, false))["Worker#run"]
	if _, present := f.Props["loop_depth"]; present {
		t.Errorf("transaction block must not be a loop; loop_depth=%v", f.Props["loop_depth"])
	}
	if cil := rbStrSlice(f, "calls_in_loop"); rbContains(cil, "persist") {
		t.Errorf("calls_in_loop = %v, must NOT contain persist (block runs once)", cil)
	}
	if !hasCall(f, "persist") {
		t.Errorf("persist should still be a call edge; relations=%v", f.Relations)
	}
}

func TestRbComplexity_BlockParamNotCall(t *testing.T) {
	// A block parameter is a local, not a method call — even when referenced bare
	// (as an argument value) and even when its name matches an association. This is
	// the dominant N+1 false positive: `each do |user| … user … end`. A genuine
	// association read on the block var (user.posts) must still be captured, proving
	// the fix is surgical.
	src := `class Worker
  def run(users)
    users.each do |user|
      notify(user)
      user.posts
    end
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/worker.rb", false, false))["Worker#run"]
	cil := rbStrSlice(f, "calls_in_loop")
	if rbContains(cil, "user") {
		t.Errorf("calls_in_loop = %v, must NOT contain user (block-local variable)", cil)
	}
	if !rbContains(cil, "notify") {
		t.Errorf("calls_in_loop = %v, want to contain notify", cil)
	}
	if !rbContains(cil, "posts") {
		t.Errorf("calls_in_loop = %v, want to contain posts (real association read on block var)", cil)
	}
}

func TestRbComplexity_FindInBatchesNotElementLoop(t *testing.T) {
	// find_in_batches yields a batch (array); the inner .map over that batch is the
	// real per-element loop. The pair must score loop_depth 1 (a single O(n) pass),
	// not 2 — otherwise a batched reindex is mislabeled O(n²).
	src := `class Reindex
  def run(model)
    model.find_in_batches do |batch|
      batch.map { |obj| present(obj) }
    end
  end
end
`
	f := symbolsByName(extractFileAST([]byte(src), "app/reindex.rb", false, false))["Reindex#run"]
	if got := rbIntProp(t, f, "loop_depth"); got != 1 {
		t.Errorf("loop_depth = %d, want 1 (find_in_batches yields batches, not elements)", got)
	}
	if cil := rbStrSlice(f, "calls_in_loop"); !rbContains(cil, "present") {
		t.Errorf("calls_in_loop = %v, want to contain present", cil)
	}
}
