package swiftextractor

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func importDep(file, target string) facts.Fact {
	return facts.Fact{
		Kind:      facts.KindDependency,
		Name:      file + " -> " + target,
		File:      file,
		Props:     map[string]any{"language": "swift"},
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: target}},
	}
}

func importTargetOf(f facts.Fact) string {
	for _, r := range f.Relations {
		if r.Kind == facts.RelImports {
			return r.Target
		}
	}
	return ""
}

func sourceOf(f facts.Fact) string {
	s, _ := f.Props["source"].(string)
	return s
}

func TestResolveImports_SwiftBareNames(t *testing.T) {
	ff := []facts.Fact{
		{Kind: facts.KindModule, Name: "Packages/Mods/Sources/AppComposition",
			Props: map[string]any{"language": "swift", "spm_target": "AppComposition"}},
		{Kind: facts.KindModule, Name: "App/Screens",
			Props: map[string]any{"language": "swift"}},
		importDep("App/Screens/Home.swift", "AppComposition"), // internal SPM target
		importDep("App/Screens/Home.swift", "Foundation"),     // system framework
		importDep("App/Screens/Home.swift", "SwiftUI"),        // system framework
		importDep("App/Screens/Home.swift", "Alamofire"),      // unknown third-party
	}
	resolveImports(ff)

	cases := map[string]struct{ target, source string }{
		"AppComposition": {"Packages/Mods/Sources/AppComposition", "internal"},
		"Foundation":     {"Foundation", "stdlib"},
		"SwiftUI":        {"SwiftUI", "stdlib"},
		"Alamofire":      {"Alamofire", "external"},
	}
	for _, f := range ff {
		if f.Kind != facts.KindDependency {
			continue
		}
		raw := f.Name[len("App/Screens/Home.swift -> "):]
		want, ok := cases[raw]
		if !ok {
			continue
		}
		if got := importTargetOf(f); got != want.target {
			t.Errorf("import %q target = %q, want %q", raw, got, want.target)
		}
		if got := sourceOf(f); got != want.source {
			t.Errorf("import %q source = %q, want %q", raw, got, want.source)
		}
	}
}

func TestResolveImports_XcodeTargetNames(t *testing.T) {
	// A bare `import <XcodeGenTarget>` must resolve to that target's module dir,
	// mirroring SPM target resolution.
	ff := []facts.Fact{
		{Kind: facts.KindModule, Name: "Sources/Core",
			Props: map[string]any{"language": "swift", "xcode_target": "Core"}},
		{Kind: facts.KindModule, Name: "Sources/Chat",
			Props: map[string]any{"language": "swift", "xcode_target": "Chat"}},
		importDep("Sources/Chat/Chat.swift", "Core"),  // internal XcodeGen target
		importDep("Sources/Chat/Chat.swift", "UIKit"), // system framework
	}
	resolveImports(ff)

	for _, f := range ff {
		if f.Kind != facts.KindDependency {
			continue
		}
		raw := f.Name[len("Sources/Chat/Chat.swift -> "):]
		switch raw {
		case "Core":
			if got := importTargetOf(f); got != "Sources/Core" {
				t.Errorf("import Core target = %q, want Sources/Core", got)
			}
			if got := sourceOf(f); got != "internal" {
				t.Errorf("import Core source = %q, want internal", got)
			}
		case "UIKit":
			if got := sourceOf(f); got != "stdlib" {
				t.Errorf("import UIKit source = %q, want stdlib", got)
			}
		}
	}
}

func TestResolveImports_SwiftPathTargetsKept(t *testing.T) {
	// A target that is already a path (from the manifest or type-reference pass)
	// must be left intact and marked internal.
	ff := []facts.Fact{
		{Kind: facts.KindModule, Name: "Pkg/Sources/A", Props: map[string]any{"language": "swift", "spm_target": "A"}},
		{Kind: facts.KindDependency, Name: "Pkg/Sources/B -> Pkg/Sources/A", File: "Pkg/Sources/B/x.swift",
			Props:     map[string]any{"language": "swift", "internal": true},
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "Pkg/Sources/A"}}},
	}
	resolveImports(ff)
	dep := ff[1]
	if importTargetOf(dep) != "Pkg/Sources/A" {
		t.Errorf("path target should be unchanged, got %q", importTargetOf(dep))
	}
	if sourceOf(dep) != "internal" {
		t.Errorf("pass-2 path dep should be source=internal, got %q", sourceOf(dep))
	}
}

// TestExtract_SwiftImportResolvesInternal is the end-to-end guard over a real SPM
// manifest repo: a bare `import <Target>` must resolve to that target's module dir.
func TestExtract_SwiftImportResolvesInternal(t *testing.T) {
	manifest := `// swift-tools-version:5.9
import PackageDescription
let package = Package(
    name: "Mods",
    targets: [
        .target(name: "Core"),
        .target(name: "Feature", dependencies: ["Core"]),
    ]
)
`
	repo, files := writeManifestRepo(t, manifest, []string{"Core", "Feature"})

	// Add a Feature source file that imports Core and Foundation.
	featureFile := "Packages/Mods/Sources/Feature/View.swift"
	mustWrite(t, repo, featureFile, "import Foundation\nimport Core\n\npublic struct View {}\n")
	files = append(files, featureFile)

	ff, err := New().Extract(context.Background(), repo, files)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	moduleNames := map[string]bool{}
	for _, f := range ff {
		if f.Kind == facts.KindModule {
			moduleNames[f.Name] = true
		}
	}

	var importCoreResolved bool
	for _, f := range ff {
		if f.Kind != facts.KindDependency {
			continue
		}
		for _, r := range f.Relations {
			if r.Kind == facts.RelImports && r.Target == "Packages/Mods/Sources/Core" && moduleNames[r.Target] {
				if sourceOf(f) == "internal" {
					importCoreResolved = true
				}
			}
		}
	}
	if !importCoreResolved {
		t.Errorf("`import Core` should resolve to module Packages/Mods/Sources/Core (source=internal); modules: %v", moduleNames)
	}
}
