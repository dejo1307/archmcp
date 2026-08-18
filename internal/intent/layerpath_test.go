package intent

import (
	"strings"
	"testing"
)

// `layers:` paths were the one declaration field with no form validation at all.
// Anything was accepted, compiled into an intent fact, and matched against every
// module — and an unsupported form matches none of them, leaving a layer order that
// validates clean and governs nothing. Both halves of the fix are pinned here: the
// dialect is now checked, and an authored backslash is normalised rather than
// refused, so the same file selects the same code on every host (issue #242).

func TestValidLayerPath_AcceptsTheTwoImplementedForms(t *testing.T) {
	for _, p := range []string{"src/lib", "src/lib/**", "app/handlers/**", "."} {
		if !ValidLayerPath(p) {
			t.Errorf("ValidLayerPath(%q) = false, want true", p)
		}
	}
}

func TestValidLayerPath_RejectsFormsTheClassifierDoesNotRead(t *testing.T) {
	// Each of these matched nothing while reporting the order in force. A glob the
	// matcher does not implement has to be a message, not silence.
	for _, p := range []string{
		"",
		"/**",
		"src/*/lib",       // a single-star segment is not a subtree
		"**/site-blocks",  // no basename form: a layer is a region, not a filename
		"src/lib/**/*.ts", // nor a suffix filter
		"src/{lib,app}/**",
		`src\lib\**`, // only reachable unnormalised; a declaration is normalised first
	} {
		if ValidLayerPath(p) {
			t.Errorf("ValidLayerPath(%q) = true, want false", p)
		}
	}
}

// The message has to name what IS allowed. A validation error that only says "no"
// leaves the author guessing at a dialect that is nowhere written down.
func TestDeclarationProblems_NamesTheAllowedLayerForms(t *testing.T) {
	d := &Declaration{Layers: []Layer{{Name: "domain", Paths: []string{"src/*/lib"}}}}
	problems := d.Problems()
	if len(problems) != 1 {
		t.Fatalf("want exactly one problem, got %v", problems)
	}
	for _, want := range []string{"layers[0]", "domain", "src/*/lib", "exact path", "prefix/** subtree"} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("problem %q does not mention %q", problems[0], want)
		}
	}
}

// A declaration written on Windows must parse, validate and select the same code as
// the identical file written on Linux. This is the reporter's first symptom: the only
// subtree form their tooling produced was the one the dialect refused.
func TestParse_NormalisesAuthoredBackslashPaths(t *testing.T) {
	d, err := Parse([]byte("layers:\n  - {name: lib, paths: ['src\\lib\\**']}\n"))
	if err != nil {
		t.Fatalf("a backslash layer path must be normalised, not refused: %v", err)
	}
	if got := d.Layers[0].Paths[0]; got != "src/lib/**" {
		t.Errorf("path = %q, want the fact-path form", got)
	}
}

// The same normalisation applies to component match patterns, which is where the
// reporter first hit the refusal ("must be an exact path, a prefix/** subtree...").
func TestParse_NormalisesComponentMatchPatterns(t *testing.T) {
	d, err := Parse([]byte("components:\n  - {name: lib, match: ['src\\lib\\**']}\n"))
	if err != nil {
		t.Fatalf("a backslash match pattern must be normalised, not refused: %v", err)
	}
	if got := d.Components[0].Match[0]; got != "src/lib/**" {
		t.Errorf("match = %q, want the fact-path form", got)
	}
}

// Normalising must not weaken the dialect: a form that is invalid with forward
// slashes stays invalid when it arrives with backslashes.
func TestParse_NormalisationDoesNotAdmitInvalidForms(t *testing.T) {
	if _, err := Parse([]byte("layers:\n  - {name: lib, paths: ['src\\*\\lib']}\n")); err == nil {
		t.Fatal("a single-star segment is not a subtree, whichever separator it is written with")
	}
}
