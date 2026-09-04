package pythonextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// modSet builds a module-dir set from the given dirs.
func modSet(dirs ...string) map[string]bool {
	m := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		m[d] = true
	}
	return m
}

// depFact builds a dependency fact with a single imports relation, as the
// extractor emits them.
func depFact(file, target string) facts.Fact {
	return facts.Fact{
		Kind:      facts.KindDependency,
		Name:      "x -> " + target,
		File:      file,
		Props:     map[string]any{"language": "python"},
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: target}},
	}
}

// importTarget returns the (possibly rewritten) imports target of a dep fact.
func importTarget(f facts.Fact) string {
	for _, r := range f.Relations {
		if r.Kind == facts.RelImports {
			return r.Target
		}
	}
	return ""
}

func source(f facts.Fact) string {
	s, _ := f.Props["source"].(string)
	return s
}

func TestResolveImports_AbsoluteMultiSourceRoot(t *testing.T) {
	modules := modSet(
		"airflow-core/src/airflow",
		"airflow-core/src/airflow/models",
		"airflow-core/src/airflow/utils",
		"providers/foo/src/airflow/providers/foo",
	)
	ff := []facts.Fact{
		depFact("airflow-core/src/airflow/dag.py", "airflow.models.dag"),
		depFact("airflow-core/src/airflow/dag.py", "airflow.providers.foo"),
		depFact("airflow-core/src/airflow/dag.py", "airflow.utils"),
	}
	resolveImports(ff, modules, nil, nil)

	if got := importTarget(ff[0]); got != "airflow-core/src/airflow/models" {
		t.Errorf("airflow.models.dag resolved to %q, want airflow-core/src/airflow/models", got)
	}
	if got := importTarget(ff[1]); got != "providers/foo/src/airflow/providers/foo" {
		t.Errorf("airflow.providers.foo resolved to %q, want providers/foo/src/airflow/providers/foo", got)
	}
	if got := importTarget(ff[2]); got != "airflow-core/src/airflow/utils" {
		t.Errorf("airflow.utils resolved to %q, want airflow-core/src/airflow/utils", got)
	}
	for i, f := range ff {
		if source(f) != "internal" {
			t.Errorf("fact %d source = %q, want internal", i, source(f))
		}
	}
}

func TestResolveImports_ShortestSourceRootWinsDeterministic(t *testing.T) {
	// Two dirs whose trailing segments are both "pkg/models"; the shorter path
	// (nearest source root) must win, consistently across runs.
	modules := modSet(
		"src/pkg/models",
		"deeply/nested/src/pkg/models",
		"src/pkg",
	)
	for run := 0; run < 3; run++ {
		ff := []facts.Fact{depFact("src/pkg/x.py", "pkg.models")}
		resolveImports(ff, modules, nil, nil)
		if got := importTarget(ff[0]); got != "src/pkg/models" {
			t.Fatalf("run %d: pkg.models resolved to %q, want src/pkg/models", run, got)
		}
	}
}

func TestResolveImports_Relative(t *testing.T) {
	modules := modSet("pkg/a/b", "pkg/a", "pkg")
	cases := []struct {
		raw  string
		want string // expected rewritten target; "" means unchanged (self)
	}{
		{".sibling", "pkg/a/b/sibling"},
		{"..uncle", "pkg/a/uncle"},
		{"...grand.x", "pkg/grand/x"},
		{".", ".|self"}, // bare dot is self → target unchanged
	}
	for _, c := range cases {
		ff := []facts.Fact{depFact("pkg/a/b/mod.py", c.raw)}
		resolveImports(ff, modules, nil, nil)
		got := importTarget(ff[0])
		if c.want == ".|self" {
			if got != c.raw {
				t.Errorf("relative %q: self import should leave target unchanged, got %q", c.raw, got)
			}
		} else if got != c.want {
			t.Errorf("relative %q resolved to %q, want %q", c.raw, got, c.want)
		}
		if source(ff[0]) != "internal" {
			t.Errorf("relative %q source = %q, want internal", c.raw, source(ff[0]))
		}
	}
}

func TestResolveImports_StdlibAndExternal(t *testing.T) {
	modules := modSet("pkg/app")
	ff := []facts.Fact{
		depFact("pkg/app/x.py", "os"),
		depFact("pkg/app/x.py", "json.decoder"),
		depFact("pkg/app/x.py", "requests"),
		depFact("pkg/app/x.py", "sqlalchemy.orm"),
	}
	resolveImports(ff, modules, nil, nil)

	wantSource := []string{"stdlib", "stdlib", "external", "external"}
	for i, f := range ff {
		if source(f) != wantSource[i] {
			t.Errorf("fact %d (%s) source = %q, want %q", i, importTarget(f), source(f), wantSource[i])
		}
	}
	if importTarget(ff[0]) != "os" {
		t.Errorf("stdlib import target should be unchanged, got %q", importTarget(ff[0]))
	}
}

