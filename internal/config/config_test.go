package config

import "testing"

// TestDefaultIgnoresNestedBuildAndPods locks in the ignore-pattern fix: build
// output must be ignored at ANY depth (Gradle/Android emit data/build/...), and
// CocoaPods must be excluded. The bare "build/**" (top-level only) must be gone.
func TestDefaultIgnoresNestedBuildAndPods(t *testing.T) {
	has := func(want string) bool {
		for _, p := range Default().Ignore {
			if p == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"**/build/**", "**/Pods/**"} {
		if !has(want) {
			t.Errorf("Default().Ignore missing %q", want)
		}
	}
	if has("build/**") {
		t.Error("Default().Ignore still has top-level-only \"build/**\"; want \"**/build/**\"")
	}
}

// TestDefaultIgnoresPythonEnvs locks in the .venv fix: Python virtual
// environments and installed dependencies must be ignored at any depth so a
// repo-local .venv (whole dependency tree) is never indexed.
func TestDefaultIgnoresPythonEnvs(t *testing.T) {
	has := func(want string) bool {
		for _, p := range Default().Ignore {
			if p == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"**/.venv/**", "**/venv/**", "**/site-packages/**"} {
		if !has(want) {
			t.Errorf("Default().Ignore missing %q", want)
		}
	}
}

// TestDefaultTestGlobsCoverGoAndStayIgnored pins both halves of the test-ref
// contract for Go (GAP-GO-06, v100).
//
// A test file's references survive only if it is BOTH ignored for normal indexing
// AND matched by a TestGlob — engine.walkRepo collects an ignored file for
// reference-only extraction only when matchesTestGlob says so. Adding a glob to
// one list and not the other silently drops the file (ignored, never recovered)
// or indexes test symbols as production code. config.go states the invariant in a
// comment; this asserts it.
//
// Go's bare-suffix form is correct and must stay: unlike Ruby — where
// "**/*_test.rb" swallowed a production job named ..._ab_test.rb and had to become
// directory-scoped (v97) — the Go toolchain DEFINES any *_test.go as a test file,
// so no production file can collide.
func TestDefaultTestGlobsCoverGoAndStayIgnored(t *testing.T) {
	cfg := Default()

	if !contains(cfg.TestGlobs, "**/*_test.go") {
		t.Errorf("Default().TestGlobs missing %q — Go test files are ignored but never recovered", "**/*_test.go")
	}

	for _, g := range cfg.TestGlobs {
		if !contains(cfg.Ignore, g) {
			t.Errorf("TestGlob %q is not in Default().Ignore; a test glob that is not ignored indexes test symbols as production code", g)
		}
	}
}
