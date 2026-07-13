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
	{name: "ruby_sample", subRepos: []string{"."}},
	{name: "swift_sample", subRepos: []string{"."}},
	{name: "kotlin_sample", subRepos: []string{"."}},
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
	{name: "py_grpc_multirepo", subRepos: []string{"server", "client"}},
	// Two different-language repos sharing only nested type names. The linker must
	// draw no shared_symbols edge between them; see GAP-LK-03.
	{name: "kotlin_swift_multirepo", subRepos: []string{"android", "ios"}},
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