// TestResolveImports_StdlibNotShadowedByInternalDir guards Fix 3: a stdlib
// top-level name (e.g. "typing") must classify as stdlib even when an internal
// directory happens to share that trailing segment, instead of mis-resolving to
// it and producing a phantom internal coupling.
func TestResolveImports_StdlibNotShadowedByInternalDir(t *testing.T) {
	modules := modSet(
		"providers/http/src/airflow/providers/http/hooks",
		"providers/opsgenie/tests/unit/opsgenie/typing", // collides with stdlib "typing"
	)
	ff := []facts.Fact{
		depFact("providers/http/src/airflow/providers/http/hooks/http.py", "typing"),
	}
	resolveImports(ff, modules, nil, nil)
	if got := source(ff[0]); got != "stdlib" {
		t.Errorf("import typing source = %q, want stdlib", got)
	}
	if got := importTarget(ff[0]); got != "typing" {
		t.Errorf("import typing target = %q, want unchanged 'typing' (no phantom internal edge)", got)
	}
}

func TestResolveImports_SelfImportNoSelfEdge(t *testing.T) {
	// An absolute import that resolves to the importer's own dir must NOT rewrite
	// the target to that dir (which would create a self-coupling edge).
	modules := modSet("pkg/app", "pkg")
	ff := []facts.Fact{depFact("pkg/app/x.py", "pkg.app")}
	resolveImports(ff, modules, nil, nil)
	if got := importTarget(ff[0]); got == "pkg/app" {
		t.Errorf("self import resolved to own dir %q (self-edge); should be left as dotted", got)
	}
}

// TestExtract_PythonResolvesImports is the end-to-end guard: a real Extract over
// a temp multi-dir repo must produce a dependency fact whose import target equals
// a module Name with source=internal.
func TestExtract_PythonResolvesImports(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"pyproject.toml":              "[project]\nname='demo'\n",
		"src/demo/__init__.py":        "",
		"src/demo/models/__init__.py": "class Model:\n    pass\n",
		"src/demo/service.py":         "from demo.models import Model\n\ndef use():\n    return Model()\n",
	}
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

	ff, err := New().Extract(context.Background(), dir, rel)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Collect module names.
	moduleNames := map[string]bool{}
	for _, f := range ff {
		if f.Kind == facts.KindModule {
			moduleNames[f.Name] = true
		}
	}

	// Find the dependency fact for the "demo.models" import from service.py and
	// assert it resolved to the models module dir.
	found := false
	for _, f := range ff {
		if f.Kind != facts.KindDependency {
			continue
		}
		for _, r := range f.Relations {
			if r.Kind == facts.RelImports && r.Target == "src/demo/models" {
				if source(f) != "internal" {
					t.Errorf("resolved import source = %q, want internal", source(f))
				}
				if !moduleNames[r.Target] {
					t.Errorf("resolved import target %q is not a module Name", r.Target)
				}
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected the demo.models import to resolve to module dir 'src/demo/models'; module names: %v", keysOf(moduleNames))
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// symCall builds a symbol fact with a single RelCalls relation to target.
func symCall(file, name, target string) facts.Fact {
	return facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      name,
		File:      file,
		Props:     map[string]any{"language": "python", "symbol_kind": facts.SymbolFunc},
		Relations: []facts.Relation{{Kind: facts.RelCalls, Target: target}},
	}
}

// callTarget returns the first RelCalls target of a fact, or "" if none remain
// (edge dropped).
func callTarget(f facts.Fact) string {
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls {
			return r.Target
		}
	}
	return ""
}

func TestResolveCallTargets_AbsoluteInternal_RewritesToSlashSymbol(t *testing.T) {
	fileModules := modSet(
		"airflow-core/src/airflow/api/common/airflow_health",
		"airflow-core/src/airflow/api_fastapi/core_api/routes/public/monitor",
	)
	ff := []facts.Fact{
		symCall(
			"airflow-core/src/airflow/api_fastapi/core_api/routes/public/monitor.py",
			"airflow-core/src/airflow/api_fastapi/core_api/routes/public/monitor.get_health",
			"airflow.api.common.airflow_health.get_airflow_health",
		),
	}
	resolveCallTargets(ff, fileModules, nil)
	got := callTarget(ff[0])
	want := "airflow-core/src/airflow/api/common/airflow_health.get_airflow_health"
	if got != want {
		t.Errorf("resolved call target = %q, want %q", got, want)
	}
}

func TestExtract_PythonResolvesInheritanceTarget(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"pkg/__init__.py":    "",
		"pkg/wrongparent.py": "class Parent:\n    pass\n",
		"pkg/parent.py":      "class Parent:\n    pass\n",
		"pkg/child.py":       "from pkg.parent import Parent\n\nclass Child(Parent):\n    pass\n",
	}
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

	all, err := New().Extract(context.Background(), dir, rel)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	for _, f := range all {
		if f.Kind != facts.KindSymbol || f.Name != "pkg/child.Child" {
			continue
		}
		if hasRel(f, facts.RelImplements, "pkg/parent.Parent") {
			return
		}
		t.Fatalf("Child inheritance target = %v, want pkg/parent.Parent", f.Relations)
	}
	t.Fatal("missing pkg/child.Child symbol")
}

