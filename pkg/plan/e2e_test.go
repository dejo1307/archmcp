package plan_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	constraintsexp "github.com/enola-labs/enola/internal/explainers/constraints"
	"github.com/enola-labs/enola/internal/extractors/goextractor"
	"github.com/enola-labs/enola/pkg/plan"
)

const fixtureIntent = `components:
  - name: domain
    match: [pkg/domain/**]
  - name: adapters
    match: [pkg/adapters/**]
rules:
  - id: no-io-in-domain
    forbid: domain
    to: adapters
    via: imports
    because: the domain stays io-free; adapters call it, never the reverse
`

const cleanDomain = "package domain\n\nfunc Answer() int {\n\treturn 42\n}\n"

const violatingDomain = "package domain\n\nimport \"fixture/pkg/adapters\"\n\nfunc Answer() int {\n\tadapters.Fetch()\n\treturn 42\n}\n"

const violatingPatch = "--- a/pkg/domain/domain.go\n" +
	"+++ b/pkg/domain/domain.go\n" +
	"@@ -1,5 +1,8 @@\n" +
	" package domain\n" +
	" \n" +
	"+import \"fixture/pkg/adapters\"\n" +
	"+\n" +
	" func Answer() int {\n" +
	"+\tadapters.Fetch()\n" +
	" \treturn 42\n" +
	" }\n"

const resolvingPatch = "--- a/pkg/domain/domain.go\n" +
	"+++ b/pkg/domain/domain.go\n" +
	"@@ -1,8 +1,5 @@\n" +
	" package domain\n" +
	" \n" +
	"-import \"fixture/pkg/adapters\"\n" +
	"-\n" +
	" func Answer() int {\n" +
	"-\tadapters.Fetch()\n" +
	" \treturn 42\n" +
	" }\n"

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixtureRepo(t *testing.T, domainSource string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "fixture")
	write(t, filepath.Join(repo, "go.mod"), "module fixture\n\ngo 1.21\n")
	write(t, filepath.Join(repo, "enola-intent.yaml"), fixtureIntent)
	write(t, filepath.Join(repo, "pkg", "domain", "domain.go"), domainSource)
	write(t, filepath.Join(repo, "pkg", "adapters", "adapters.go"), "package adapters\n\nfunc Fetch() {}\n")
	return repo
}

func fixtureFactory(t *testing.T) plan.EngineFactory {
	t.Helper()
	return func() (plan.Generator, error) {
		eng, err := engine.New(config.Default())
		if err != nil {
			return nil, err
		}
		eng.RegisterExtractor(goextractor.New())
		eng.RegisterExplainer(constraintsexp.New())
		eng.SetPersistCache(false)
		return eng, nil
	}
}

func fixtureDeps(t *testing.T, repo string, factory plan.EngineFactory) plan.Deps {
	t.Helper()
	gen, err := factory()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := gen.GenerateSnapshot(context.Background(), repo, false)
	if err != nil {
		t.Fatalf("snapshotting the fixture: %v", err)
	}
	store, err := plan.ContractStore(repo, snap.Facts, nil)
	if err != nil {
		t.Fatalf("ContractStore: %v", err)
	}
	return plan.Deps{
		RepoPath:      repo,
		RepoLabel:     filepath.Base(repo),
		Store:         store,
		OutputDirName: ".enola",
		NewEngine:     factory,
	}
}

