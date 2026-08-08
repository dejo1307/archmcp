package cycles

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// mod builds a Scala module fact with a build-unit attribution.
func scalaMod(name, unit string) facts.Fact {
	p := map[string]any{"language": "scala"}
	if unit != "" {
		p["jvm_module"] = unit
	}
	return facts.Fact{Kind: facts.KindModule, Name: name, File: name, Props: p}
}

// dep mirrors what the Scala extractor emits: the importing module is derived from
// the dependency fact's FILE path, not from its name.
func dep(from, to string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindDependency, Name: from + " -> " + to, File: from + "/X.scala",
		Props:     map[string]any{"language": "scala", facts.PropSource: "internal"},
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: to}},
	}
}

// TestScalaCycleWithinOneModuleIsCoupling pins what the jvm_module prop buys. Scala
// imposes no package-level acyclicity inside a module, so a cycle there is legal and
// compiles; sbt rejects one BETWEEN modules. The wording has to say which it is.
func TestScalaCycleWithinOneModuleIsCoupling(t *testing.T) {
	run := func(t *testing.T, a, b, unitA, unitB string) string {
		t.Helper()
		store := facts.NewStore()
		store.Add(scalaMod(a, unitA))
		store.Add(scalaMod(b, unitB))
		store.Add(dep(a, b))
		store.Add(dep(b, a))
		ins, err := New().Explain(context.Background(), store)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range ins {
			if strings.HasPrefix(i.Title, "Cyclic dependency detected") {
				return i.Description
			}
		}
		t.Fatalf("no cycle finding; got %d insights", len(ins))
		return ""
	}

	// Two packages in ONE sbt module: legal Scala, so a coupling signal.
	within := run(t, "core/src/main/scala/a", "core/src/main/scala/b", "core", "core")
	if strings.Contains(within, "can cause initialization issues") {
		t.Errorf("same-module cycle reported as a build defect:\n%s", within)
	}
	if !strings.Contains(within, "NOT a build-order problem") {
		t.Errorf("same-module cycle not reworded as coupling:\n%s", within)
	}

	// Two different sbt modules: sbt rejects this, so it IS a build defect.
	across := run(t, "alpha/src/main/scala/a", "beta/src/main/scala/b", "alpha", "beta")
	if !strings.Contains(across, "can cause initialization issues") {
		t.Errorf("cross-module cycle lost the build-defect wording:\n%s", across)
	}

	// Unattributed (outside any source set) keeps the stricter wording, so an
	// un-modelled directory can never soften a real finding.
	unattributed := run(t, "project/a", "project/b", "", "")
	if !strings.Contains(unattributed, "can cause initialization issues") {
		t.Errorf("unattributed cycle was softened:\n%s", unattributed)
	}
}