func TestExtract_PythonResolvesMultipleInheritanceTargets(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"pkg/__init__.py": "",
		"pkg/father.py":   "class Father:\n    pass\n",
		"pkg/mother.py":   "class Mother:\n    pass\n",
		"pkg/child.py":    "from pkg.father import Father\nfrom pkg.mother import Mother\n\nclass Child(Father, Mother):\n    pass\n",
	}
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

	all, err := New().Extract(context.Background(), dir, rel)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	for _, f := range all {
		if f.Kind != facts.KindSymbol || f.Name != "pkg/child.Child" {
			continue
		}
		if !hasRel(f, facts.RelImplements, "pkg/father.Father") {
			t.Errorf("Child missing Father inheritance target: %v", f.Relations)
		}
		if !hasRel(f, facts.RelImplements, "pkg/mother.Mother") {
			t.Errorf("Child missing Mother inheritance target: %v", f.Relations)
		}
		return
	}
	t.Fatal("missing pkg/child.Child symbol")
}

func TestResolveCallTargets_External_DropsEdge(t *testing.T) {
	fileModules := modSet("airflow-core/src/airflow/models/dag")
	ff := []facts.Fact{
		symCall(
			"airflow-core/src/airflow/models/dag.py",
			"airflow-core/src/airflow/models/dag.make",
			"sqlalchemy.select",
		),
	}
	resolveCallTargets(ff, fileModules, nil)
	if got := callTarget(ff[0]); got != "" {
		t.Errorf("external edge should be dropped, but target = %q", got)
	}
}

func TestResolveCallTargets_Stdlib_DropsEdge(t *testing.T) {
	fileModules := modSet("airflow-core/src/airflow/utils/helpers")
	ff := []facts.Fact{
		symCall(
			"airflow-core/src/airflow/utils/helpers.py",
			"airflow-core/src/airflow/utils/helpers.cwd",
			"os.getcwd",
		),
	}
	resolveCallTargets(ff, fileModules, nil)
	if got := callTarget(ff[0]); got != "" {
		t.Errorf("stdlib edge should be dropped, but target = %q", got)
	}
}

func TestResolveCallTargets_InternalReexport_KeepsDotted(t *testing.T) {
	// "airflow.models" is a package dir (re-export via __init__), not an exact file
	// module, so the dotted target is kept for short-name matching downstream.
	fileModules := modSet("airflow-core/src/airflow/models/dag")
	ff := []facts.Fact{
		symCall(
			"airflow-core/src/airflow/example.py",
			"airflow-core/src/airflow/example.build",
			"airflow.models.DAG",
		),
	}
	resolveCallTargets(ff, fileModules, nil)
	if got := callTarget(ff[0]); got != "airflow.models.DAG" {
		t.Errorf("internal re-export target = %q, want it kept as %q", got, "airflow.models.DAG")
	}
}

func TestResolveCallTargets_AlreadyResolvedSlash_Untouched(t *testing.T) {
	fileModules := modSet("airflow-core/src/airflow/configuration")
	target := "airflow-core/src/airflow/configuration.get_airflow_home"
	ff := []facts.Fact{
		symCall(
			"airflow-core/src/airflow/foo.py",
			"airflow-core/src/airflow/foo.bar",
			target,
		),
	}
	resolveCallTargets(ff, fileModules, nil)
	if got := callTarget(ff[0]); got != target {
		t.Errorf("already-resolved slash target should be untouched, got %q", got)
	}
}

