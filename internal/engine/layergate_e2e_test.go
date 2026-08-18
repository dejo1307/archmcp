package engine_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/explainers/layers"
	"github.com/enola-labs/enola/internal/extractors/tsextractor"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/check"
)

// The whole chain issue #242 reported as broken, exercised end to end: declare a
// layer order, pin a baseline, introduce an inner→outer import, and assert that
// `check --fail-on=layers` exits non-zero.
//
// The reporter could not get this to bite, and concluded declared-order enforcement
// might not be implemented. It is — what was broken was the step before it, where
// their module names carried host separators and their import targets did not, so
// nothing classified and there was no module→module edge left for the rule to grade.
// A unit test on the explainer cannot show that, because the failure was in how the
// two halves MET. This drives the real engine, the real diff and the real gate.
//
// The declaration below is written with BACKSLASHES on purpose. It is the reporter's
// own workaround — they generated exact host-separator paths to get anything to
// classify at all — and it has to work on every host, not just the one that wrote it.

const layerGateIntent = `service:
  name: layered
layers:
  - name: web-components
    paths: ['src\components\**']
  - name: web-lib
    paths: ['src\lib\**']
`

func TestLayerGate_InnerImportingOuterFailsTheBuild(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "package.json"), `{"name":"layered","version":"1.0.0"}`)
	// tsconfig.json is what makes the TypeScript extractor claim the tree.
	writeFile(t, filepath.Join(repo, "tsconfig.json"), `{"compilerOptions":{"baseUrl":"."}}`)
	writeFile(t, filepath.Join(repo, "enola-intent.yaml"), layerGateIntent)
	writeFile(t, filepath.Join(repo, "src", "components", "site.ts"),
		"export type SiteBlock = { id: string };\nexport function render(b: SiteBlock): string { return b.id; }\n")
	writeFile(t, filepath.Join(repo, "src", "lib", "blocks.ts"),
		"export function blockId(x: string): string { return x.trim(); }\n")

	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(tsextractor.New())
	eng.RegisterExplainer(layers.New())

	ctx := context.Background()

	// 1. Clean snapshot, and the declaration must actually govern something. This
	//    assertion is the one that would have caught the reported bug: on Windows it
	//    classified zero modules while every other step reported success.
	base, err := eng.GenerateSnapshot(ctx, repo, false)
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	declared := findByTitle(base.Insights, "Architecture pattern: declared")
	if declared == nil {
		t.Fatalf("no declared layer pattern: %+v", base.Insights)
	}
	if !strings.Contains(declared.Description, "2 classified modules") {
		t.Fatalf("the declaration governs nothing, so nothing downstream can fail: %s", declared.Description)
	}
	if vacuous := findByTitle(base.Insights, "classifies no modules"); vacuous != nil {
		t.Fatalf("every declared layer has members here: %+v", vacuous)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}
	if err := eng.SetBaseline(repo); err != nil {
		t.Fatalf("set baseline: %v", err)
	}

	// 2. The regression: web-lib is the innermost layer and now reaches up into
	//    web-components.
	writeFile(t, filepath.Join(repo, "src", "lib", "blocks.ts"),
		"import type { SiteBlock } from \"../components/site\";\n"+
			"export function blockId(x: string): string { return x.trim(); }\n"+
			"export function empty(): SiteBlock[] { return []; }\n")

	cur, err := eng.GenerateSnapshot(ctx, repo, false)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts 2: %v", err)
	}
	if v := findByTitle(cur.Insights, "Layer violation"); v == nil {
		t.Fatalf("inner-imports-outer must be a violation: %+v", cur.Insights)
	}

	// 3. Grade the delta exactly as `enola check --fail-on=layers` does.
	baseDir := filepath.Join(eng.OutputDir(repo), engine.BaselineSubdir)
	baseline, err := engine.LoadSnapshotDir(baseDir)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	current := &facts.Snapshot{Meta: cur.Meta, Facts: eng.Store().All(), Insights: cur.Insights}
	d := diff.Compute(baseline, current)

	verdict := check.Evaluate(d, check.Policy{FailExplainers: []string{"layers"}})
	if verdict.ExitCode() == 0 {
		t.Fatalf("--fail-on=layers exited 0 on a new layer violation.\n"+
			"failures=%+v\nadvisories=%+v\ndescriptive=%+v\nincidental=%+v",
			verdict.Failures, verdict.Advisories, verdict.Descriptive, verdict.Incidental)
	}
	if got := findByTitle(verdict.Failures, "Layer violation"); got == nil {
		t.Fatalf("the violation must be the FAILURE, not merely reported: %+v", verdict)
	}
}

// The other half of the contract, and the reason the vacuous advisory sits below the
// gate's confidence floor: the pull request that DECLARES a layer order must not fail
// on it — including when the order it declares matches nothing, which is the most
// likely state of a declaration on the day it is written.
func TestLayerGate_DeclaringAnOrderNeverFailsTheBuild(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "package.json"), `{"name":"layered","version":"1.0.0"}`)
	// tsconfig.json is what makes the TypeScript extractor claim the tree.
	writeFile(t, filepath.Join(repo, "tsconfig.json"), `{"compilerOptions":{"baseUrl":"."}}`)
	// Real modules, so the repo is one a layer order COULD govern. The advisory is
	// about a declaration that matches nothing, not about a repo with nothing in it.
	writeFile(t, filepath.Join(repo, "src", "components", "site.ts"),
		"export type SiteBlock = { id: string };\nexport function render(b: SiteBlock): string { return b.id; }\n")
	writeFile(t, filepath.Join(repo, "src", "lib", "blocks.ts"),
		"export function blockId(x: string): string { return x.trim(); }\n")

	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(tsextractor.New())
	eng.RegisterExplainer(layers.New())

	ctx := context.Background()
	if _, err := eng.GenerateSnapshot(ctx, repo, false); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}
	if err := eng.SetBaseline(repo); err != nil {
		t.Fatalf("set baseline: %v", err)
	}

	// The change under test IS the declaration — and it names a tree that does not
	// exist, so it classifies nothing.
	writeFile(t, filepath.Join(repo, "enola-intent.yaml"),
		"layers:\n  - {name: outer, paths: [\"app/handlers/**\"]}\n  - {name: inner, paths: [\"app/domain/**\"]}\n")

	cur, err := eng.GenerateSnapshot(ctx, repo, false)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if err := eng.WriteArtifacts(repo); err != nil {
		t.Fatalf("write artifacts 2: %v", err)
	}

	// It must be REPORTED — that is the fix for the silence.
	if findByTitle(cur.Insights, "classifies no modules") == nil {
		t.Fatalf("a declaration matching nothing must say so: %+v", cur.Insights)
	}

	baseDir := filepath.Join(eng.OutputDir(repo), engine.BaselineSubdir)
	baseline, err := engine.LoadSnapshotDir(baseDir)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	current := &facts.Snapshot{Meta: cur.Meta, Facts: eng.Store().All(), Insights: cur.Insights}
	d := diff.Compute(baseline, current)

	// ...and must not fail the build that reported it.
	verdict := check.Evaluate(d, check.Policy{FailExplainers: []string{"layers"}})
	if verdict.ExitCode() != 0 {
		t.Fatalf("declaring a layer order failed its own pull request: %+v", verdict.Failures)
	}
}

func findByTitle(insights []facts.Insight, substr string) *facts.Insight {
	for i := range insights {
		if strings.Contains(insights[i].Title, substr) {
			return &insights[i]
		}
	}
	return nil
}
