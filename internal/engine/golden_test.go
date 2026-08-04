package engine_test

// End-to-end golden + determinism tests for the full extraction pipeline.
//
// Unlike the per-extractor unit tests (which feed inline snippets to a single
// extractor), these run the real engine wired exactly as production wires it
// (via bootstrap.NewEngine: all 9 extractors, 9 explainers, the llm_context
// renderer) over small fixture repos under testdata/repos, then assert the
// emitted fact graph byte-for-byte against a committed golden file.
//
// This is the regression net for Enola's core promise — "deterministic,
// structurally faithful extraction." A golden mismatch means the fact graph
// for a known repo changed; a reviewer diffs the regenerated JSONL to decide
// whether the change is intended. Regenerate with:
//
//	go test ./internal/engine -run TestGolden -update
//
// The golden captures only the fact graph (Store.WriteJSONL, which sorts facts
// and their relations deterministically). Snapshot metadata (timestamps,
// durations, file hashes, absolute repo path) is intentionally excluded so the
// golden is stable across machines and runs.

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/pkg/bootstrap"
)

var update = flag.Bool("update", false, "regenerate golden files in testdata/golden")

// fixture describes a testdata repo and the sub-repos to snapshot, in order.
// Single-repo fixtures use one entry ("."); multi-repo fixtures list each
// sub-repo so the harness can drive append mode (the 2nd+ repo with append=true).
type fixture struct {
	name     string
	subRepos []string
}

