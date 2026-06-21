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
