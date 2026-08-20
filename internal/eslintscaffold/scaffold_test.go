package eslintscaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/mining"
)

func classSymbol(name, file string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Props: map[string]any{"symbol_kind": facts.SymbolClass}}
}

func moduleFact(name string) facts.Fact {
	return facts.Fact{Kind: facts.KindModule, Name: name, File: name + ".ts"}
}

func storeOf(ff []facts.Fact) *facts.Store {
	st := facts.NewStore()
	for _, f := range ff {
		st.Add(f)
	}
	return st
}

// A naming regularity over TypeScript classes and a forbidden import edge
// between two directories, plus a Ruby naming regularity and a depends-on edge
// that the graph resolved: the first two scaffold, the other two are left with a
// reason.
func scaffoldWorld() []facts.Fact {
	words := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel", "India", "Juliett", "Kilo", "Lima"}
	var ff []facts.Fact
	for _, w := range words {
		ff = append(ff, classSymbol(w+"Service", "app/services/"+strings.ToLower(w)+".ts"))
		ff = append(ff, classSymbol(w+"Serializer", "app/serializers/"+strings.ToLower(w)+"_serializer.rb"))
	}
	ff = append(ff, classSymbol("Helper", "app/services/helper.ts"))
	ff = append(ff, classSymbol("Widget", "app/serializers/widget.rb"))
	for i := 0; i < 19; i++ {
		ff = append(ff, moduleFact(fmt.Sprintf("app/models/model_%02d", i)))
	}
	ff = append(ff, moduleFact("app/jobs/job_x"))
	for i := 0; i < 20; i++ {
		target := fmt.Sprintf("app/models/model_%02d", i)
		if i == 19 {
			target = "app/jobs/job_x"
		}
		dep := facts.Fact{Kind: facts.KindDependency, Name: fmt.Sprintf("app/components -> %s", target), File: fmt.Sprintf("app/components/c_%02d.ts", i), Relations: []facts.Relation{{Kind: facts.RelImports, Target: target}}}
		ff = append(ff, dep)
		svc := classSymbol(fmt.Sprintf("Svc%02d", i), fmt.Sprintf("app/workers/svc_%02d.rb", i))
		svc.Relations = []facts.Relation{{Kind: facts.RelDependsOn, Target: target}}
		ff = append(ff, svc)
	}
	return ff
}

func find(t *testing.T, res Result, ruleID string) Scaffold {
	t.Helper()
	for _, sc := range res.Scaffolds {
		if sc.RuleID == ruleID {
			return sc
		}
	}
	var ids []string
	for _, sc := range res.Scaffolds {
		ids = append(ids, sc.RuleID)
	}
	t.Fatalf("no scaffold %q; have %v", ruleID, ids)
	return Scaffold{}
}

func TestRender_ScaffoldsFileLocalFamiliesAndLeavesTheRest(t *testing.T) {
	report := mining.Mine(storeOf(scaffoldWorld()), mining.DefaultConfig())
	res := Render(report.Candidates)

	naming := find(t, res, "mined-app-services-symbol-named-service")
	for _, want := range []string{"const CLUSTER = 'app/services';", "const SUFFIX = 'Service';", "ClassDeclaration: check", "FunctionDeclaration: check", "VariableDeclarator: check"} {
		if !strings.Contains(naming.RuleFile, want) {
			t.Errorf("naming rule lacks %q", want)
		}
	}
	for _, want := range []string{"name: 'AlphaService conforms'", "filename: repoFile('app/services/alpha.ts')", "name: 'Helper is the recorded exception'", "messageId: 'nameOutsidePattern', data: { name: 'Helper' }", "a file outside app/services/ is not checked"} {
		if !strings.Contains(naming.TestFile, want) {
			t.Errorf("naming test lacks %q", want)
		}
	}

	forbid := find(t, res, "mined-forbid-app-components-to-app-jobs-imports")
	for _, want := range []string{"const SOURCE_CLUSTER = 'app/components';", "const TARGET_CLUSTER = 'app/jobs';", "ImportDeclaration(node)"} {
		if !strings.Contains(forbid.RuleFile, want) {
			t.Errorf("forbid rule lacks %q", want)
		}
	}
	for _, want := range []string{"filename: repoFile('app/components/c_19.ts')", `import dep from "app/jobs/job_x";`, "messageId: 'forbiddenImport', data: { specifier: 'app/jobs/job_x' }", `import dep from "app/models/model_00";`} {
		if !strings.Contains(forbid.TestFile, want) {
			t.Errorf("forbid test lacks %q", want)
		}
	}

	reasons := map[string]string{}
	for _, sk := range res.Skipped {
		reasons[sk.Identity] = sk.Reason
	}
	wantSkips := map[string]string{
		"naming\x1fapp/serializers":     "not JavaScript or TypeScript (.rb)",
		"forbid-edge\x1fapp/workers":    "depends_on edge is resolved through the graph",
		"forbid-edge\x1fapp/components": "",
	}
	for _, sk := range res.Skipped {
		for key, want := range wantSkips {
			family, cluster, _ := strings.Cut(key, "\x1f")
			if strings.HasPrefix(sk.Identity, family+"|") && strings.Contains(sk.Identity, cluster) {
				if want == "" {
					t.Errorf("%s was skipped (%s) but should scaffold", sk.Identity, sk.Reason)
				} else if !strings.Contains(sk.Reason, want) {
					t.Errorf("%s skipped for %q, want reason containing %q", sk.Identity, sk.Reason, want)
				}
			}
		}
	}
	if len(res.Skipped) == 0 {
		t.Fatal("nothing was skipped; the Ruby naming and the depends_on edge should have been")
	}
	sawRuby, sawGraphEdge := false, false
	for _, sk := range res.Skipped {
		sawRuby = sawRuby || strings.Contains(sk.Reason, "(.rb)")
		sawGraphEdge = sawGraphEdge || strings.Contains(sk.Reason, "depends_on edge is resolved through the graph")
	}
	if !sawRuby || !sawGraphEdge {
		t.Errorf("skip reasons missing: ruby=%v graphEdge=%v: %+v", sawRuby, sawGraphEdge, res.Skipped)
	}
}

func TestWrite_LaysOutAPluginDirectory(t *testing.T) {
	report := mining.Mine(storeOf(scaffoldWorld()), mining.DefaultConfig())
	dir := t.TempDir()
	res, written, err := Write(dir, report.Candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 2*len(res.Scaffolds)+1 {
		t.Fatalf("wrote %d files for %d scaffolds", len(written), len(res.Scaffolds))
	}
	index, err := os.ReadFile(filepath.Join(dir, "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sc := range res.Scaffolds {
		if !strings.Contains(string(index), "'"+sc.RuleID+"': require('./"+sc.RuleID+".js')") {
			t.Errorf("index.js does not register %s", sc.RuleID)
		}
		if _, err := os.Stat(filepath.Join(dir, "tests", sc.RuleID+".test.js")); err != nil {
			t.Errorf("test for %s not written: %v", sc.RuleID, err)
		}
	}
}
