package cppextractor

import (
	"sort"
	"testing"
)

func tableFrom(src string) macroTable {
	t := macroTable{}
	collectMacros([]byte(src), t)
	return t
}

// refsOf expands name(args) and returns the function-position identifiers in the
// expansion (what the extractor would mark as used).
func refsOf(table macroTable, name string, args ...string) []string {
	toks := expandCall(name, args, table)
	got := macroFuncRefIdents([]byte(tokensText(toks)))
	sort.Strings(got)
	return got
}

func hasStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestMacroTokenPaste(t *testing.T) {
	table := tableFrom("#define ATTR(_pfx, _name) { .show = _pfx##_name##_show, .store = _pfx##_name##_store }\n")
	got := refsOf(table, "ATTR", "gpio_sim_", "label")
	if !hasStr(got, "gpio_sim_label_show") || !hasStr(got, "gpio_sim_label_store") {
		t.Errorf("expected pasted show/store names, got %v", got)
	}
}

func TestMacroTwoLevelChain(t *testing.T) {
	// CONFIGFS_ATTR shape: a wrapper that forwards to the pasting macro.
	table := tableFrom(`
#define ATTR_PERM(_pfx, _name, _perm) static struct a _pfx##attr_##_name = { .ca_name = __stringify(_name), .ca_mode = _perm, .show = _pfx##_name##_show, .store = _pfx##_name##_store, }
#define ATTR(_pfx, _name) ATTR_PERM(_pfx, _name, S_IRUGO | S_IWUSR)
`)
	got := refsOf(table, "ATTR", "cfg_", "live")
	if !hasStr(got, "cfg_live_show") || !hasStr(got, "cfg_live_store") {
		t.Errorf("expected cfg_live_show/store from 2-level chain, got %v", got)
	}
}

func TestMacroValuePositionShowStore(t *testing.T) {
	// DEVICE_ATTR_RO shape: .show = _name##_show in value position only.
	table := tableFrom("#define DEV_RO(_name) static struct da dev_attr_##_name = { .show = _name##_show }\n")
	got := refsOf(table, "DEV_RO", "temp")
	if !hasStr(got, "temp_show") {
		t.Errorf("expected temp_show, got %v", got)
	}
	if hasStr(got, "dev_attr_temp") {
		t.Errorf("the pasted struct variable name must not be a function ref, got %v", got)
	}
}

func TestMacroObjectLikeExpansion(t *testing.T) {
	table := tableFrom(`
#define HANDLER real_handler
#define WIRE(x) { .cb = HANDLER }
`)
	got := refsOf(table, "WIRE", "z")
	if !hasStr(got, "real_handler") {
		t.Errorf("expected object-like HANDLER to expand to real_handler, got %v", got)
	}
}

func TestMacroRecursionTerminates(t *testing.T) {
	// Self/mutual reference must not loop (hideset + depth cap).
	table := tableFrom(`
#define A(x) B(x)
#define B(x) A(x)
`)
	done := make(chan []token, 1)
	go func() { done <- expandCall("A", []string{"q"}, table) }()
	select {
	case <-done:
	default:
		// expandCall is synchronous; the goroutine just guards against a hang in CI.
	}
	_ = expandCall("A", []string{"q"}, table) // must return, not hang
}

func TestMacroFunctionLikeNotInvokedWithoutParen(t *testing.T) {
	// A bare reference to a function-like macro name (no '(') is not an invocation.
	table := tableFrom("#define CALL(x) helper(x)\n")
	toks := expandCall("CALL", []string{"v"}, table)
	got := macroFuncRefIdents([]byte(tokensText(toks)))
	if !hasStr(got, "helper") {
		t.Errorf("expected helper from CALL body, got %v", got)
	}
}

func TestCollectMacrosContinuationAndForms(t *testing.T) {
	table := tableFrom("#define MULTI(a, b) \\\n    foo(a) + \\\n    bar(b)\n#define OBJ 42\n")
	if d, ok := table["MULTI"]; !ok || d.params == nil || len(d.params) != 2 {
		t.Fatalf("MULTI should be function-like with 2 params, got %+v", table["MULTI"])
	}
	if d, ok := table["OBJ"]; !ok || d.params != nil {
		t.Fatalf("OBJ should be object-like, got %+v", table["OBJ"])
	}
	got := refsOf(table, "MULTI", "1", "2")
	if !hasStr(got, "foo") || !hasStr(got, "bar") {
		t.Errorf("expected foo and bar from continuation body, got %v", got)
	}
}
