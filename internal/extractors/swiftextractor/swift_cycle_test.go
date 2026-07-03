package swiftextractor

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// TestExtract_NoFalseCrossTargetCycle guards the fix for the false Swift module
// cycle. Two framework targets both declare a top-level type with the same short
// name ("Event") — legal in Swift because each module is its own namespace. A
// file in the low-level target (Core) references its OWN Event. Previously the
// type-reference pass resolved the bare name "Event" through a global,
// last-writer-wins index and could point it at the OTHER target (Feature),
// fabricating an impossible back-edge Core -> Feature that, together with the
// declared Feature -> Core edge, formed a module cycle. Because Core's files
// belong to a resolved XcodeGen target, module dependencies now come from
// imports + the declared target graph only; the type-reference pass is skipped.
func TestExtract_NoFalseCrossTargetCycle(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, repo, "project.yml", `
name: app
targets:
  Core:
    type: framework
    sources:
      - Sources/Core
  Feature:
    type: framework
    sources:
      - Sources/Feature
    dependencies:
      - target: Core
`)
	files := []string{}
	add := func(rel, content string) {
		mustWrite(t, repo, rel, content)
		files = append(files, rel)
	}
	// Core owns a top-level Event and references it — a self-reference that must
	// never resolve to Feature's Event.
	add("Sources/Core/Event.swift", "public struct Event {}\n")
	add("Sources/Core/CoreUser.swift", "public struct CoreUser { let e: Event? = nil }\n")
	// Feature owns its own Event and legitimately depends on Core (import).
	add("Sources/Feature/Event.swift", "public struct Event {}\n")
	add("Sources/Feature/View.swift", "import Core\n\npublic struct FeatureView { let e: Event? = nil }\n")

	ff, err := New().Extract(context.Background(), repo, files)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// The real declared/import edge survives.
	if !hasDepEdge(ff, "Sources/Feature", "Sources/Core", "internal") {
		t.Error("missing genuine Feature -> Core dependency")
	}
	// The fabricated back-edge must NOT exist (it would create a cycle).
	if hasDepEdge(ff, "Sources/Core", "Sources/Feature", "internal") {
		t.Error("fabricated false back-edge Core -> Feature (collision-driven cycle not fixed)")
	}
	if hasModuleCycle(ff) {
		t.Error("module dependency graph contains a cycle")
	}
}

// TestExtract_LooseProject_AmbiguousTypeSkipped covers the loose-Swift path (no
// SPM/XcodeGen target graph, so files fall back to leaf-directory modules and the
// type-reference pass still runs). An ambiguous short name (defined in >1 module)
// must not fabricate an edge, while a UNIQUE cross-module type reference must
// still produce one — so the collision guard fixes false positives without
// suppressing genuine dependencies.
func TestExtract_LooseProject_AmbiguousTypeSkipped(t *testing.T) {
	repo := t.TempDir()
	files := []string{}
	add := func(rel, content string) {
		mustWrite(t, repo, rel, content)
		files = append(files, rel)
	}
	// Event is defined in both ModB and ModC -> ambiguous. Modules nest under
	// Sources/ so their identity is a path (matching real Swift layouts); a
	// top-level single-segment identity is classified as an external import name.
	add("Sources/ModB/B.swift", "public struct Event {}\n")
	add("Sources/ModC/C.swift", "public struct Event {}\npublic struct Widget {}\n")
	// ModA references the ambiguous Event and the unique Widget (ModC only).
	add("Sources/ModA/A.swift", "public struct A { let e: Event? = nil; let w: Widget? = nil }\n")

	ff, err := New().Extract(context.Background(), repo, files)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Unique reference resolves -> genuine edge kept.
	if !hasDepEdge(ff, "Sources/ModA", "Sources/ModC", "internal") {
		t.Error("missing genuine ModA -> ModC edge (unique Widget reference)")
	}
	// Ambiguous reference must not fabricate an edge to the arbitrary index winner.
	if hasDepEdge(ff, "Sources/ModA", "Sources/ModB", "internal") {
		t.Error("fabricated ModA -> ModB edge from ambiguous type name Event")
	}
}

// hasModuleCycle reports whether the module→module import edges in ff contain a
// cycle. It mirrors the explainer's graph model (source = the dependency fact's
// directory, target = the RelImports target), restricted to internal edges whose
// endpoints are both known modules.
func hasModuleCycle(ff []facts.Fact) bool {
	modules := map[string]bool{}
	for _, f := range ff {
		if f.Kind == facts.KindModule {
			modules[f.Name] = true
		}
	}
	adj := map[string][]string{}
	for _, f := range ff {
		if f.Kind != facts.KindDependency {
			continue
		}
		from := slashDir(f.File)
		if !modules[from] {
			continue
		}
		for _, r := range f.Relations {
			if r.Kind == facts.RelImports && modules[r.Target] {
				adj[from] = append(adj[from], r.Target)
			}
		}
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var dfs func(string) bool
	dfs = func(n string) bool {
		color[n] = gray
		for _, m := range adj[n] {
			switch color[m] {
			case gray:
				return true
			case white:
				if dfs(m) {
					return true
				}
			}
		}
		color[n] = black
		return false
	}
	for m := range modules {
		if color[m] == white && dfs(m) {
			return true
		}
	}
	return false
}