// initDep builds the dependency fact an __init__.py from-import produces after
// resolveImports has rewritten its target to a slash module path.
func initDep(initFile, sourceModule string, reexports ...string) facts.Fact {
	return facts.Fact{
		Kind:      facts.KindDependency,
		Name:      "x -> " + sourceModule,
		File:      initFile,
		Props:     map[string]any{"language": "python", "reexports": reexports},
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: sourceModule}},
	}
}

// The router-wiring case. A package is a DIRECTORY, so "pkg.routers" matches no
// module file and the call target used to dangle as a dotted string — leaving the
// API composition root connected to none of the routers it mounts.
func TestResolveCallTargets_PackageReexport_ResolvesToDefiningModule(t *testing.T) {
	fileModules := modSet(
		"cognee/api/client",
		"cognee/api/v1/add/routers/__init__",
		"cognee/api/v1/add/routers/get_add_router",
	)
	ff := []facts.Fact{
		initDep("cognee/api/v1/add/routers/__init__.py",
			"cognee/api/v1/add/routers/get_add_router", "get_add_router"),
		symCall("cognee/api/client.py", "cognee/api/client.app",
			"cognee.api.v1.add.routers.get_add_router"),
	}
	resolveCallTargets(ff, fileModules, nil)

	want := "cognee/api/v1/add/routers/get_add_router.get_add_router"
	if got := callTarget(ff[1]); got != want {
		t.Errorf("resolved call target = %q, want %q", got, want)
	}
}

// The re-exported name need not match the module file name.
func TestResolveCallTargets_PackageReexport_NameDiffersFromModule(t *testing.T) {
	fileModules := modSet("pkg/app", "pkg/svc/__init__", "pkg/svc/impl")
	ff := []facts.Fact{
		initDep("pkg/svc/__init__.py", "pkg/svc/impl", "Widget", "build"),
		symCall("pkg/app.py", "pkg/app.run", "pkg.svc.Widget"),
	}
	resolveCallTargets(ff, fileModules, nil)

	if got, want := callTarget(ff[1]), "pkg/svc/impl.Widget"; got != want {
		t.Errorf("resolved call target = %q, want %q", got, want)
	}
}

// A name re-exported from two different modules in the same package is ambiguous.
// Binding it to either would fabricate an edge, so it stays dotted — carrying no
// graph edge, exactly as before this resolution step existed.
func TestResolveCallTargets_PackageReexport_AmbiguousStaysDotted(t *testing.T) {
	fileModules := modSet("pkg/app", "pkg/svc/__init__", "pkg/svc/a", "pkg/svc/b")
	ff := []facts.Fact{
		initDep("pkg/svc/__init__.py", "pkg/svc/a", "thing"),
		initDep("pkg/svc/__init__.py", "pkg/svc/b", "thing"),
		symCall("pkg/app.py", "pkg/app.run", "pkg.svc.thing"),
	}
	resolveCallTargets(ff, fileModules, nil)

	if got := callTarget(ff[2]); got != "pkg.svc.thing" {
		t.Errorf("ambiguous re-export must stay dotted, got %q", got)
	}
}

// An __init__.py whose source module did not resolve to an internal path cannot
// name an internal symbol; the target must not be bound to it.
func TestResolveCallTargets_PackageReexport_ExternalSourceIgnored(t *testing.T) {
	fileModules := modSet("pkg/app", "pkg/svc/__init__")
	ff := []facts.Fact{
		// Unresolved/external source: still dotted, no slash.
		initDep("pkg/svc/__init__.py", "third_party.lib", "helper"),
		symCall("pkg/app.py", "pkg/app.run", "pkg.svc.helper"),
	}
	resolveCallTargets(ff, fileModules, nil)

	if got := callTarget(ff[1]); got != "pkg.svc.helper" {
		t.Errorf("external re-export source must not bind; got %q", got)
	}
}

// An exact module match must still win: the re-export step is a FALLBACK and must
// not change any target that already resolved.
func TestResolveCallTargets_ExactModuleWinsOverReexport(t *testing.T) {
	fileModules := modSet("pkg/app", "pkg/svc", "pkg/svc/__init__", "pkg/svc/impl")
	ff := []facts.Fact{
		initDep("pkg/svc/__init__.py", "pkg/svc/impl", "run"),
		symCall("pkg/app.py", "pkg/app.main", "pkg.svc.run"),
	}
	resolveCallTargets(ff, fileModules, nil)

	// "pkg/svc" is a real module file, so the symbol belongs to it, not to impl.
	if got, want := callTarget(ff[1]), "pkg/svc.run"; got != want {
		t.Errorf("exact module match must win: got %q, want %q", got, want)
	}
}

