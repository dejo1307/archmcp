package scalaextractor

import (
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// stdlibPrefixes are the package roots that ship with the language or the JVM.
// `scala.*` is the Scala standard library; the `java.*`/`javax.*` roots are the
// JDK, which a Scala file imports as freely as a Java one does.
var stdlibPrefixes = []string{"scala.", "java.", "javax.", "jdk.", "sun.", "com.sun."}

// depSource classifies an import before the whole fact set exists. Only stdlib is
// decidable from the string alone; everything else starts as external and is
// promoted to internal by canonicalizeTargets, which can see what this repository
// actually declares.
func depSource(fqn string) string {
	for _, p := range stdlibPrefixes {
		if strings.HasPrefix(fqn, p) {
			return "stdlib"
		}
	}
	if fqn == "scala" || fqn == "java" {
		return "stdlib"
	}
	return "external"
}

// canonicalizeTargets is the second pass: it rewrites edge targets from the fully
// qualified names each file could see to the canonical "<dir>.<Type>" fact names,
// and reclassifies imports that resolve inside this repository as internal.
//
// It exists because a reference and its declaration are almost never in the same
// file. `extends Base` resolves, through the referencing file's imports, to
// `com.example.core.Base` — a string that matches no fact until this pass maps it
// onto the name the declaring file chose. Targets that resolve to nothing are left
// exactly as written: an unresolved edge to a readable external name is useful,
// and a fabricated internal one is not.
//
// crossLangIndex seeds the package map with directories learned from .java and .kt
// sources, so a Scala import of a Java type in a sibling module resolves rather
// than being written off as a third-party dependency. filePkg maps each source file
// to the package it declares, which is what lets a BARE reference be resolved here
// against the merged fact set instead of being guessed at per file.
func canonicalizeTargets(allFacts []facts.Fact, crossLangIndex, filePkg map[string]string) {
	typeIndex := make(map[string]string) // FQN -> "<dir>.<Type>"
	typeDir := make(map[string]string)   // FQN -> declaring directory
	packageDir := make(map[string]string)

	for pkg, dir := range crossLangIndex {
		packageDir[pkg] = dir
	}

	for i := range allFacts {
		f := &allFacts[i]
		if f.Kind != facts.KindSymbol {
			continue
		}
		switch f.Props["symbol_kind"] {
		case facts.SymbolClass, facts.SymbolInterface, facts.SymbolEnum, facts.SymbolType:
		default:
			continue
		}
		fqn, _ := f.Props["fqn"].(string)
		if fqn == "" {
			continue
		}
		dir := f.File
		if j := strings.LastIndex(dir, "/"); j >= 0 {
			dir = dir[:j]
		} else {
			dir = "."
		}
		// A name-keyed graph merges same-named facts, so two declarations of one
		// FQN (a companion object beside its class — the single most common shape
		// in Scala) must not fight over the index. First writer wins, and because
		// facts are merged in file order that is deterministic.
		if _, seen := typeIndex[fqn]; !seen {
			typeIndex[fqn] = f.Name
			typeDir[fqn] = dir
		}
		if pkg := parentName(fqn); pkg != "" {
			if _, seen := packageDir[pkg]; !seen {
				packageDir[pkg] = dir
			}
		}
	}

	for i := range allFacts {
		f := &allFacts[i]
		if f.Kind == facts.KindDependency {
			resolveImport(f, typeDir, packageDir)
			continue
		}
		for j := range f.Relations {
			r := &f.Relations[j]
			switch r.Kind {
			case facts.RelImplements, facts.RelInstantiates, facts.RelInjects:
				r.Target = resolveTypeTarget(r.Target, filePkg[f.File], typeIndex, packageDir)
			}
		}
	}
}

// resolveTypeTarget maps one edge target onto a canonical fact name, or leaves it
// honestly unresolved.
//
// Four outcomes, in order:
//
//  1. A BARE name that this file's own package declares. The walker cannot decide
//     this — Scala auto-imports scala.*, java.lang.* and scala.Predef.*, so a bare
//     name is as likely to be stdlib as same-package — but the merged fact set can,
//     because it either contains `<pkg>.<Name>` or it does not. This is the check
//     that keeps `Ordering` unresolved while resolving a genuine sibling type.
//  2. A qualified FQN naming a type this repository declares: rewrite to its name.
//  3. A qualified FQN whose PACKAGE we know but whose type is not in our index — a
//     Java or Kotlin type in a sibling module. Those facts belong to a different
//     extractor and are not in this slice, so they cannot be looked up; but the JVM
//     extractors all name a type `<dir>.<Type>` and the package index knows the
//     directory, so the canonical name is derivable. This is what connects a Scala
//     service to the Java class it extends instead of leaving the graph split by
//     language. It is sound because the reference reached us through an explicit
//     import of a package this repository declares.
//  4. Anything else is external and is left exactly as written.
func resolveTypeTarget(target, filePackage string, typeIndex, packageDir map[string]string) string {
	if target == "" {
		return target
	}
	if !strings.Contains(target, ".") {
		if filePackage != "" {
			if canon, ok := typeIndex[filePackage+"."+target]; ok {
				return canon
			}
		}
		return target // an auto-imported or third-party type; honestly unresolved
	}
	if canon, ok := typeIndex[target]; ok {
		return canon
	}
	pkg, name := parentName(target), lastSegment(target)
	// Gated on the name looking like a type, so a member reference
	// (`Registry.cache`) is never rewritten into a type nobody declared.
	if dir, ok := packageDir[pkg]; ok && isTypeName(name) {
		return dir + "." + name
	}
	return target
}

func lastSegment(fqn string) string {
	if i := strings.LastIndex(fqn, "."); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

// isTypeName reports whether a segment looks like a type rather than a member,
// by JVM naming convention (types are capitalized).
func isTypeName(s string) bool {
	if s == "" {
		return false
	}
	r := rune(s[0])
	return r >= 'A' && r <= 'Z'
}

// resolveImport promotes a dependency to internal when its target names something
// this repository declares, and repoints the edge at the declaring directory so
// module-level coupling is visible.
func resolveImport(f *facts.Fact, typeDir, packageDir map[string]string) {
	imp, _ := f.Props["import"].(string)
	if imp == "" {
		return
	}
	// A stdlib classification is already correct and must not be overwritten: a
	// repository that declares its own `scala.meta` package would otherwise
	// reclassify the language's own library as its internal code.
	if f.Props[facts.PropSource] == "stdlib" {
		return
	}

	dir, ok := typeDir[imp]
	if !ok {
		dir, ok = packageDir[imp]
	}
	if !ok {
		// A member import (`import Registry.cache`) or a nested type names its
		// declaring type rather than a package; try the parent. Skipped for a
		// wildcard, whose import string is already the package — walking to its
		// parent would resolve the wrong thing.
		if wc, _ := f.Props["wildcard"].(bool); !wc {
			if parent := parentName(imp); parent != "" {
				if dir, ok = typeDir[parent]; !ok {
					dir, ok = packageDir[parent]
				}
			}
		}
	}
	if !ok {
		return // genuinely external
	}

	f.Props[facts.PropSource] = "internal"
	for j := range f.Relations {
		if f.Relations[j].Kind == facts.RelImports {
			f.Relations[j].Target = dir
		}
	}
}

func parentName(fqn string) string {
	if i := strings.LastIndex(fqn, "."); i >= 0 {
		return fqn[:i]
	}
	return ""
}
