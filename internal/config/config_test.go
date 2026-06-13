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