// A directory nested inside a package must not capture a same-named third-party
// import. Suffix matching used to let "…/databases/relational/sqlalchemy" satisfy
// a plain `import sqlalchemy`, classifying the dependency internal and keeping
// hundreds of third-party call edges in the graph as first-party.
//
// Python's rule is that a directory is a top-level package only if its parent is
// not itself one — "relational" holds an __init__.py, so that sqlalchemy dir is
// reachable only as relational.sqlalchemy.
func TestResolveImports_NestedLookalikeDoesNotCaptureThirdParty(t *testing.T) {
	modules := modSet("cognee/api", "cognee/infrastructure/databases/relational/sqlalchemy")
	pkgDirs := modSet(
		"cognee",
		"cognee/infrastructure",
		"cognee/infrastructure/databases",
		"cognee/infrastructure/databases/relational",
	)
	ff := []facts.Fact{depFact("cognee/api/client.py", "sqlalchemy")}

	resolveImports(ff, modules, nil, pkgDirs)

	if got := ff[0].Props["source"]; got != "external" {
		t.Errorf("source = %v, want external (a nested subpackage cannot satisfy a top-level import)", got)
	}
	if got := ff[0].Relations[0].Target; got != "sqlalchemy" {
		t.Errorf("target rewritten to %q; it should stay the third-party name", got)
	}
}

// The same rule must NOT break the multi-source-root layout it was built for:
// "airflow-core/src/airflow" is importable as "airflow" because "src" holds no
// __init__.py.
func TestResolveImports_MultiSourceRootSurvivesPackageBoundaryRule(t *testing.T) {
	modules := modSet("airflow-core/src/airflow/models")
	pkgDirs := modSet("airflow-core/src/airflow", "airflow-core/src/airflow/models")
	ff := []facts.Fact{depFact("other/consumer.py", "airflow.models")}

	resolveImports(ff, modules, nil, pkgDirs)

	if got, want := ff[0].Relations[0].Target, "airflow-core/src/airflow/models"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
	if got := ff[0].Props["source"]; got != "internal" {
		t.Errorf("source = %v, want internal", got)
	}
}

// A bare `import models` must not reach a subpackage of an importable package.
func TestResolveImports_SubpackageNotReachableByBareName(t *testing.T) {
	modules := modSet("airflow-core/src/airflow/models")
	pkgDirs := modSet("airflow-core/src/airflow", "airflow-core/src/airflow/models")
	ff := []facts.Fact{depFact("other/consumer.py", "models")}

	resolveImports(ff, modules, nil, pkgDirs)

	if got := ff[0].Props["source"]; got != "external" {
		t.Errorf("source = %v, want external (models is a subpackage of airflow)", got)
	}
}

// With no package information the rule cannot fire and the index keeps its
// historical permissive shape, so callers that do not track __init__.py are
// unaffected. This is what keeps the change from silently altering older paths.
func TestBuildSuffixIndex_NoPackageDirsStaysPermissive(t *testing.T) {
	modules := modSet("a/b/c")

	loose := buildSuffixIndex(modules, nil)
	for _, key := range []string{"a.b.c", "b.c", "c"} {
		if len(loose[key]) == 0 {
			t.Errorf("permissive index missing key %q", key)
		}
	}

	strict := buildSuffixIndex(modules, modSet("a", "a/b"))
	if len(strict["a.b.c"]) == 0 {
		t.Error("full path must always be indexed")
	}
	for _, key := range []string{"b.c", "c"} {
		if len(strict[key]) != 0 {
			t.Errorf("key %q should be suppressed: its parent is a package", key)
		}
	}
}

// The call-edge half: a third-party call through a like-named internal dir must be
// dropped, not retained as a dangling dotted target.
func TestResolveCallTargets_NestedLookalikeThirdPartyDropped(t *testing.T) {
	fileModules := modSet(
		"cognee/api/client",
		"cognee/infrastructure/databases/relational/sqlalchemy/adapter",
	)
	pkgDirs := modSet(
		"cognee",
		"cognee/infrastructure",
		"cognee/infrastructure/databases",
		"cognee/infrastructure/databases/relational",
		"cognee/infrastructure/databases/relational/sqlalchemy",
	)
	ff := []facts.Fact{
		symCall("cognee/api/client.py", "cognee/api/client.setup", "sqlalchemy.Column"),
	}

	resolveCallTargets(ff, fileModules, pkgDirs)

	if got := callTarget(ff[0]); got != "" {
		t.Errorf("third-party call edge should be dropped, got %q", got)
	}
}

