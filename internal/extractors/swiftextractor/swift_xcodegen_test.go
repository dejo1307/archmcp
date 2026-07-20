package swiftextractor

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// writeXcodeGenRepo builds a temp XcodeGen project: a project.yml with an
// include: file and a packages block (one local, one remote), a local SPM
// package, and a stub source file per target directory (some nested to exercise
// the leaf-directory over-split the target-level resolver fixes).
func writeXcodeGenRepo(t *testing.T) (string, []string) {
	t.Helper()
	repo := t.TempDir()

	mustWrite(t, repo, "project.yml", `
include:
  - path: XCodegen/Extra.yml
name: app
packages:
  LocalDep:
    path: LocalPackages/LocalDep
  RemoteDep:
    url: https://example.com/remote.git
    from: 1.0.0
targets:
  App:
    type: application
    sources:
      - path: App
        excludes:
          - "Info.plist"
    dependencies:
      - target: Core
      - target: UI
      - package: LocalDep
        product: LocalDep
      - package: RemoteDep
        product: RemoteDep
  Core:
    type: framework
    sources:
      - Sources/Core
  UI:
    type: framework
    sources:
      - path: Sources/UI
        excludes:
          - "3rdParty/*.md"
    dependencies:
      - target: Core
`)
	mustWrite(t, repo, "XCodegen/Extra.yml", `
targets:
  Ext:
    type: app-extension
    sources:
      - ShareExt
    dependencies:
      - target: Core
`)
	mustWrite(t, repo, "LocalPackages/LocalDep/Package.swift", `// swift-tools-version:5.5
import PackageDescription
let package = Package(name: "LocalDep", targets: [.target(name: "LocalDep")])
`)

	files := []string{}
	add := func(rel, content string) {
		mustWrite(t, repo, rel, content)
		files = append(files, rel)
	}
	files = append(files, "LocalPackages/LocalDep/Package.swift")
	add("App/Main.swift", "import Core\nimport RemoteDep\n\npublic struct AppRoot {}\n")
	add("Sources/Core/Deep/Thing.swift", "public struct Thing { public let n: Int = 0 }\n")
	add("Sources/Core/Core.swift", "public struct CoreEntry {}\n")
	add("Sources/UI/View.swift", "import Core\n\npublic struct MyView { let t: Thing? = nil }\n")
	add("ShareExt/S.swift", "public struct ShareRoot {}\n")
	add("LocalPackages/LocalDep/Sources/LocalDep/Lib.swift", "public struct Lib {}\n")
	return repo, files
}

func TestParseXcodeGenProject(t *testing.T) {
	repo, _ := writeXcodeGenRepo(t)
	xp, err := parseXcodeGenProject(repo, "project.yml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if xp == nil {
		t.Fatal("expected a project, got nil")
	}
	// Ext comes from the include: file, merged into the flat target namespace.
	for _, name := range []string{"App", "Core", "UI", "Ext"} {
		if _, ok := xp.targets[name]; !ok {
			t.Errorf("missing target %q", name)
		}
	}
	if got := xp.targets["App"].Type; got != "application" {
		t.Errorf("App type = %q, want application", got)
	}
	if !xp.packages["LocalDep"].isLocal() {
		t.Error("LocalDep should be a local package")
	}
	if xp.packages["RemoteDep"].isLocal() {
		t.Error("RemoteDep should be remote (not local)")
	}
}

