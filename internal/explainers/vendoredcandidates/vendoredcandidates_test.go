package vendoredcandidates

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// storeOf builds a store whose facts sit at the given files, one module fact each.
func storeOf(files ...string) *facts.Store {
	s := facts.NewStore()
	for _, f := range files {
		s.Add(facts.Fact{
			Kind:  facts.KindModule,
			Name:  f,
			File:  f,
			Props: map[string]any{"language": "cpp"},
		})
	}
	return s
}

func explain(t *testing.T, store *facts.Store, walked []string) []facts.Insight {
	t.Helper()
	got, err := New().ExplainFiles(context.Background(), store, walked)
	if err != nil {
		t.Fatalf("ExplainFiles: %v", err)
	}
	return got
}

// The candidate reported is the LICENSED directory, never the conventionally named
// parent holding it. This is the correction that matters: a container routinely
// mixes dependencies with the repository's own code, and naming the container
// would tell the reader to exclude both together.
func TestReportsTheLicensedProjectNotTheContainer(t *testing.T) {
	walked := []string{
		"contrib/Netgen/LICENSE",
		"contrib/Netgen/libsrc/meshing.cpp",
		"contrib/Netgen/libsrc/geom.cpp",
		"contrib/Netgen/libsrc/opti.cpp",
		"contrib/mobile/main.cpp",
		"contrib/MeshOptimizer/opt.cpp",
		"contrib/domhex/surface.cpp",
	}
	store := storeOf(
		"contrib/Netgen/libsrc/meshing.cpp", "contrib/Netgen/libsrc/geom.cpp",
		"contrib/Netgen/libsrc/opti.cpp",
		"contrib/mobile/main.cpp", "contrib/MeshOptimizer/opt.cpp", "contrib/domhex/surface.cpp",
	)
	got := explain(t, store, walked)
	if len(got) != 1 {
		t.Fatalf("got %d insights, want 1", len(got))
	}
	body := got[0].Title + " " + strings.Join(got[0].Actions, " ")
	for _, ev := range got[0].Evidence {
		body += " " + ev.File + " " + ev.Detail
	}
	if !strings.Contains(body, "contrib/Netgen") {
		t.Errorf("licensed project not reported: %s", body)
	}
	for _, own := range []string{"contrib/mobile", "contrib/MeshOptimizer", "contrib/domhex"} {
		if strings.Contains(body, own) {
			t.Errorf("reported %s, which is the repository's own code: %s", own, body)
		}
	}
	if strings.Contains(strings.Join(got[0].Actions, " "), "\"contrib/**\"") {
		t.Errorf("suggested ignoring the whole container, which would exclude first-party code")
	}
}

// A licensed directory with a conventional NAME but no conventional parent is the
// repository's own — gitea's contrib/ holds the project LICENSE beside its own Go.
func TestLicenceOnTheContainerItselfIsNotACandidate(t *testing.T) {
	walked := []string{"contrib/LICENSE", "contrib/pr/checkout.go", "contrib/pr/run.go", "contrib/fixtures/x.go"}
	if got := explain(t, storeOf(walked[1:]...), walked); len(got) != 0 {
		t.Errorf("got %d insights, want 0 — a licensed contrib/ is not vendoring", len(got))
	}
}

// First-party packages whose names collide with vendoring conventions must never
// be reported. Each of these is a real directory from a public repository.
func TestFirstPartyNamesAreNotReported(t *testing.T) {
	for _, files := range [][]string{
		{"modules/markup/external/external.go", "modules/markup/external/render.go", "modules/markup/external/opts.go"},
		{"server/pkg/external/client.go", "server/pkg/external/auth.go", "server/pkg/external/api.go"},
		{"lib/gitlab/ci/config/external/file.rb", "lib/gitlab/ci/config/external/mapper.rb", "lib/gitlab/ci/config/external/processor.rb"},
		{"airflow/ti_deps/deps/base_ti_dep.py", "airflow/ti_deps/deps/dag_ti_slots.py", "airflow/ti_deps/deps/pool_slots.py"},
	} {
		if got := explain(t, storeOf(files...), files); len(got) != 0 {
			t.Errorf("reported first-party code as vendored: %v -> %d insights", files[0], len(got))
		}
	}
}

// A grouping directory between the convention and the project is the layout real
// trees use, so the ancestor search goes two levels up.
func TestGroupedLayoutIsFound(t *testing.T) {
	walked := []string{
		"third_party/packages/some-lib/LICENSE",
		"third_party/packages/some-lib/lib/a.dart",
		"third_party/packages/some-lib/lib/b.dart",
		"third_party/packages/some-lib/lib/c.dart",
	}
	got := explain(t, storeOf(walked[1:]...), walked)
	if len(got) != 1 {
		t.Fatalf("got %d insights, want 1", len(got))
	}
	if !strings.Contains(strings.Join(got[0].Actions, " "), "third_party/packages/some-lib/**") {
		t.Errorf("ignore glob missing or wrong: %v", got[0].Actions)
	}
}

// The finding must never be able to fail a build. It is a hint about somebody's
// directory layout, and an exclusion decision belongs to the reader.
func TestFindingIsInformationalAndExcludesNothing(t *testing.T) {
	walked := []string{"external/brotli/LICENSE", "external/brotli/a.c", "external/brotli/b.c", "external/brotli/c.c"}
	got := explain(t, storeOf(walked[1:]...), walked)
	if len(got) != 1 {
		t.Fatalf("got %d insights, want 1", len(got))
	}
	if !got[0].Informational {
		t.Errorf("finding is not Informational — it could set a CI exit code")
	}
	if !strings.Contains(got[0].Description, "Nothing has been excluded") {
		t.Errorf("description does not state that nothing was excluded: %q", got[0].Description)
	}
}

// Without the walked names there is no licence to see, so the explainer says
// nothing rather than guessing from paths alone.
func TestPlainExplainSaysNothing(t *testing.T) {
	got, err := New().Explain(context.Background(), storeOf("external/brotli/a.c"))
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d insights without walked names, want 0", len(got))
	}
}

// A licensed directory holding almost nothing is not worth a decision either way.
func TestTinyDirectoriesAreNotReported(t *testing.T) {
	walked := []string{"external/tiny/LICENSE", "external/tiny/a.c"}
	if got := explain(t, storeOf("external/tiny/a.c"), walked); len(got) != 0 {
		t.Errorf("got %d insights for a 1-file directory, want 0", len(got))
	}
}

// Every candidate is listed. A capped report would be the same failure as a silent
// exclusion, only quieter: code missing with no way to know what was left out.
func TestEveryCandidateIsListed(t *testing.T) {
	var walked, indexed []string
	const n = 40
	for i := 0; i < n; i++ {
		d := "third_party/lib" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		walked = append(walked, d+"/LICENSE")
		for _, f := range []string{"/a.c", "/b.c", "/c.c"} {
			walked = append(walked, d+f)
			indexed = append(indexed, d+f)
		}
	}
	got := explain(t, storeOf(indexed...), walked)
	if len(got) != 1 {
		t.Fatalf("got %d insights, want 1", len(got))
	}
	if len(got[0].Evidence) != n {
		t.Errorf("listed %d candidates, want all %d", len(got[0].Evidence), n)
	}
}
