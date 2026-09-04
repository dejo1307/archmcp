// Package importclosure answers what a Python entry point actually loads when it
// is imported.
//
// It exists because the module graph the other explainers read cannot answer that
// question. That graph is deliberately coarse: common.BuildModuleGraph resolves BOTH
// endpoints of every edge up to their enclosing module directory, because coupling is
// a property of packages, not of files. Import cost is the opposite — it is a property
// of files, and of the exact order Python walks them, so an edge from
// "pkg/api" to "pkg/shared" cannot say which of the twenty modules under pkg/shared
// were paid for.
//
// The two graphs are therefore kept separate on purpose, and this one is read by no
// coupling explainer. That is not tidiness: a package node accumulates an edge from
// every file beneath it, so folding import-time reachability into the module graph
// would make each namespace package the most-depended-upon node in the repository
// and turn every one of them into a god-class finding.
package importclosure

import (
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/explainers/common"
	"github.com/enola-labs/enola/internal/facts"
)

// Graph is the import-time adjacency between FILES: Edges[f] lists the files that
// importing f loads, in the sense that Python executes them before f finishes
// importing. Imports that do not run at module-import time — a function-local import,
// a TYPE_CHECKING block — carry Props["deferred"] and contribute no edge, since the
// whole point is to distinguish what an import costs from what a call might later.
type Graph struct {
	Edges map[string][]string
	// Files is every Python file the snapshot knows, so a caller can tell "not
	// reachable" from "not in this repository".
	Files map[string]bool
}

// Build derives the import-time file graph from a snapshot.
//
// Nothing is stored for it: the edges are a projection of the dependency facts the
// Python extractor already emits, so this adds no facts, no cache version, and no
// weight to a snapshot that does not ask the question.
func Build(store *facts.Store) *Graph {
	g := &Graph{Edges: map[string][]string{}, Files: map[string]bool{}}

	for _, f := range store.FactsRef() {
		if strings.HasSuffix(f.File, ".py") {
			g.Files[f.File] = true
		}
	}

	seen := map[[2]string]bool{}
	for _, dep := range store.ByKind(facts.KindDependency) {
		if lang, _ := dep.Props["language"].(string); lang != "python" {
			continue
		}
		// Only imports that actually run when the module is imported.
		if deferred, _ := dep.Props["deferred"].(bool); deferred {
			continue
		}
		// An external or stdlib target names no file in this repository.
		if src, _ := dep.Props[facts.PropSource].(string); src != facts.DepSourceInternal {
			continue
		}
		for _, rel := range dep.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}
			target := rel.Target
			if strings.HasPrefix(target, ".") {
				// resolveImports rewrites relative targets at extraction time, but a
				// `from . import x` resolves to the importer's own directory and is
				// left as written.
				target = common.ResolveRelativeImport(fileDir(dep.File), target)
			}
			to := g.resolveFile(target)
			if to == "" || to == dep.File {
				continue
			}
			key := [2]string{dep.File, to}
			if seen[key] {
				continue
			}
			seen[key] = true
			g.Edges[dep.File] = append(g.Edges[dep.File], to)
		}
	}
	for f := range g.Edges {
		sort.Strings(g.Edges[f])
	}
	return g
}

// resolveFile maps an import target to the file Python would execute for it: a
// module target names that module's file, a package target names its __init__.py.
// A target naming neither (an unresolved dotted path) yields "" rather than a guess.
func (g *Graph) resolveFile(target string) string {
	if target == "" {
		return ""
	}
	if f := target + ".py"; g.Files[f] {
		return f
	}
	if f := target + "/__init__.py"; g.Files[f] {
		return f
	}
	return ""
}

// Closure returns every file reachable from entry by import-time edges, mapped to
// the fewest hops it takes to reach it. The entry itself is at depth 0.
func (g *Graph) Closure(entry string) map[string]int {
	depth := map[string]int{entry: 0}
	queue := []string{entry}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range g.Edges[cur] {
			if _, ok := depth[next]; ok {
				continue
			}
			depth[next] = depth[cur] + 1
			queue = append(queue, next)
		}
	}
	return depth
}

// fileDir returns the directory part of a slash path, or "" for a bare filename.
func fileDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}