func hashTree(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	var entries []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, filepath.ToSlash(rel)+":"+hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	for _, e := range entries {
		h.Write([]byte(e + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestE2E_CounterfactualVerdictBeforeAnyEditExists(t *testing.T) {
	repo := fixtureRepo(t, cleanDomain)
	deps := fixtureDeps(t, repo, fixtureFactory(t))
	before := hashTree(t, repo)

	report, err := plan.Compute(context.Background(), plan.Request{Patch: []byte(violatingPatch)}, deps)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if after := hashTree(t, repo); after != before {
		t.Fatal("the plan call mutated the target tree — the counterfactual must run over a scratch copy only")
	}
	if _, err := os.Stat(filepath.Join(repo, ".enola")); !os.IsNotExist(err) {
		t.Fatal("the plan call wrote a .enola directory into the target repo")
	}

	cf := report.Counterfactual
	if cf == nil {
		t.Fatal("no counterfactual in the report")
	}
	if !cf.ConstraintsDeclared {
		t.Fatal("counterfactual reports constraints undeclared on a declared fixture")
	}
	if len(cf.New) != 1 {
		t.Fatalf("new verdicts = %d, want exactly 1: %+v", len(cf.New), cf.New)
	}
	verdict := cf.New[0]
	if verdict.Rule != "no-io-in-domain" {
		t.Errorf("verdict names rule %q, want no-io-in-domain", verdict.Rule)
	}
	if !strings.Contains(verdict.Title, "no-io-in-domain violated:") {
		t.Errorf("verdict title does not name the violated rule: %q", verdict.Title)
	}
	if !strings.Contains(verdict.Because, "io-free") {
		t.Errorf("verdict lost the declared because: %q", verdict.Because)
	}
	witnessed := false
	for _, ev := range verdict.Evidence {
		if strings.Contains(ev.File, "pkg/domain") || strings.Contains(ev.Symbol, "domain") {
			witnessed = true
		}
	}
	if !witnessed {
		t.Errorf("no witness names the domain side of the edge: %+v", verdict.Evidence)
	}
	if len(cf.Resolved) != 0 || len(cf.Unchanged) != 0 {
		t.Errorf("resolved/unchanged = %d/%d, want 0/0", len(cf.Resolved), len(cf.Unchanged))
	}

	governed := false
	for _, tr := range report.Targets {
		if tr.Target != "pkg/domain/domain.go" {
			continue
		}
		for _, c := range tr.Components {
			for _, rule := range c.Rules {
				if rule.Rule == "no-io-in-domain" {
					governed = true
				}
			}
		}
	}
	if !governed {
		t.Error("the patched path is not reported as governed by no-io-in-domain")
	}
}

func TestE2E_ResolvingPatchLandsInTheResolvedBucket(t *testing.T) {
	repo := fixtureRepo(t, violatingDomain)
	deps := fixtureDeps(t, repo, fixtureFactory(t))

	report, err := plan.Compute(context.Background(), plan.Request{Patch: []byte(resolvingPatch)}, deps)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	cf := report.Counterfactual
	if len(cf.Resolved) != 1 || cf.Resolved[0].Rule != "no-io-in-domain" {
		t.Fatalf("resolved = %+v, want exactly the no-io-in-domain breach", cf.Resolved)
	}
	if len(cf.New) != 0 {
		t.Errorf("new = %+v, want none", cf.New)
	}
}

func TestE2E_UnchangedViolationStaysInTheUnchangedBucket(t *testing.T) {
	repo := fixtureRepo(t, violatingDomain)
	deps := fixtureDeps(t, repo, fixtureFactory(t))
	patch := "--- /dev/null\n+++ b/pkg/domain/extra.go\n@@ -0,0 +1,3 @@\n+package domain\n+\n+func Extra() int { return 1 }\n"

	report, err := plan.Compute(context.Background(), plan.Request{Patch: []byte(patch)}, deps)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	cf := report.Counterfactual
	if len(cf.Unchanged) != 1 || cf.Unchanged[0].Rule != "no-io-in-domain" {
		t.Fatalf("unchanged = %+v, want exactly the pre-existing breach", cf.Unchanged)
	}
	if len(cf.New) != 0 || len(cf.Resolved) != 0 {
		t.Errorf("new/resolved = %d/%d, want 0/0", len(cf.New), len(cf.Resolved))
	}
}

func TestE2E_IdenticalPlanIsByteIdentical(t *testing.T) {
	repo := fixtureRepo(t, cleanDomain)
	deps := fixtureDeps(t, repo, fixtureFactory(t))

	first, err := plan.Compute(context.Background(), plan.Request{Patch: []byte(violatingPatch), Paths: []string{"pkg/domain/domain.go"}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	second, err := plan.Compute(context.Background(), plan.Request{Patch: []byte(violatingPatch), Paths: []string{"pkg/domain/domain.go"}}, deps)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := first.JSON()
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := second.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Errorf("identical plans produced different reports:\n%s\n---\n%s", firstJSON, secondJSON)
	}
	if first.Render() != second.Render() {
		t.Error("identical plans produced different text renders")
	}
}
