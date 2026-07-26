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
	resolveImports(ff, modules)

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
		resolveImports(ff, modules)
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
		resolveImports(ff, modules)
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
	resolveImports(ff, modules)

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
	resolveImports(ff, modules)
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
	resolveImports(ff, modules)
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
	resolveCallTargets(ff, fileModules)
	got := callTarget(ff[0])
	want := "airflow-core/src/airflow/api/common/airflow_health.get_airflow_health"
	if got != want {
		t.Errorf("resolved call target = %q, want %q", got, want)
	}
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
	resolveCallTargets(ff, fileModules)
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
	resolveCallTargets(ff, fileModules)
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
	resolveCallTargets(ff, fileModules)
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
	resolveCallTargets(ff, fileModules)
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
	resolveCallTargets(ff, fileModules)

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
	resolveCallTargets(ff, fileModules)

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
	resolveCallTargets(ff, fileModules)

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
	resolveCallTargets(ff, fileModules)

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
	resolveCallTargets(ff, fileModules)

	// "pkg/svc" is a real module file, so the symbol belongs to it, not to impl.
	if got, want := callTarget(ff[1]), "pkg/svc.run"; got != want {
		t.Errorf("exact module match must win: got %q, want %q", got, want)
	}
}
