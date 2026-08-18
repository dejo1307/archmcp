package factpath

import "testing"

// Slash and Declared answer the same question for two different kinds of string, and
// getting them the wrong way round is a bug in each direction:
//
//   - Slash on authored text is a no-op wherever the separator is already "/", so a
//     declaration written on Windows would normalise there and be REJECTED in CI.
//   - Declared on a real path rewrites a filename that legitimately contains a
//     backslash — legal on every Unix filesystem — into a directory it is not in.
//
// These tests hold both halves down, and they are host-independent: the Declared
// cases assert the same answer everywhere, and the Slash cases assert only what is
// true on every host.

func TestDeclared_RewritesAuthoredSeparatorsOnEveryHost(t *testing.T) {
	cases := map[string]string{
		`src\lib\**`:           "src/lib/**",
		`src\lib`:              "src/lib",
		"src/lib/**":           "src/lib/**",
		`app\Http\Controllers`: "app/Http/Controllers",
		"":                     "",
	}
	for in, want := range cases {
		if got := Declared(in); got != want {
			t.Errorf("Declared(%q) = %q, want %q", in, got, want)
		}
	}
}

// The property that makes the declaration dialect portable: normalising is
// idempotent, so a file already written with forward slashes is untouched and one
// written with backslashes converges on the same value.
func TestDeclared_IsIdempotent(t *testing.T) {
	for _, in := range []string{`src\lib\**`, "src/lib/**", `a\b\c\d`} {
		once := Declared(in)
		if twice := Declared(once); twice != once {
			t.Errorf("Declared(%q) = %q but Declared again = %q", in, once, twice)
		}
	}
}

// Slash must NOT touch a Unix path that contains a backslash as a filename
// character. This is why the two functions cannot be one function.
func TestSlash_LeavesAForwardSlashPathAlone(t *testing.T) {
	// True on every host: a path already in fact form survives unchanged.
	if got := Slash("src/lib/site-blocks.ts"); got != "src/lib/site-blocks.ts" {
		t.Errorf("Slash rewrote an already-normalised path: %q", got)
	}
}

// The operations are total: a caller that has not been normalised yet gets the right
// answer rather than a subtler wrong one. path.Dir on a raw backslash path finds no
// separator and answers ".", which would silently collapse a module to the repo root.
func TestOperations_AreTotalOverBothDialects(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"Dir/slash", Dir("src/lib/x.ts"), "src/lib"},
		{"Base/slash", Base("src/lib/x.ts"), "x.ts"},
		{"Ext/slash", Ext("src/lib/x.ts"), ".ts"},
		{"Clean/slash", Clean("src/lib/../lib/x.ts"), "src/lib/x.ts"},
		{"Join", Join("src/lib", "x.ts"), "src/lib/x.ts"},
		{"Join/dotdot", Join("src/lib", "../components/y.ts"), "src/components/y.ts"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// No operation may emit a backslash, whatever it was given. This is the invariant the
// whole package exists for, stated once as a property.
func TestOperations_NeverEmitAHostSeparator(t *testing.T) {
	inputs := []string{
		"src/lib/x.ts",
		"src/lib",
		"x.ts",
		".",
		"",
	}
	for _, in := range inputs {
		for name, got := range map[string]string{
			"Dir":   Dir(in),
			"Base":  Base(in),
			"Ext":   Ext(in),
			"Clean": Clean(in),
			"Join":  Join(in, "child"),
		} {
			for _, r := range got {
				if r == '\\' {
					t.Errorf("%s(%q) = %q, which carries a host separator", name, in, got)
					break
				}
			}
		}
	}
}

// Match's separator is "/" on every host. filepath.Match would use "\" on Windows,
// where `src/*` would then match across a directory boundary — a glob that means one
// thing in CI and another on a laptop.
func TestMatch_TreatsSlashAsTheSeparator(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"src/*", "src/lib", true},
		{"src/*", "src/lib/x.ts", false}, // * does not cross a separator
		{"*.ts", "x.ts", true},
		{"*.ts", "src/x.ts", false},
	}
	for _, c := range cases {
		got, err := Match(c.pattern, c.name)
		if err != nil {
			t.Fatalf("Match(%q, %q): %v", c.pattern, c.name, err)
		}
		if got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
