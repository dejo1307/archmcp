package jvmsrc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// writeRepo materializes files (relpath -> contents) under a temp dir and returns
// the repo root and the relative file list.
func writeRepo(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	var rel []string
	for p, content := range files {
		abs := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		rel = append(rel, p)
	}
	return root, rel
}

// TestBuildPackageIndex_MultiModuleSamePrefix is the core regression: two Gradle
// modules (app, api) both root packages at com.example.app.*. Each package must
// map to ITS OWN module directory, not collapse onto one source root.
func TestBuildPackageIndex_MultiModuleSamePrefix(t *testing.T) {
	root, files := writeRepo(t, map[string]string{
		"app/src/main/java/com/example/app/ui/Home.kt":                       "package com.example.app.ui\n",
		"api/src/main/java/com/example/app/api/model/search/SearchResp.java": "package com.example.app.api.model.search;\n",
	})
	idx := BuildPackageIndex(root, files)

	if got, want := idx["com.example.app.ui"], "app/src/main/java/com/example/app/ui"; got != want {
		t.Errorf("com.example.app.ui -> %q, want %q", got, want)
	}
	if got, want := idx["com.example.app.api.model.search"], "api/src/main/java/com/example/app/api/model/search"; got != want {
		t.Errorf("com.example.app.api.model.search -> %q, want %q", got, want)
	}
}

// TestBuildPackageIndex_MainWinsOverVariant ensures that when a build-variant
// source set (src/debug) declares the same root package as src/main, imports of
// that package resolve to the MAIN module — a variant must never shadow main
// (which would misroute the whole app's afferent coupling onto the variant).
func TestBuildPackageIndex_MainWinsOverVariant(t *testing.T) {
	root, files := writeRepo(t, map[string]string{
		"app/src/debug/java/com/example/app/DebugApp.kt": "package com.example.app\n",
		"app/src/main/java/com/example/app/App.kt":       "package com.example.app\n",
	})
	idx := BuildPackageIndex(root, files)
	if got, want := idx["com.example.app"], "app/src/main/java/com/example/app"; got != want {
		t.Errorf("com.example.app -> %q, want main source set %q (variant must not shadow main)", got, want)
	}
}

// TestBuildPackageIndex_ExcludesTestSources ensures src/test and src/androidTest
// files never enter the index (so imports don't resolve into test dirs and a main
// package isn't shadowed by a test helper sharing its name).
func TestBuildPackageIndex_ExcludesTestSources(t *testing.T) {
	root, files := writeRepo(t, map[string]string{
		"app/src/main/java/de/x/Foo.kt":         "package de.x\n",
		"app/src/test/java/de/x/FooTest.kt":     "package de.x\n",
		"app/src/androidTest/java/de/y/UiIT.kt": "package de.y\n",
	})
	idx := BuildPackageIndex(root, files)

	if got, want := idx["de.x"], "app/src/main/java/de/x"; got != want {
		t.Errorf("de.x -> %q, want production dir %q", got, want)
	}
	if _, ok := idx["de.y"]; ok {
		t.Errorf("androidTest-only package de.y should not be indexed, got %q", idx["de.y"])
	}
}

// TestResolveImport walks the longest-prefix match, including nested-type imports.
func TestResolveImport(t *testing.T) {
	idx := map[string]string{
		"com.example.app.api.model.search": "api/src/main/java/com/example/app/api/model/search",
	}
	cases := []struct {
		imp    string
		want   string
		wantOK bool
	}{
		{"com.example.app.api.model.search.SearchResp", "api/src/main/java/com/example/app/api/model/search/SearchResp", true},
		{"com.example.app.api.model.search.Outer.Inner", "api/src/main/java/com/example/app/api/model/search/Outer/Inner", true},
		{"com.example.app.api.model.search.*", "api/src/main/java/com/example/app/api/model/search", true},
		{"kotlinx.coroutines.flow.Flow", "", false},
	}
	for _, c := range cases {
		got, ok := ResolveImport(c.imp, idx)
		if ok != c.wantOK || got != c.want {
			t.Errorf("ResolveImport(%q) = (%q, %v), want (%q, %v)", c.imp, got, ok, c.want, c.wantOK)
		}
	}
}

// TestModuleRole classifies production vs test source-set directories, including
// compound test-module names that compile as src/main (release-tests, ui-test-utils,
// test-lab) via the sub-token rule — and confirms it does NOT misfire on
// single-token names that merely contain "test" (latest, contest, the abtest feature).
func TestModuleRole(t *testing.T) {
	cases := map[string]string{
		"app/src/main/java/de/x":                            facts.ModuleRoleProduction,
		"app/src/test/java/de/x":                            facts.ModuleRoleTest,
		"app/src/androidTest/java/de/y":                     facts.ModuleRoleTest,
		"release-tests/src/main/java/de/x/releasetests":     facts.ModuleRoleTest,
		"ui-test-utils/src/main/java/de/x/testutils":        facts.ModuleRoleTest,
		"test-lab/src/main/java/de/x":                       facts.ModuleRoleTest,
		"app/src/main/java/de/x/latest":                     facts.ModuleRoleProduction,
		"app/src/main/java/de/x/contest":                    facts.ModuleRoleProduction,
		"business/src/main/java/de/example/business/abtest": facts.ModuleRoleProduction,
	}
	for dir, want := range cases {
		if got := ModuleRole(dir); got != want {
			t.Errorf("ModuleRole(%q) = %q, want %q", dir, got, want)
		}
	}
}