// symFact declares a symbol so class-qualified resolution has something to confirm
// a candidate against.
func symFact(file, name string) facts.Fact {
	return facts.Fact{
		Kind:  facts.KindSymbol,
		Name:  name,
		File:  file,
		Props: map[string]any{"language": "python", "symbol_kind": "method"},
	}
}

// A method reached as module.Class.method. Splitting on the last dot asks for a
// module named "…engine.DataPoint" and fails, but the walker qualifies symbols as
// module.Class.method, so the wanted name exists — the split point just has to
// move left.
func TestResolveCallTargets_ClassQualifiedChainResolves(t *testing.T) {
	fileModules := modSet("pkg/app", "pkg/infra/engine")
	ff := []facts.Fact{
		symFact("pkg/infra/engine.py", "pkg/infra/engine.DataPoint.get_embeddable_data"),
		symCall("pkg/app.py", "pkg/app.run", "pkg.infra.engine.DataPoint.get_embeddable_data"),
	}

	resolveCallTargets(ff, fileModules, nil)

	want := "pkg/infra/engine.DataPoint.get_embeddable_data"
	if got := callTarget(ff[1]); got != want {
		t.Errorf("resolved call target = %q, want %q", got, want)
	}
}

// The confirmation guard: a shorter prefix is a weaker claim, so a candidate that
// names no real symbol must NOT be minted. Fabricating an edge here would silently
// mark dead code live — worse than the dangling target it replaces.
func TestResolveCallTargets_ClassQualifiedUnconfirmedStaysDotted(t *testing.T) {
	fileModules := modSet("pkg/app", "pkg/infra/engine")
	ff := []facts.Fact{
		// No symbol fact for DataPoint.missing_method.
		symCall("pkg/app.py", "pkg/app.run", "pkg.infra.engine.DataPoint.missing_method"),
	}

	resolveCallTargets(ff, fileModules, nil)

	if got := callTarget(ff[0]); got != "pkg.infra.engine.DataPoint.missing_method" {
		t.Errorf("unconfirmed candidate must stay dotted, got %q", got)
	}
}

// A class-qualified chain reached through a package re-export: resolve the package
// to the defining module, then confirm the full class-qualified name.
func TestResolveCallTargets_ClassQualifiedThroughReexport(t *testing.T) {
	fileModules := modSet("pkg/app", "pkg/svc/__init__", "pkg/svc/impl")
	ff := []facts.Fact{
		initDep("pkg/svc/__init__.py", "pkg/svc/impl", "Widget"),
		symFact("pkg/svc/impl.py", "pkg/svc/impl.Widget.render"),
		symCall("pkg/app.py", "pkg/app.run", "pkg.svc.Widget.render"),
	}

	resolveCallTargets(ff, fileModules, nil)

	if got, want := callTarget(ff[2]), "pkg/svc/impl.Widget.render"; got != want {
		t.Errorf("resolved call target = %q, want %q", got, want)
	}
}

// The single-segment path must not start demanding confirmation: a plain
// module.function target still resolves even when no symbol fact declares it.
func TestResolveCallTargets_SingleSegmentNeedsNoConfirmation(t *testing.T) {
	fileModules := modSet("pkg/app", "pkg/util")
	ff := []facts.Fact{
		symCall("pkg/app.py", "pkg/app.run", "pkg.util.helper"),
	}

	resolveCallTargets(ff, fileModules, nil)

	if got, want := callTarget(ff[0]), "pkg/util.helper"; got != want {
		t.Errorf("resolved call target = %q, want %q", got, want)
	}
}

// A directory with no __init__.py breaks the package chain and starts a new source
// root, so its own children are importable by bare name even though the directory
// sits inside a package. Scripts in such a tree import each other that way
// ("from corpus import read_source"), and classifying those external made the whole
// tree read as dead code.
//
// The same fixture pins that this does NOT undo the third-party guard: the
// look-alike dir's parent IS a package, so it stays unreachable by bare name. Both
// halves must hold together — that is the whole point of the per-position rule.
func TestImportableRoots_NonPackageDirStartsNewRoot(t *testing.T) {
	modules := modSet(
		"pkg/analysis/corpus",             // pkg/analysis has no __init__.py
		"pkg/infra/relational/sqlalchemy", // pkg/infra/relational IS a package
	)
	pkgDirs := modSet("pkg", "pkg/infra", "pkg/infra/relational")

	roots := importableRoots(modules, pkgDirs)

	if !roots["corpus"] {
		t.Error("corpus must be an importable root: its parent pkg/analysis is not a package")
	}
	if roots["sqlalchemy"] {
		t.Error("sqlalchemy must NOT be a root: its parent pkg/infra/relational is a package")
	}
	if !roots["pkg"] {
		t.Error("the repo-root segment must always be a root")
	}
	if roots["infra"] || roots["relational"] {
		t.Error("subpackages of pkg must not be roots")
	}
}

