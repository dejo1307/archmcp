package kotlinextractor

import (
	"os"
	"path/filepath"
	"testing"
)

// writeKotlinRepo writes files (rel path -> content) into a temp repo and returns
// the repo dir and the relative file list.
func writeKotlinRepo(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	var rel []string
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		rel = append(rel, name)
	}
	return dir, rel
}

// TestDetectSourceRoot_IgnoresTestSourceSets is the regression guard for the
// coupling-collapse bug: when a test-source-set file is walked first, source-root
// detection must still pick the production (main) root, not the androidTest one.
func TestDetectSourceRoot_IgnoresTestSourceSets(t *testing.T) {
	repo, files := writeKotlinRepo(t, map[string]string{
		// androidTest file deliberately first in the map; map order is random so the
		// fix must not depend on order — it skips test sources entirely.
		"app/src/androidTest/java/com/foo/AppTest.kt": "package com.foo\nclass AppTest\n",
		"app/src/test/java/com/foo/UnitTest.kt":       "package com.foo\nclass UnitTest\n",
		"app/src/main/java/com/foo/A.kt":              "package com.foo\nclass A\n",
		"app/src/main/java/com/foo/B.kt":              "package com.foo\nclass B\n",
	})

	got := detectKotlinSourceRoot(repo, files)
	want := "app/src/main/java/"
	if got != want {
		t.Errorf("detectKotlinSourceRoot = %q, want %q (must prefer the production source set)", got, want)
	}
}

// TestDetectSourceRoot_MostCommonProductionRoot picks the dominant production
// root, not whichever file happens to be scanned first.
func TestDetectSourceRoot_MostCommonProductionRoot(t *testing.T) {
	repo, files := writeKotlinRepo(t, map[string]string{
		"feature/src/main/kotlin/com/x/One.kt": "package com.x\nclass One\n",
		"app/src/main/java/com/foo/A.kt":       "package com.foo\nclass A\n",
		"app/src/main/java/com/foo/B.kt":       "package com.foo\nclass B\n",
		"app/src/main/java/com/foo/bar/C.kt":   "package com.foo.bar\nclass C\n",
	})
	// app/src/main/java/ appears 3x; feature/src/main/kotlin/ once → app wins.
	if got := detectKotlinSourceRoot(repo, files); got != "app/src/main/java/" {
		t.Errorf("detectKotlinSourceRoot = %q, want app/src/main/java/ (most common)", got)
	}
}

// TestDetectSourceRoot_AllTestsFallback: if every file is a test source (no
// production code), fall back to a detected root rather than returning "".
func TestDetectSourceRoot_AllTestsFallback(t *testing.T) {
	repo, files := writeKotlinRepo(t, map[string]string{
		"app/src/test/java/com/foo/UnitTest.kt": "package com.foo\nclass UnitTest\n",
	})
	if got := detectKotlinSourceRoot(repo, files); got != "app/src/test/java/" {
		t.Errorf("detectKotlinSourceRoot = %q, want app/src/test/java/ (fallback when only tests)", got)
	}
}

// TestDetectBasePackage_GroovyAndKotlinDSL is the regression guard for the
// cross-module import false-orphan bug: the namespace must be read from BOTH a
// Groovy `app/build.gradle` (single quotes, no `=`) and a Kotlin-DSL
// `app/build.gradle.kts` (double quotes). A double-quote-only regex left the base
// package empty on Groovy projects, so every in-repo import resolved as external
// and bare calls to imported top-level functions emitted no call edge.
func TestDetectBasePackage_GroovyAndKotlinDSL(t *testing.T) {
	t.Run("groovy single-quote", func(t *testing.T) {
		repo, _ := writeKotlinRepo(t, map[string]string{
			"app/build.gradle": "android {\n    namespace 'com.example.app'\n}\n",
		})
		if got := detectKotlinBasePackage(repo); got != "com.example.app" {
			t.Errorf("detectKotlinBasePackage = %q, want com.example.app (Groovy single-quote)", got)
		}
	})
	t.Run("kotlin-dsl double-quote", func(t *testing.T) {
		repo, _ := writeKotlinRepo(t, map[string]string{
			"app/build.gradle.kts": "android {\n    namespace = \"com.example.app\"\n}\n",
		})
		if got := detectKotlinBasePackage(repo); got != "com.example.app" {
			t.Errorf("detectKotlinBasePackage = %q, want com.example.app (Kotlin-DSL)", got)
		}
	})
}

// TestResolveKotlinImport_InternalVsExternal checks that an in-repo import
// (matching the base package) resolves as internal so its bare-call edge is
// emitted, while a third-party import stays external.
func TestResolveKotlinImport_InternalVsExternal(t *testing.T) {
	const root, base = "app/src/main/java/", "com.example.app"
	if _, ext := resolveKotlinImport("com.example.app.ui.common.setMargin", nil, root, base); ext {
		t.Error("in-repo import should resolve as internal (external=false)")
	}
	if _, ext := resolveKotlinImport("retrofit2.Retrofit", nil, root, base); !ext {
		t.Error("third-party import should stay external (external=true)")
	}
}