func TestModuleResolver_ModuleFor(t *testing.T) {
	repo, _ := writeXcodeGenRepo(t)
	xp, err := parseXcodeGenProject(repo, "project.yml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	spmRoots := map[string]string{"LocalDep": "LocalPackages/LocalDep/Sources/LocalDep"}
	r := buildModuleResolver(xp, spmRoots)

	cases := []struct {
		file   string
		want   string
		wantOK bool
	}{
		{"Sources/Core/Deep/Thing.swift", "Sources/Core", true}, // nested file → target root, not leaf dir
		{"Sources/Core/Core.swift", "Sources/Core", true},
		{"App/Main.swift", "App", true},
		{"ShareExt/S.swift", "ShareExt", true}, // from the include: file
		{"LocalPackages/LocalDep/Sources/LocalDep/Lib.swift", "LocalPackages/LocalDep/Sources/LocalDep", true},
		{"Sources/UI/3rdParty/README.md", "", false}, // excluded by "3rdParty/*.md"
		{"Scripts/gen.swift", "", false},             // not covered by any target → leaf-dir fallback
	}
	for _, c := range cases {
		got, ok := r.moduleFor(c.file)
		if got != c.want || ok != c.wantOK {
			t.Errorf("moduleFor(%q) = (%q, %v), want (%q, %v)", c.file, got, ok, c.want, c.wantOK)
		}
	}
}

// TestModuleResolver_SharedDirAmbiguous verifies that a path claimed by two
// equal-priority targets is treated as ambiguous (ok=false → leaf-dir fallback),
// so shared helper code is not misattributed.
func TestModuleResolver_SharedDirAmbiguous(t *testing.T) {
	r := &moduleResolver{
		identities:     map[string]bool{"Sources/A": true, "Sources/B": true},
		targetIdentity: map[string]string{"A": "Sources/A", "B": "Sources/B"},
		entries: []moduleEntry{
			{prefix: "Sources/A", identity: "Sources/A", priority: 1},
			{prefix: "Sources/B", identity: "Sources/B", priority: 1},
			{prefix: "Shared", identity: "Sources/A", priority: 1},
			{prefix: "Shared", identity: "Sources/B", priority: 1},
		},
	}
	if got, ok := r.moduleFor("Shared/Helper.swift"); ok {
		t.Errorf("shared dir should be ambiguous, got (%q, %v)", got, ok)
	}
	if got, ok := r.moduleFor("Sources/A/X.swift"); !ok || got != "Sources/A" {
		t.Errorf("unambiguous file misresolved: (%q, %v)", got, ok)
	}
}

// TestModuleResolver_SharedSourceRootCollapses verifies that several product
// targets rooted at the same source directory (app + preview host + test host)
// collapse to a single module identity owned by the first target by sorted name,
// emitting no duplicate module facts.
func TestModuleResolver_SharedSourceRootCollapses(t *testing.T) {
	xp := &xcodeProject{
		projectDir: ".",
		targets: map[string]xcodeTarget{
			"App":         {Type: "application", Sources: []xcodeSource{{path: "App"}}, Deps: []xcodeDep{{Target: "Core"}}},
			"AppPreview":  {Type: "application", Sources: []xcodeSource{{path: "App"}}},
			"AppUnitTest": {Type: "application", Sources: []xcodeSource{{path: "App"}}},
			"Core":        {Type: "framework", Sources: []xcodeSource{{path: "Sources/Core"}}},
		},
		packages: map[string]xcodePackage{},
	}
	r := buildModuleResolver(xp, nil)

	// App is an application target → subdivided (per-directory packages), so it owns
	// its root identity but is deliberately kept OUT of targetIdentity (it is never an
	// import unit and emits no flat module fact — its per-directory modules and imports
	// carry its structure).
	if !r.subdivided["App"] {
		t.Error(`subdivided["App"] = false, want true (application target)`)
	}
	if !r.identities["App"] {
		t.Error(`identities["App"] = false, want true`)
	}
	if _, ok := r.targetIdentity["App"]; ok {
		t.Error("subdivided App must not be in targetIdentity")
	}
	// The preview/test-host shadow targets sharing the "App" root collapse away.
	for _, shadow := range []string{"AppPreview", "AppUnitTest"} {
		if _, ok := r.targetIdentity[shadow]; ok {
			t.Errorf("shadow target %q should not own an identity", shadow)
		}
	}

	// emitXcodeGenFacts emits the whole Core framework module, but NOT a flat App
	// module (App is subdivided into per-directory modules elsewhere).
	ff := emitXcodeGenFacts(r, xp, nil, map[string]string{"Sources/Core": "Sources/Core/Core.swift"})
	coreModules, appModules := 0, 0
	for _, f := range ff {
		if f.Kind != facts.KindModule {
			continue
		}
		switch f.Name {
		case "Sources/Core":
			coreModules++
		case "App":
			appModules++
		}
	}
	if coreModules != 1 {
		t.Errorf("emitted %d Sources/Core module facts, want exactly 1", coreModules)
	}
	if appModules != 0 {
		t.Errorf("emitted %d flat App module facts, want 0 (App is subdivided)", appModules)
	}
}

// TestExtract_XcodeGenTargetModules is the end-to-end guard: one module per
// product target (not per leaf directory), correct target→target edges, and
// imports resolved to the owning target module.
func TestExtract_XcodeGenTargetModules(t *testing.T) {
	repo, files := writeXcodeGenRepo(t)
	ff, err := New().Extract(context.Background(), repo, files)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	modules := map[string]facts.Fact{}
	for _, f := range ff {
		if f.Kind == facts.KindModule {
			modules[f.Name] = f
		}
	}

	// Core is one module at its source root, tagged with its XcodeGen identity.
	core, ok := modules["Sources/Core"]
	if !ok {
		t.Fatalf("missing module Sources/Core; modules: %v", keysOf(modules))
	}
	if core.Props["xcode_target"] != "Core" || core.Props["xcode_type"] != "framework" {
		t.Errorf("Core module props = %v", core.Props)
	}
	// The nested directory must NOT be its own module (the over-split we fixed).
	if _, bad := modules["Sources/Core/Deep"]; bad {
		t.Error("Sources/Core/Deep leaked as a separate module")
	}
	// Other product targets and the local SPM package are modules too.
	for _, want := range []string{"App", "Sources/UI", "ShareExt", "LocalPackages/LocalDep/Sources/LocalDep"} {
		if _, ok := modules[want]; !ok {
			t.Errorf("missing module %q; modules: %v", want, keysOf(modules))
		}
	}

	// Declared App→Core edge (internal), and App→RemoteDep (external).
	if !hasDepEdge(ff, "App", "Sources/Core", "internal") {
		t.Error("missing internal App -> Sources/Core dependency")
	}
	if !hasDepEdge(ff, "App", "RemoteDep", "external") {
		t.Error("missing external App -> RemoteDep dependency")
	}

	// `import Core` from UI resolves to the Core target module.
	if !importResolvesTo(ff, "Sources/Core") {
		t.Error("`import Core` did not resolve to module Sources/Core")
	}
}

// TestTargetPriorityAndRoles guards the priority tiers (product > test > other)
// and the role classification helpers.
func TestTargetPriorityAndRoles(t *testing.T) {
	if targetPriority("application") <= targetPriority("app-extension") ||
		targetPriority("app-extension") <= targetPriority("framework") ||
		targetPriority("framework") <= targetPriority("bundle.unit-test") ||
		targetPriority("bundle.unit-test") <= targetPriority("aggregate") {
		t.Errorf("priority ordering wrong: app=%d ext=%d fw=%d test=%d other=%d",
			targetPriority("application"), targetPriority("app-extension"),
			targetPriority("framework"), targetPriority("bundle.unit-test"),
			targetPriority("aggregate"))
	}
	if targetPriority("bundle.unit-test") == 0 || targetPriority("bundle.ui-testing") == 0 {
		t.Error("test bundles must be modeled (priority > 0)")
	}
	roleType := map[string]string{
		"application":       facts.ModuleRoleProduction,
		"framework":         facts.ModuleRoleProduction,
		"app-extension":     facts.ModuleRoleProduction,
		"bundle.unit-test":  facts.ModuleRoleTest,
		"bundle.ui-testing": facts.ModuleRoleTest,
		"aggregate":         facts.ModuleRoleTooling,
	}
	for typ, want := range roleType {
		if got := moduleRoleForXcodeType(typ); got != want {
			t.Errorf("moduleRoleForXcodeType(%q) = %q, want %q", typ, got, want)
		}
	}
	rolePath := map[string]string{
		"Tests/Core/Account":    facts.ModuleRoleTest,
		"MyLib/Test":            facts.ModuleRoleTest,
		"Scripts/Localizations": facts.ModuleRoleTooling,
		"fastlane":              facts.ModuleRoleTooling,
		"ci_scripts":            facts.ModuleRoleTooling,
		"Sources/Core":          facts.ModuleRoleUnknown,
	}
	for p, want := range rolePath {
		if got := facts.ModuleRoleForPath(p); got != want {
			t.Errorf("facts.ModuleRoleForPath(%q) = %q, want %q", p, got, want)
		}
	}
}

// TestExtract_TestBundleCollapsesAndTagsRole verifies that a bundle.unit-test
// target's files collapse into ONE module tagged module_role=test (not exploded
// into per-leaf-directory modules), that framework modules are tagged
// production, and that an uncovered tooling directory falls back to a leaf module
// tagged tooling.
func TestExtract_TestBundleCollapsesAndTagsRole(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, repo, "project.yml", `
name: app
targets:
  Core:
    type: framework
    sources:
      - Sources/Core
  CoreTests:
    type: bundle.unit-test
    sources:
      - Tests/Core
    dependencies:
      - target: Core
`)
	files := []string{}
	add := func(rel, content string) {
		mustWrite(t, repo, rel, content)
		files = append(files, rel)
	}
	add("Sources/Core/Core.swift", "public struct CoreEntry {}\n")
	add("Tests/Core/Account/AccountTests.swift", "import Core\nstruct AccountTests {}\n")
	add("Tests/Core/Feed/VOs/FeedVOTests.swift", "import Core\nstruct FeedVOTests {}\n")
	add("Scripts/gen.swift", "struct Gen {}\n")

	ff, err := New().Extract(context.Background(), repo, files)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	modules := map[string]facts.Fact{}
	for _, f := range ff {
		if f.Kind == facts.KindModule {
			modules[f.Name] = f
		}
	}

	// The two nested test directories collapse into the single Tests/Core module.
	tc, ok := modules["Tests/Core"]
	if !ok {
		t.Fatalf("missing collapsed module Tests/Core; modules: %v", keysOf(modules))
	}
	if tc.Props["module_role"] != facts.ModuleRoleTest {
		t.Errorf("Tests/Core module_role = %v, want test", tc.Props["module_role"])
	}
	for _, leaked := range []string{"Tests/Core/Account", "Tests/Core/Feed/VOs", "Tests/Core/Feed"} {
		if _, bad := modules[leaked]; bad {
			t.Errorf("test leaf %q leaked as its own module (should collapse into Tests/Core)", leaked)
		}
	}
	// Production framework tagged production.
	if core := modules["Sources/Core"]; core.Props["module_role"] != facts.ModuleRoleProduction {
		t.Errorf("Sources/Core module_role = %v, want production", core.Props["module_role"])
	}
	// Uncovered tooling dir → leaf-fallback module tagged tooling.
	if s, ok := modules["Scripts"]; !ok {
		t.Errorf("missing leaf-fallback module Scripts; modules: %v", keysOf(modules))
	} else if s.Props["module_role"] != facts.ModuleRoleTooling {
		t.Errorf("Scripts module_role = %v, want tooling", s.Props["module_role"])
	}
}

func keysOf(m map[string]facts.Fact) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hasDepEdge(ff []facts.Fact, from, to, source string) bool {
	for _, f := range ff {
		if f.Kind != facts.KindDependency {
			continue
		}
		if slashDir(f.File) != from {
			continue
		}
		if sourceOf(f) != source {
			continue
		}
		if importTargetOf(f) == to {
			return true
		}
	}
	return false
}

func importResolvesTo(ff []facts.Fact, target string) bool {
	for _, f := range ff {
		if f.Kind != facts.KindDependency {
			continue
		}
		if importTargetOf(f) == target && sourceOf(f) == "internal" {
			return true
		}
	}
	return false
}