// End-to-end for the same shape: a sibling import inside a non-package directory
// resolves to a real symbol, while a bare third-party import in the same repo is
// still classified external.
func TestResolveCallTargets_SiblingImportInNonPackageDir(t *testing.T) {
	fileModules := modSet("pkg/analysis/analyze", "pkg/analysis/corpus", "pkg/infra/relational/sqlalchemy/adapter")
	pkgDirs := modSet("pkg", "pkg/infra", "pkg/infra/relational", "pkg/infra/relational/sqlalchemy")
	ff := []facts.Fact{
		symCall("pkg/analysis/analyze.py", "pkg/analysis/analyze.main", "corpus.read_source"),
		symCall("pkg/analysis/analyze.py", "pkg/analysis/analyze.main2", "sqlalchemy.Column"),
	}

	resolveCallTargets(ff, fileModules, pkgDirs)

	if got, want := callTarget(ff[0]), "pkg/analysis/corpus.read_source"; got != want {
		t.Errorf("sibling import: got %q, want %q", got, want)
	}
	if got := callTarget(ff[1]); got != "" {
		t.Errorf("third-party import must still be dropped, got %q", got)
	}
}

// TestResolveImports_SamePackageAbsoluteIsInternal is the regression guard for
// same-package absolute imports being reported as third-party. `from app.db import
// x` written inside app/ has no directory to bind to other than app/ itself — the
// importer's own dir — so the resolver used to give up and fall through to
// "external", counting this repository's own code as a third-party dependency.
func TestResolveImports_SamePackageAbsoluteIsInternal(t *testing.T) {
	modules := modSet("app")
	fileModules := modSet("app/api", "app/db")
	ff := []facts.Fact{depFact("app/api.py", "app.db")}
	resolveImports(ff, modules, fileModules, nil)
	if got := source(ff[0]); got != "internal" {
		t.Errorf("app.db from app/api.py source = %q, want internal (it is first-party code)", got)
	}
	if got := importTarget(ff[0]); got != "app/db" {
		t.Errorf("app.db resolved to %q, want app/db (the sibling file module)", got)
	}
}

// TestResolveImports_PackageInitBarrelReachesSubmodule covers the shape that hid an
// 830 ms transitive dependency in a real repository: a package's own __init__.py
// re-exporting from its submodules by absolute path. Every such edge was dropped,
// so an import-closure walk stopped dead at the barrel.
func TestResolveImports_PackageInitBarrelReachesSubmodule(t *testing.T) {
	modules := modSet("pkg", "pkg/infra", "pkg/infra/llm")
	fileModules := modSet("pkg/infra/llm/__init__", "pkg/infra/llm/config", "pkg/infra/llm/gateway")
	ff := []facts.Fact{
		depFact("pkg/infra/llm/__init__.py", "pkg.infra.llm.config"),
		depFact("pkg/infra/llm/__init__.py", "pkg.infra.llm.gateway"),
	}
	resolveImports(ff, modules, fileModules, nil)
	for i, want := range []string{"pkg/infra/llm/config", "pkg/infra/llm/gateway"} {
		if got := source(ff[i]); got != "internal" {
			t.Errorf("fact %d source = %q, want internal", i, got)
		}
		if got := importTarget(ff[i]); got != want {
			t.Errorf("fact %d resolved to %q, want %q", i, got, want)
		}
	}
}

// TestResolveImports_SelfPackageImportStaysInternalWithoutTarget pins the half of
// the fix that has no target to offer: importing the package itself binds to no
// distinct module, so the target is left dotted (no self-edge) — but the import is
// still first-party and must not be classified external.
func TestResolveImports_SelfPackageImportStaysInternalWithoutTarget(t *testing.T) {
	modules := modSet("pkg/app", "pkg")
	ff := []facts.Fact{depFact("pkg/app/x.py", "pkg.app")}
	resolveImports(ff, modules, modSet("pkg/app/x"), nil)
	if got := importTarget(ff[0]); got == "pkg/app" {
		t.Errorf("self import resolved to own dir %q (self-edge)", got)
	}
	if got := source(ff[0]); got != "internal" {
		t.Errorf("source = %q, want internal — pkg.app is this repo's own package", got)
	}
}

