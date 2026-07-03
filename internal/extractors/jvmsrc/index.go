// Package jvmsrc holds source-layout helpers shared by the JVM-language
// extractors (Kotlin, Java). Its job is to resolve a declared package name to the
// directory that actually holds it, so cross-module imports resolve correctly in
// multi-module Gradle/Maven projects where several modules root their packages at
// the same prefix (e.g. app/, api/, business/ all declaring de.foo.*).
package jvmsrc

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// ModuleRole classifies a JVM module directory as production or test/tooling for
// the module_role fact prop. It defers to facts.ModuleRoleForPath (which knows
// the Gradle src/test and src/androidTest layouts) and, since every JVM module
// directory holds real source, treats an otherwise-unknown directory as
// production so downstream analyses include it explicitly.
func ModuleRole(dir string) string {
	role := facts.ModuleRoleForPath(dir)
	if role == facts.ModuleRoleUnknown {
		return facts.ModuleRoleProduction
	}
	return role
}

// packageRe extracts a JVM source file's package declaration. It matches both
// Kotlin (`package de.foo.bar`) and Java (`package de.foo.bar;`) — the trailing
// `;` and any comment are excluded by the `[\w.]+` capture.
var packageRe = regexp.MustCompile(`^\s*package\s+([\w.]+)`)

// jvmSourceExts are the file extensions BuildPackageIndex reads.
func isJVMSource(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".kt") || strings.HasSuffix(p, ".java")
}

// isTestSource reports whether a file lives in a Gradle/Maven test source set
// (src/test, src/androidTest). Test sources are kept OUT of the package index so
// an import never resolves into a test directory — package-metrics and other
// analyses exclude test modules, and a main package can share its name with a
// test helper in the same package.
func isTestSource(relFile string) bool {
	p := filepath.ToSlash(relFile)
	return strings.Contains(p, "/src/test/") || strings.HasPrefix(p, "src/test/") ||
		strings.Contains(p, "/src/androidTest/") || strings.HasPrefix(p, "src/androidTest/")
}

// BuildPackageIndex maps a declared JVM package FQN (e.g. "com.example.app.api")
// to the on-disk directory that declares it, scanning every non-test .kt/.java
// file in files. It is the reliable, multi-module replacement for assuming a
// single global source root: an import of com.example.app.api.model.search.Foo
// resolves to whichever module's directory actually contains that package, not
// to whichever module happens to hold the most files.
//
// When two directories declare the same package, the main source set wins over
// Gradle build-variant source sets (src/debug, src/release, src/staging, flavors):
// an Android app's src/main and src/debug both declare the root application
// package, and imports of it must resolve to the primary (main) module, not a
// variant. Among equally-primary candidates the lexicographically smallest
// directory wins so the result is deterministic across runs.
func BuildPackageIndex(repoPath string, files []string) map[string]string {
	index := make(map[string]string)
	for _, relFile := range files {
		if !isJVMSource(relFile) || isTestSource(relFile) {
			continue
		}
		pkg, ok := readPackage(filepath.Join(repoPath, relFile))
		if !ok {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(relFile))
		if existing, seen := index[pkg]; !seen || preferDir(dir, existing) {
			index[pkg] = dir
		}
	}
	return index
}

// preferDir reports whether candidate should replace existing as the directory
// for a package declared in more than one place. A main source set (…/src/main/…)
// is preferred over a build-variant source set; otherwise the lexicographically
// smaller path wins (deterministic).
func preferDir(candidate, existing string) bool {
	cMain, eMain := isMainSourceSet(candidate), isMainSourceSet(existing)
	if cMain != eMain {
		return cMain // promote a main source set over a variant one
	}
	return candidate < existing
}

// isMainSourceSet reports whether a directory lives in a Gradle main source set
// (src/main/...), as opposed to a build-variant source set like src/debug.
func isMainSourceSet(dir string) bool {
	return strings.Contains(dir, "/src/main/") || strings.HasPrefix(dir, "src/main/")
}

// readPackage returns the package declared in a source file, reading only until
// the first package line (declarations always precede any type). ok is false when
// the file cannot be read or declares no package (the default/root package).
func readPackage(absFile string) (string, bool) {
	f, err := os.Open(absFile)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if m := packageRe.FindStringSubmatch(scanner.Text()); m != nil {
			return m[1], true
		}
	}
	return "", false
}

// ResolveImport maps a JVM import FQN to the directory of the package that holds
// it, using a package index from BuildPackageIndex. It walks from the longest
// dotted prefix down to the shortest, so that `de.foo.bar.Baz` resolves via the
// package `de.foo.bar` and a nested-type import `de.foo.bar.Outer.Inner` still
// resolves once a known package prefix is found. Any remaining segments (the
// type / nested-type name) are appended as path components so the caller's
// module-resolution still lands on the package directory and the produced fact
// name matches the "<dir>.<Type>" symbol convention. Returns ok=false when no
// prefix is a known package (i.e. an external dependency).
func ResolveImport(importPath string, index map[string]string) (string, bool) {
	p := strings.TrimSuffix(importPath, ".*")
	segs := strings.Split(p, ".")
	for i := len(segs); i > 0; i-- {
		pkg := strings.Join(segs[:i], ".")
		dir, ok := index[pkg]
		if !ok {
			continue
		}
		if rest := segs[i:]; len(rest) > 0 {
			return dir + "/" + strings.Join(rest, "/"), true
		}
		return dir, true
	}
	return "", false
}
