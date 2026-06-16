package javaextractor

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// JavaExtractor extracts architectural facts from Java source code using
// tree-sitter AST parsing (see java_ast.go for the walker and spring.go for
// Spring/JPA/Dubbo framework specialization).
type JavaExtractor struct{}

// New creates a new JavaExtractor.
func New() *JavaExtractor {
	return &JavaExtractor{}
}

func (e *JavaExtractor) Name() string {
	return "java"
}

// Detect returns true if the repository looks like a Java project: a Maven project
// (pom.xml), or any actual .java source file. A Gradle build file alone is not
// sufficient — Gradle is equally used by Kotlin, Android, and Groovy projects, so
// detecting on it would wrongly claim pure-Kotlin repos. Requiring real .java
// sources keeps the Java extractor off non-Java JVM projects.
func (e *JavaExtractor) Detect(repoPath string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repoPath, "pom.xml")); err == nil {
		return true, nil
	}
	return containsJavaSource(repoPath, 8), nil
}

// Extract parses Java files and emits architectural facts.
//
// Two passes: pass 1 walks each file's AST (extractFileAST) to emit declaration,
// import, route, storage and call-graph facts while indexing every declared type by
// its fully-qualified name. Pass 2 (canonicalizeTargets) rewrites type-reference
// edge targets (implements/instantiates/injects) and import targets from FQNs to
// canonical "<dir>.<Type>" / module-dir names so reverse traversal connects
// dependents. Module facts are emitted per directory.
func (e *JavaExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var allFacts []facts.Fact
	modules := make(map[string]bool)

	for _, relFile := range files {
		select {
		case <-ctx.Done():
			return allFacts, ctx.Err()
		default:
		}

		if !isJavaFile(relFile) {
			continue
		}

		absFile := filepath.Join(repoPath, relFile)
		src, err := os.ReadFile(absFile)
		if err != nil {
			log.Printf("[java-extractor] error reading %s: %v", relFile, err)
			continue
		}

		allFacts = append(allFacts, extractFileAST(src, relFile)...)
		modules[filepath.Dir(relFile)] = true
	}

	canonicalizeTargets(allFacts)
	resolveTableConstants(allFacts)

	for dir := range modules {
		allFacts = append(allFacts, facts.Fact{
			Kind: facts.KindModule,
			Name: dir,
			File: dir,
			Props: map[string]any{
				"language": "java",
			},
		})
	}

	return allFacts, nil
}

// canonicalizeTargets resolves FQN-based edge targets to canonical fact names.
//
//   - implements/instantiates/injects targets that match a declared type's FQN are
//     rewritten to that type's "<dir>.<Type>" fact name; unresolved targets (external
//     libraries) are left as written.
//   - import dependency facts whose target FQN resolves to a declared type — or whose
//     value names a known source package — are marked source="internal" and pointed at
//     the owning module dir.
func canonicalizeTargets(allFacts []facts.Fact) {
	typeIndex := make(map[string]string) // FQN -> "<dir>.<Type>" canonical name
	typeDir := make(map[string]string)   // FQN -> dir
	packageDir := make(map[string]string)
	for _, f := range allFacts {
		if f.Kind != facts.KindSymbol {
			continue
		}
		switch f.Props["symbol_kind"] {
		case facts.SymbolClass, facts.SymbolInterface, facts.SymbolEnum:
			fqn, _ := f.Props["fqn"].(string)
			if fqn == "" {
				continue
			}
			dir := f.File
			if i := strings.LastIndex(dir, "/"); i >= 0 {
				dir = dir[:i]
			} else {
				dir = "."
			}
			typeIndex[fqn] = f.Name
			typeDir[fqn] = dir
			if pkg := parentName(fqn); pkg != "" {
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
				if canon, ok := typeIndex[r.Target]; ok {
					r.Target = canon
				}
			}
		}
	}
}

func resolveImport(f *facts.Fact, typeDir, packageDir map[string]string) {
	imp, _ := f.Props["import"].(string)
	if imp == "" {
		return
	}
	var dir string
	var ok bool
	if dir, ok = typeDir[imp]; !ok {
		// Wildcard / package import (e.g. "com.example.foo").
		dir, ok = packageDir[imp]
	}
	if !ok {
		return // external dependency
	}
	f.Props["source"] = "internal"
	for j := range f.Relations {
		if f.Relations[j].Kind == facts.RelImports {
			f.Relations[j].Target = dir
		}
	}
}

// resolveTableConstants rewrites storage facts whose "table" prop names a string
// constant (e.g. @Table(name = ADMIN_SETTINGS_TABLE_NAME)) to that constant's
// literal value. Constants are indexed by simple name across all files, since the
// table-name constants typically live in a shared ModelConstants class. When the
// same simple name maps to conflicting values it is left unresolved (ambiguous).
func resolveTableConstants(allFacts []facts.Fact) {
	values := make(map[string]string)
	ambiguous := make(map[string]bool)
	for _, f := range allFacts {
		if f.Kind != facts.KindSymbol {
			continue
		}
		v, ok := f.Props["value"].(string)
		if !ok {
			continue
		}
		simple := f.Name
		if i := strings.LastIndex(simple, "."); i >= 0 {
			simple = simple[i+1:]
		}
		if existing, seen := values[simple]; seen && existing != v {
			ambiguous[simple] = true
			continue
		}
		values[simple] = v
	}

	for i := range allFacts {
		f := &allFacts[i]
		if f.Kind != facts.KindStorage {
			continue
		}
		tbl, ok := f.Props["table"].(string)
		if !ok {
			continue
		}
		if ambiguous[tbl] {
			continue
		}
		if v, ok := values[tbl]; ok {
			f.Props["table"] = v
			f.Props["table_constant"] = tbl
		}
	}
}

func parentName(fqn string) string {
	if i := strings.LastIndex(fqn, "."); i >= 0 {
		return fqn[:i]
	}
	return ""
}

func isJavaFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".java")
}

// containsJavaSource reports whether any .java file exists under root within
// maxDepth directory levels. It returns on the first match and skips hidden and
// common build/dependency directories so it stays cheap on large repos.
func containsJavaSource(root string, maxDepth int) bool {
	var search func(dir string, depth int) bool
	search = func(dir string, depth int) bool {
		if depth > maxDepth {
			return false
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() {
				if strings.HasPrefix(name, ".") || name == "build" ||
					name == "target" || name == "node_modules" {
					continue
				}
				if search(filepath.Join(dir, name), depth+1) {
					return true
				}
			} else if isJavaFile(name) {
				return true
			}
		}
		return false
	}
	return search(root, 0)
}
