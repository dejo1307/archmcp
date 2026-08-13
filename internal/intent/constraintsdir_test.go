package intent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func writeConstraintsRepo(t *testing.T, inline string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if inline != "" {
		if err := os.WriteFile(filepath.Join(dir, RepoFileName), []byte(inline), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(files) > 0 {
		cdir := filepath.Join(dir, filepath.FromSlash(ConstraintsDirName))
		if err := os.MkdirAll(cdir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(cdir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

const constraintsInline = `
service:
  name: shop
components:
  - name: domain
    match: ["app/domain/**"]
rules:
  - id: domain-pure
    forbid: domain
    to: adapters
    via: imports
    because: the domain must not know its delivery mechanisms
`

const constraintsAdaptersFile = `
components:
  - name: adapters
    match: ["app/adapters/**"]
`

const constraintsBillingFile = `
components:
  - name: billing
    match: ["app/billing/**"]
rules:
  - id: billing-owned
    protect: billing
    owners: [domain]
    via: calls
    because: only the domain may drive billing
`

func TestLoadRepoFile_ConstraintsDirMergesAfterInline(t *testing.T) {
	dir := writeConstraintsRepo(t, constraintsInline, map[string]string{
		"adapters.yaml": constraintsAdaptersFile,
		"billing.yaml":  constraintsBillingFile,
	})
	d, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotComponents := []string{}
	for _, c := range d.Components {
		gotComponents = append(gotComponents, c.Name+"@"+c.SourceFile)
	}
	wantComponents := []string{
		"domain@",
		"adapters@" + ConstraintsDirName + "/adapters.yaml",
		"billing@" + ConstraintsDirName + "/billing.yaml",
	}
	if !reflect.DeepEqual(gotComponents, wantComponents) {
		t.Fatalf("merged components = %v, want inline first then sorted files: %v", gotComponents, wantComponents)
	}
	if len(d.Rules) != 2 || d.Rules[0].ID != "domain-pure" || d.Rules[0].SourceFile != "" {
		t.Fatalf("inline rule must stay first and unstamped: %+v", d.Rules)
	}
	if d.Rules[1].ID != "billing-owned" || d.Rules[1].SourceFile != ConstraintsDirName+"/billing.yaml" {
		t.Fatalf("file rule must carry its declaring file: %+v", d.Rules[1])
	}
	if d.Source != RepoFileName {
		t.Fatalf("a repo with an inline file stays sourced from it, got %q", d.Source)
	}
}

func TestLoadRepoFile_CrossFileComponentReferencesResolve(t *testing.T) {
	// The inline rule's to: names a component declared only in a constraints
	// file, and the billing rule's owners: names one declared only inline —
	// validation must run over the merged set, or splitting a declaration
	// into files would break every rule that crosses a file boundary.
	dir := writeConstraintsRepo(t, constraintsInline, map[string]string{
		"adapters.yaml": constraintsAdaptersFile,
		"billing.yaml":  constraintsBillingFile,
	})
	if _, err := LoadRepoFile(dir); err != nil {
		t.Fatalf("cross-file references must validate against the merged set: %v", err)
	}
}

func TestCompileFacts_ConstraintsFactsCiteDeclaringFile(t *testing.T) {
	dir := writeConstraintsRepo(t, constraintsInline, map[string]string{
		"billing.yaml":  constraintsBillingFile,
		"adapters.yaml": constraintsAdaptersFile,
	})
	d, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]facts.Fact{}
	for _, f := range CompileFacts(d) {
		byName[f.Name] = f
	}
	billing := byName["component: billing"]
	if billing.File != ConstraintsDirName+"/billing.yaml" || billing.Props["source"] != ConstraintsDirName+"/billing.yaml" {
		t.Fatalf("a directory-declared component must cite its file, got File=%q source=%v", billing.File, billing.Props["source"])
	}
	rule := byName["rule: billing-owned"]
	if rule.File != ConstraintsDirName+"/billing.yaml" || rule.Props["source"] != ConstraintsDirName+"/billing.yaml" {
		t.Fatalf("a directory-declared rule must cite its file, got File=%q source=%v", rule.File, rule.Props["source"])
	}
	inline := byName["component: domain"]
	if inline.File != RepoFileName || inline.Props["source"] != RepoFileName {
		t.Fatalf("an inline component keeps the declaration file, got File=%q source=%v", inline.File, inline.Props["source"])
	}
}

func TestLoadRepoFile_DuplicateRuleAcrossFilesNamesBothFiles(t *testing.T) {
	dir := writeConstraintsRepo(t, "", map[string]string{
		"a.yaml": "components:\n  - name: alpha\n    match: [\"a/**\"]\nrules:\n  - id: shared\n    forbid_fact: alpha\n    because: a\n",
		"b.yaml": "components:\n  - name: beta\n    match: [\"b/**\"]\nrules:\n  - id: shared\n    forbid_fact: beta\n    because: b\n",
	})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("one rule id in two constraints files must be an error")
	}
	if !strings.Contains(err.Error(), ConstraintsDirName+"/a.yaml") || !strings.Contains(err.Error(), ConstraintsDirName+"/b.yaml") {
		t.Fatalf("the error must name both declaring files, got: %v", err)
	}
}

func TestLoadRepoFile_DuplicateComponentAgainstInlineNamesBothFiles(t *testing.T) {
	dir := writeConstraintsRepo(t, constraintsInline, map[string]string{
		"adapters.yaml": constraintsAdaptersFile,
		"extra.yaml":    "components:\n  - name: domain\n    match: [\"elsewhere/**\"]\n",
	})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("re-declaring an inline component in a constraints file must be an error")
	}
	if !strings.Contains(err.Error(), ConstraintsDirName+"/extra.yaml") || !strings.Contains(err.Error(), RepoFileName) {
		t.Fatalf("the error must name the file and the inline declaration, got: %v", err)
	}
}

func TestLoadRepoFile_AbsentOrEmptyDirIsANoop(t *testing.T) {
	files, problems, err := LoadConstraintsDir(t.TempDir())
	if files != nil || problems != nil || err != nil {
		t.Fatalf("absent directory = (%v, %v, %v), want all nil", files, problems, err)
	}

	empty := writeConstraintsRepo(t, constraintsInline, map[string]string{"adapters.yaml": constraintsAdaptersFile})
	if err := os.Remove(filepath.Join(empty, filepath.FromSlash(ConstraintsDirName), "adapters.yaml")); err != nil {
		t.Fatal(err)
	}
	files, problems, err = LoadConstraintsDir(empty)
	if files != nil || problems != nil || err != nil {
		t.Fatalf("empty directory = (%v, %v, %v), want all nil", files, problems, err)
	}
	// An inline-only declaration still validates only against itself, so its
	// dangling adapters reference is now the error it always would have been.
	if _, err := LoadRepoFile(empty); err == nil || !strings.Contains(err.Error(), "adapters") {
		t.Fatalf("inline validation unchanged by an empty directory, got: %v", err)
	}
}

func TestLoadRepoFile_ConstraintsDirOnlyDeclaration(t *testing.T) {
	dir := writeConstraintsRepo(t, "", map[string]string{"adapters.yaml": constraintsAdaptersFile})
	d, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || d.Source != ConstraintsDirName {
		t.Fatalf("a constraints-only repo must resolve to a directory-sourced declaration, got %+v", d)
	}
	if len(d.Components) != 1 || d.Components[0].Name != "adapters" {
		t.Fatalf("components = %+v", d.Components)
	}
}

func TestLoadRepoFile_UnparseableConstraintsFileIsAnError(t *testing.T) {
	dir := writeConstraintsRepo(t, constraintsInline, map[string]string{
		"adapters.yaml": constraintsAdaptersFile,
		"broken.yaml":   "components: [\n",
	})
	_, err := LoadRepoFile(dir)
	if err == nil {
		t.Fatal("a present-but-unparseable constraints file must error, never silently skip")
	}
	if !strings.Contains(err.Error(), ConstraintsDirName+"/broken.yaml") {
		t.Fatalf("the error must cite the broken file, got: %v", err)
	}
}

func TestLoadRepoFile_ConstraintsDirIsDeterministic(t *testing.T) {
	dir := writeConstraintsRepo(t, constraintsInline, map[string]string{
		"billing.yaml":  constraintsBillingFile,
		"adapters.yaml": constraintsAdaptersFile,
	})
	first, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two loads of one repo differ:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if !reflect.DeepEqual(CompileFacts(first), CompileFacts(second)) {
		t.Fatal("two compilations of one repo differ")
	}
}