var fixtures = []fixture{
	{name: "go_sample", subRepos: []string{"."}},
	{name: "ts_sample", subRepos: []string{"."}},
	{name: "python_sample", subRepos: []string{"."}},
	// Flask app: @app.route (methods=), @app.get shorthand, a Blueprint @bp.route,
	// and Flask-AppBuilder @expose views. Pins GAP-PY-01 (v109) — routes detected
	// and framework=flask (the @app.get shorthand is NOT mislabeled fastapi).
	{name: "python_flask_sample", subRepos: []string{"."}},
	// TypeScript ORMs: a TypeORM @Entity class, a Drizzle pgTable const, and Prisma
	// models in schema.prisma (read off-glob). Pins GAP-XL-04's TS half (v112) —
	// tsextractor emitted ZERO storage facts, so a database-backed Node service
	// modelled no tables at all. Also pins the io_direct seeding: repo.ts wraps an ORM
	// call in loadPostsFor() and calls it once per iteration, which is only detectable
	// as an N+1 once the ORM call seeds performs_io through the wrapper.
	{name: "ts_orm_sample", subRepos: []string{"."}},
	// Ember app: two .gts template-tag components (one importing the other through a
	// tsconfig path alias, one injecting a service), a .gjs holding a named-binding
	// component AND a default-export template (expression-position blanking; each
	// owns its own template's refs, including a same-file reference), a classic
	// .hbs + .js component pair invoking a component and a helper, a template-only
	// .hbs, a route template owned by its route class and using a modifier, a
	// Router.map with nested paths, ember-data models with a belongsTo/hasMany
	// relationship edge, and a service. Pins v148 end-to-end: template blanking
	// preserves line numbers, strict-mode template refs resolve through imports and
	// locals, and the ember-resolver binder joins .hbs invocations, @service
	// injections and model relationships to the symbols the files actually declare.
	{name: "ts_ember_sample", subRepos: []string{"."}},
	// React Navigation: screen registrations become page routes handled_by their
	// imported components; a literal navigate() becomes a navigation edge from
	// the enclosing symbol. Pins v151's RN half.
	{name: "ts_reactnav_sample", subRepos: []string{"."}},
	// A graphql-ruby server beside a gql-tag client. Pins the GraphQL seam end
	// to end: root-field route facts on both sides, the graphql cross-repo
	// signal drawing client -> server on the exact field name, the unserved
	// operation counted but unlinked, and GraphQL staying OUT of HTTP matching.
	{name: "graphql_multirepo", subRepos: []string{"server", "client"}},
	// Terraform: blocks as symbols, literal references (prefixed, declared bare
	// addresses, depends_on lists) as edges, a local module source drawing the
	// directory dependency. Pins v151's HCL extractor.
	{name: "hcl_sample", subRepos: []string{"."}},
	// Ansible: plays depend on the roles they list, import_role draws role-to-
	// role edges, templates count without rendering. Self-walking (YAML is
	// ignore-globbed), so the fixture also pins that the walk stays in bounds.
	{name: "ansible_sample", subRepos: []string{"."}},
	{name: "ruby_sample", subRepos: []string{"."}},
	{name: "swift_sample", subRepos: []string{"."}},
	{name: "kotlin_sample", subRepos: []string{"."}},
	{name: "rust_sample", subRepos: []string{"."}},
	{name: "java_sample", subRepos: []string{"."}},
	{name: "cpp_sample", subRepos: []string{"."}},
	{name: "php_sample", subRepos: []string{"."}},
	{name: "php_laravel_sample", subRepos: []string{"."}},
	{name: "php_symfony_sample", subRepos: []string{"."}},
	{name: "openapi_sample", subRepos: []string{"."}},
	{name: "multirepo", subRepos: []string{"repoA", "repoB"}},
	{name: "php_multirepo", subRepos: []string{"provider", "consumer"}},
	{name: "go_grpc_multirepo", subRepos: []string{"server", "client"}},
	// A Go backend plus a Go client that calls it and two third-party APIs. Pins
	// GAP-LK-02 (v101): a `baseURL + "/path"` concat to a hardcoded host is tagged
	// external, a hardcoded INTERNAL host still resolves to its loaded repo, and a
	// config-injected base URL stays an unresolved internal edge.
	{name: "go_httpclient_multirepo", subRepos: []string{"api", "consumer"}},
	// Two Go services coupled ONLY by Kafka topics — no import, no call, no HTTP
	// route between them. Pins the async linking signal (v132) end-to-end, which the
	// unit tests cannot: the topic name the extractor emits has to resolve against
	// the repo label the ENGINE assigns (the directory basename). Covers all four
	// outcomes — a consumed topic owned by a loaded repo draws the edge (including
	// one the producer declares no fact for, so it resolves from the consumer side
	// alone), an own topic and an unowned topic draw nothing, and an in-process event
	// bus emits no topic fact at all.
	{name: "go_kafka_multirepo", subRepos: []string{"svc-orders", "svc-billing"}},
	{name: "py_grpc_multirepo", subRepos: []string{"server", "client"}},
	// A FastAPI backend with its own frontend, beside an API-compatible rewrite
	// that also publishes npm packages under an @acme scope. Pins v133 end-to-end,
	// which the unit tests cannot: (a) routes declared on a factory-built router
	// resolve to their mounted path ("/api/v1/search/results", not "/results"),
	// (b) the frontend's calls bind to the backend IN ITS OWN REPO rather than to
	// the rewrite that serves the same shapes, and (c) acme-rs importing
	// "@acme/native-darwin-arm64" — a package under the scope it publishes itself —
	// draws no import edge to the repo labeled "acme". Both cross-repo edges here
	// are false positives the linker used to emit; the golden pins their absence.
	{name: "py_fastapi_multirepo", subRepos: []string{"acme", "acme-rs"}},
	// Two different-language repos sharing only nested type names. The linker must
	// draw no shared_symbols edge between them; see GAP-LK-03.
	{name: "kotlin_swift_multirepo", subRepos: []string{"android", "ios"}},
	// A Spring backend plus a Java consumer calling it through BOTH hand-written
	// client forms — RestTemplate (source="java-http-client") and @FeignClient
	// (source="feign"). Pins the contract-vocabulary fix that no unit test could:
	// the cross-repo linker kept its own private copy of the hand-written client
	// source set and had never included either Java value, so every Java call site
	// linked as via="http" — indistinguishable from an edge merely implied by an
	// OpenAPI spec. The golden now pins via=["http-client"] end to end, through the
	// real extractor rather than synthetic facts. java_sample cannot cover this:
	// it is single-repo, and a single-repo snapshot draws no cross-repo edge at
	// all, which is exactly why the omission survived undetected.
	{name: "java_httpclient_multirepo", subRepos: []string{"inventory", "storefront"}},
	// A decorator-routed TypeScript backend plus an SDK that calls it. Pins v142 end
	// to end, which the unit tests cannot: the server routes the @Controller classes
	// compose to have to RESOLVE against the SDK's client calls and draw a cross-repo
	// edge. Before v142 TypeScript had no server-side route DSL at all, so the api
	// repo emitted zero routes and was classified `isolated` while every SDK call sat
	// unresolved. Covers both argument forms (@Controller({path}) and
	// @Controller("…")), a bare @Get() serving the class path, the InversifyJS
	// vocabulary, and — by its absence from the golden — a verb decorator on a
	// non-controller class minting nothing.
	{name: "ts_nest_multirepo", subRepos: []string{"api", "sdk"}},
	// A call-routed Express server plus a consumer that calls it. Pins v143's three
	// rules, none of which a unit test can prove end to end: (a) receiver binding
	// separates registrations from v141's identically-shaped client calls, so no call
	// site is emitted twice and no client route is reclassified; (b) a sub-router
	// mounted in the SAME file composes ('/admin/users'), while one mounted from
	// another file emits nothing rather than a wrong fragment path ('/login'); and
	// (c) a bare catch-all is not an endpoint. The consumer's fourth call is served by
	// nobody, so it stays unresolved — the control that the linker is matching real
	// paths rather than accepting anything.
	{name: "ts_express_multirepo", subRepos: []string{"server", "consumer"}},
}

