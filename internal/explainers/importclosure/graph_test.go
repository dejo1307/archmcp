package importclosure

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// dep builds an import dependency fact as the Python extractor emits one.
func dep(file, target string, deferred bool) facts.Fact {
	p := map[string]any{"language": "python", facts.PropSource: facts.DepSourceInternal}
	if deferred {
		p["deferred"] = true
	}
	return facts.Fact{
		Kind:      facts.KindDependency,
		Name:      file + " -> " + target,
		File:      file,
		Props:     p,
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: target}},
	}
}

func sym(file string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: file + ".x", File: file, Props: map[string]any{"language": "python"}}
}

func build(ff ...facts.Fact) *Graph {
	s := facts.NewStore()
	s.Add(ff...)
	return Build(s)
}

func TestBuild_ResolvesModulesAndPackages(t *testing.T) {
	g := build(
		sym("pkg/__init__.py"), sym("pkg/api.py"), sym("pkg/db.py"), sym("pkg/sub/__init__.py"),
		dep("pkg/__init__.py", "pkg/api", false),
		dep("pkg/api.py", "pkg/db", false),
		dep("pkg/api.py", "pkg/sub", false), // a package target loads its __init__.py
	)
	c := g.Closure("pkg/__init__.py")
	for file, want := range map[string]int{
		"pkg/__init__.py": 0, "pkg/api.py": 1, "pkg/db.py": 2, "pkg/sub/__init__.py": 2,
	} {
		if got, ok := c[file]; !ok || got != want {
			t.Errorf("closure[%s] = %d (present=%v), want %d", file, got, ok, want)
		}
	}
}

// TestBuild_DeferredImportsAreNotOnThePath is the reason the prop exists: a lazy
// import is a dependency, but it is not something `import pkg` pays for.
func TestBuild_DeferredImportsAreNotOnThePath(t *testing.T) {
	g := build(
		sym("pkg/__init__.py"), sym("pkg/api.py"), sym("pkg/heavy.py"),
		dep("pkg/__init__.py", "pkg/api", false),
		dep("pkg/api.py", "pkg/heavy", true), // function-local
	)
	c := g.Closure("pkg/__init__.py")
	if _, reached := c["pkg/heavy.py"]; reached {
		t.Error("a deferred import put pkg/heavy.py on the import path")
	}
	if len(c) != 2 {
		t.Errorf("closure has %d files, want 2", len(c))
	}
}

func TestBuild_SkipsExternalAndUnresolved(t *testing.T) {
	ext := dep("pkg/api.py", "requests", false)
	ext.Props[facts.PropSource] = facts.DepSourceExternal
	g := build(
		sym("pkg/api.py"), ext,
		dep("pkg/api.py", "pkg.unresolved.dotted", false), // names no file
	)
	if n := len(g.Edges["pkg/api.py"]); n != 0 {
		t.Errorf("pkg/api.py has %d edges, want 0 (external and unresolved targets name no file)", n)
	}
}

// TestBuild_CyclesTerminate — Python import cycles are real and common; the walk
// must not spin on one.
func TestBuild_CyclesTerminate(t *testing.T) {
	g := build(
		sym("pkg/a.py"), sym("pkg/b.py"),
		dep("pkg/a.py", "pkg/b", false),
		dep("pkg/b.py", "pkg/a", false),
	)
	c := g.Closure("pkg/a.py")
	if len(c) != 2 || c["pkg/b.py"] != 1 {
		t.Errorf("closure = %v, want both files with b at depth 1", c)
	}
}

// TestBuild_ClosureIsShortestPath pins that depth is hop count, not discovery order.
func TestBuild_ClosureIsShortestPath(t *testing.T) {
	g := build(
		sym("pkg/a.py"), sym("pkg/b.py"), sym("pkg/c.py"), sym("pkg/d.py"),
		dep("pkg/a.py", "pkg/b", false),
		dep("pkg/a.py", "pkg/d", false),
		dep("pkg/b.py", "pkg/c", false),
		dep("pkg/c.py", "pkg/d", false),
	)
	if got := g.Closure("pkg/a.py")["pkg/d.py"]; got != 1 {
		t.Errorf("depth of pkg/d.py = %d, want 1 (direct edge, not the 3-hop route)", got)
	}
}
