package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_ValidDeclaration(t *testing.T) {
	d, err := Parse([]byte(`
service:
  name: payments
consumes:
  - repo: billing
    via: http-client
  - repo: analytics
    via: graphql
serves:
  - via: http
layers:
  - name: handlers
    paths: ["app/controllers/**"]
  - name: domain
    paths: ["app/models/**"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Service.Name != "payments" || len(d.Consumes) != 2 || len(d.Layers) != 2 {
		t.Fatalf("parsed = %+v", d)
	}
}

func TestParse_FreeFormViaIsAnError(t *testing.T) {
	_, err := Parse([]byte("consumes:\n  - repo: billing\n    via: rest\n"))
	if err == nil {
		t.Fatal("a via the linker does not define must be a parse error")
	}
	if !strings.Contains(err.Error(), "graphql") || !strings.Contains(err.Error(), "http-client") {
		t.Fatalf("the error must name the allowed set, got: %v", err)
	}
}

func TestParse_LayerShapeValidated(t *testing.T) {
	if _, err := Parse([]byte("layers:\n  - name: handlers\n")); err == nil {
		t.Fatal("a layer without paths must be a parse error")
	}
	if _, err := Parse([]byte("layers:\n  - paths: [\"a/**\"]\n")); err == nil {
		t.Fatal("a layer without a name must be a parse error")
	}
}

func TestLoadRepoFile_MissingIsNil(t *testing.T) {
	d, err := LoadRepoFile(t.TempDir())
	if err != nil || d != nil {
		t.Fatalf("missing file = (%v, %v), want (nil, nil)", d, err)
	}
}

func TestLoadRepoFile_InvalidIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RepoFileName), []byte("consumes:\n  - repo: x\n    via: bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRepoFile(dir); err == nil {
		t.Fatal("a present-but-invalid declaration must error, never silently skip")
	}
}

func TestResolve_ClusterOverridesWholesale(t *testing.T) {
	file := &Declaration{Consumes: []Seam{{Repo: "a", Via: "http"}, {Repo: "b", Via: "kafka"}}, Source: "repo/enola-intent.yaml"}
	cluster := &Declaration{Consumes: []Seam{{Repo: "c", Via: "graphql"}}}
	got := Resolve(file, cluster)
	if !got.Overridden || got.Source != ClusterSource {
		t.Fatalf("override not recorded: %+v", got)
	}
	if len(got.Consumes) != 1 || got.Consumes[0].Repo != "c" {
		t.Fatalf("override must be wholesale, never key-merged: %+v", got.Consumes)
	}
	if only := Resolve(file, nil); only != file || only.Overridden {
		t.Fatalf("file-only resolution changed the declaration: %+v", only)
	}
	if only := Resolve(nil, cluster); only.Overridden || only.Source != ClusterSource {
		t.Fatalf("cluster-only resolution mis-recorded: %+v", only)
	}
}

// A claim's compiled name is what a failed-claim finding is titled with, so an
// absent optional prefix must not leave a gap in it.
func TestClaimNames_OmitAbsentPrefixes(t *testing.T) {
	v := 99
	for _, tc := range []struct {
		name string
		in   Claim
		want string
	}{
		{"no prefixes", Claim{Metric: "fact-count", Repo: "api", Kind: "route", Value: &v},
			"claim: api route = 99"},
		{"name prefix", Claim{Metric: "fact-count", Repo: "api", Kind: "route", NamePrefix: "/api", Value: &v},
			"claim: api route /api = 99"},
		{"file prefix", Claim{Metric: "fact-count", Repo: "api", Kind: "symbol", FilePrefix: "app/", Value: &v},
			"claim: api symbol app/ = 99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ff := CompilePageFacts(&PageIntent{Claims: []Claim{tc.in}}, "wiki/p.md")
			if len(ff) != 1 || ff[0].Name != tc.want {
				t.Fatalf("name = %q, want %q", ff[0].Name, tc.want)
			}
		})
	}
}