func TestGolden(t *testing.T) {
	for _, f := range fixtures {
		f := f
		t.Run(f.name, func(t *testing.T) {
			got := snapshotFixture(t, f)
			assertGolden(t, f.name, got)
		})
	}
}

// snapshotFixture copies the fixture repo into a temp dir, runs the full
// pipeline (append mode for multi-repo fixtures), and returns the normalized,
// deterministic JSONL of the resulting fact graph.
func snapshotFixture(t *testing.T, f fixture) []byte {
	t.Helper()

	root := copyTree(t, filepath.Join("testdata", "repos", f.name), t.TempDir())

	// Build the engine the same way production does, so the golden reflects the
	// real OSS plugin wiring. A non-existent config path falls back to defaults.
	eng, _, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: filepath.Join(t.TempDir(), "no-such-config.yaml"),
	})
	if err != nil {
		t.Fatalf("bootstrap.NewEngine: %v", err)
	}

	for i, sub := range f.subRepos {
		repoPath := root
		if sub != "." {
			repoPath = filepath.Join(root, sub)
		}
		appendMode := i > 0
		if _, err := eng.GenerateSnapshot(context.Background(), repoPath, appendMode); err != nil {
			t.Fatalf("GenerateSnapshot(%s, append=%v): %v", sub, appendMode, err)
		}
	}

	var buf bytes.Buffer
	if err := eng.Store().WriteJSONL(&buf); err != nil {
		t.Fatalf("WriteJSONL: %v", err)
	}
	return normalize(buf.Bytes(), root)
}

// normalize defends against any absolute temp path leaking into a fact by
// replacing the temp repo root with a stable placeholder. Facts use repo-
// relative paths today, so this is usually a no-op, but it keeps the golden
// machine-independent if that ever changes.
func normalize(b []byte, root string) []byte {
	out := strings.ReplaceAll(string(b), root, "<REPO>")
	return []byte(out)
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".facts.jsonl")

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/engine -run TestGolden -update` to create it): %v", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("golden mismatch for %s; run `go test ./internal/engine -run TestGolden -update` and review the diff.\n%s",
			name, firstDiff(want, got))
	}
}

// firstDiff returns a short, human-readable description of the first differing
// line between want and got, so failures point at the regression directly
// instead of dumping the entire fact graph.
func firstDiff(want, got []byte) string {
	wl := strings.Split(string(want), "\n")
	gl := strings.Split(string(got), "\n")
	n := len(wl)
	if len(gl) < n {
		n = len(gl)
	}
	for i := 0; i < n; i++ {
		if wl[i] != gl[i] {
			return "first diff at line " + itoa(i+1) + ":\n  - want: " + wl[i] + "\n  + got:  " + gl[i]
		}
	}
	if len(wl) != len(gl) {
		return "line count differs: want " + itoa(len(wl)) + " got " + itoa(len(gl))
	}
	return "(no line-level diff found)"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// copyTree recursively copies src into a fresh subdir of dstParent and returns
// the new root. Each fixture is copied per-test so the pipeline (and the MCP
// generate_snapshot handler, which writes .enola/) never touches the source
// tree under version control.
func copyTree(t *testing.T, src, dstParent string) string {
	t.Helper()
	dst := filepath.Join(dstParent, filepath.Base(src))
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read fixture dir %s: %v", src, err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		if e.IsDir() {
			copyTree(t, s, dst)
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("read %s: %v", s, err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", filepath.Join(dst, e.Name()), err)
		}
	}
	return dst
}