// TestResolveImports_ExternalStillExternal guards the blast radius: widening the
// internal classification must not swallow third-party or stdlib imports.
func TestResolveImports_ExternalStillExternal(t *testing.T) {
	modules := modSet("app")
	fileModules := modSet("app/api")
	ff := []facts.Fact{
		depFact("app/api.py", "sqlalchemy.orm"),
		depFact("app/api.py", "os.path"),
	}
	resolveImports(ff, modules, fileModules, nil)
	if got := source(ff[0]); got != "external" {
		t.Errorf("sqlalchemy.orm source = %q, want external", got)
	}
	if got := source(ff[1]); got != "stdlib" {
		t.Errorf("os.path source = %q, want stdlib", got)
	}
}

// TestExtract_PythonResolvesInheritanceViaRelativeImport covers inheritance bound
// through a RELATIVE import. Its import target has always resolved to a slash
// module path, so the composed "<module>.<symbol>" was already canonical and the
// dotted resolver could never read it — the edge was dropped long before
// same-package absolute imports resolved to slash paths too.
func TestExtract_PythonResolvesInheritanceViaRelativeImport(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"pkg/__init__.py":    "",
		"pkg/wrongparent.py": "class Parent:\n    pass\n",
		"pkg/parent.py":      "class Parent:\n    pass\n",
		"pkg/child.py":       "from .parent import Parent\n\nclass Child(Parent):\n    pass\n",
	}
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
	all, err := New().Extract(context.Background(), dir, rel)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, f := range all {
		if f.Kind != facts.KindSymbol || f.Name != "pkg/child.Child" {
			continue
		}
		if hasRel(f, facts.RelImplements, "pkg/parent.Parent") {
			return
		}
		t.Fatalf("Child inheritance target = %v, want pkg/parent.Parent", f.Relations)
	}
	t.Fatal("missing pkg/child.Child symbol")
}

// TestResolveImports_AbsoluteBindsToModuleNotPackage pins the granularity: an
// import naming a module resolves to that module, not to the package directory
// that also matches one segment shorter. Resolving to the directory made every
// import of `pkg.shared.logging` indistinguishable from an import of `pkg.shared`
// itself, so a file-level import closure could not be derived from the graph.
func TestResolveImports_AbsoluteBindsToModuleNotPackage(t *testing.T) {
	modules := modSet("pkg", "pkg/shared")
	fileModules := modSet("pkg/api", "pkg/shared/logging", "pkg/shared/__init__")
	ff := []facts.Fact{depFact("pkg/api.py", "pkg.shared.logging")}
	resolveImports(ff, modules, fileModules, nil)
	if got := importTarget(ff[0]); got != "pkg/shared/logging" {
		t.Errorf("pkg.shared.logging resolved to %q, want pkg/shared/logging (the module, not the package dir)", got)
	}
	if got := source(ff[0]); got != "internal" {
		t.Errorf("source = %q, want internal", got)
	}
}

// TestResolveImports_AbsolutePackageStillResolvesToDir is the counterpart: when the
// dotted path names a PACKAGE and no module of that name exists, the directory is
// still the right answer. Preferring file modules must not break package imports.
func TestResolveImports_AbsolutePackageStillResolvesToDir(t *testing.T) {
	modules := modSet("pkg", "pkg/shared")
	fileModules := modSet("pkg/api", "pkg/shared/logging", "pkg/shared/__init__")
	ff := []facts.Fact{depFact("pkg/api.py", "pkg.shared")}
	resolveImports(ff, modules, fileModules, nil)
	if got := importTarget(ff[0]); got != "pkg/shared" {
		t.Errorf("pkg.shared resolved to %q, want pkg/shared (a package has no module file)", got)
	}
}

// TestResolveImports_AbsoluteAndRelativeAgree is the consistency guard the whole
// change exists for: the same module imported both ways must produce the same
// target. Relative imports have always resolved to file paths; absolute ones now
// do too.
func TestResolveImports_AbsoluteAndRelativeAgree(t *testing.T) {
	modules := modSet("pkg", "pkg/shared")
	fileModules := modSet("pkg/shared/logging", "pkg/shared/other", "pkg/shared/__init__")
	ff := []facts.Fact{
		depFact("pkg/shared/other.py", "pkg.shared.logging"),
		depFact("pkg/shared/other.py", ".logging"),
	}
	resolveImports(ff, modules, fileModules, nil)
	abs, rel := importTarget(ff[0]), importTarget(ff[1])
	if abs != rel {
		t.Errorf("absolute resolved to %q but relative to %q — the same import must not depend on how it is spelled", abs, rel)
	}
	if abs != "pkg/shared/logging" {
		t.Errorf("resolved to %q, want pkg/shared/logging", abs)
	}
}
