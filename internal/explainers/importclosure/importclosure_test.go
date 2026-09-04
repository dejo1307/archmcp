package importclosure

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func extDep(file, target string) facts.Fact {
	f := dep(file, target, false)
	f.Props[facts.PropSource] = facts.DepSourceExternal
	return f
}

func explain(t *testing.T, ff ...facts.Fact) []facts.Insight {
	t.Helper()
	s := facts.NewStore()
	s.Add(ff...)
	got, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func find(ins []facts.Insight, substr string) *facts.Insight {
	for i := range ins {
		if strings.Contains(ins[i].Title, substr) {
			return &ins[i]
		}
	}
	return nil
}

// TestExplain_SummaryIsInformational — the summary describes the graph rather than
// complaining about it. A package that legitimately loads all of itself must not be
// able to fail a gate for doing so.
func TestExplain_SummaryIsInformational(t *testing.T) {
	ins := explain(t,
		sym("pkg/__init__.py"), sym("pkg/a.py"), sym("pkg/b.py"),
		dep("pkg/__init__.py", "pkg/a", false),
		dep("pkg/a.py", "pkg/b", false),
	)
	s := find(ins, "Importing pkg loads")
	if s == nil {
		t.Fatalf("no summary insight in %v", ins)
	}
	if !s.Informational {
		t.Error("the summary must be informational — it states a fact, it does not complain")
	}
	if s.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0 (it is measured, not estimated)", s.Confidence)
	}
	if got := s.MetricInt("modules_loaded"); got != 3 {
		t.Errorf("modules_loaded = %d, want 3", got)
	}
}

// TestExplain_ReportsDominatingBarrel is the finding the explainer exists for: a
// package __init__.py that alone accounts for a large part of what an entry point
// loads, together with the third-party packages that come with it.
func TestExplain_ReportsDominatingBarrel(t *testing.T) {
	ff := []facts.Fact{
		sym("pkg/__init__.py"), sym("pkg/leaf.py"),
		sym("pkg/heavy/__init__.py"),
		dep("pkg/__init__.py", "pkg/leaf", false),
		dep("pkg/leaf.py", "pkg/heavy/thing", false), // pulls the barrel in implicitly
		extDep("pkg/heavy/__init__.py", "scrapylib"),
	}
	// A subtree only the barrel reaches.
	for _, n := range []string{"thing", "m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9", "m10"} {
		ff = append(ff, sym("pkg/heavy/"+n+".py"), dep("pkg/heavy/__init__.py", "pkg/heavy/"+n, false))
	}
	ins := explain(t, ff...)
	f := find(ins, "only via pkg/heavy/__init__.py")
	if f == nil {
		t.Fatalf("dominating barrel not reported; got %d insight(s)", len(ins))
	}
	if f.Informational {
		t.Error("a dominating barrel is a finding, not a description")
	}
	if n := f.MetricInt("modules_dominated"); n < minDominated {
		t.Errorf("modules_dominated = %d, want >= %d", n, minDominated)
	}
	if !strings.Contains(f.Description, "scrapylib") {
		t.Errorf("the third-party package it brings is not named: %s", f.Description)
	}
}

// TestExplain_SmallPackageIsNotAFinding — a barrel below the threshold is ordinary
// layering, and reporting it would bury the ones worth splitting.
func TestExplain_SmallPackageIsNotAFinding(t *testing.T) {
	ins := explain(t,
		sym("pkg/__init__.py"), sym("pkg/sub/__init__.py"), sym("pkg/sub/one.py"),
		dep("pkg/__init__.py", "pkg/sub/one", false),
		dep("pkg/sub/__init__.py", "pkg/sub/one", false),
	)
	for _, i := range ins {
		if !i.Informational {
			t.Errorf("small package produced a finding: %s", i.Title)
		}
	}
}

// TestExplain_DeferredImportsAreNotCharged — a lazy import is not something the entry
// point pays for, so neither it nor its third-party packages may appear.
func TestExplain_DeferredImportsAreNotCharged(t *testing.T) {
	ins := explain(t,
		sym("pkg/__init__.py"), sym("pkg/a.py"), sym("pkg/heavy.py"),
		dep("pkg/__init__.py", "pkg/a", false),
		dep("pkg/a.py", "pkg/heavy", true),
		extDep("pkg/heavy.py", "torch"),
	)
	s := find(ins, "Importing pkg loads")
	if s == nil {
		t.Fatal("no summary")
	}
	if got := s.MetricInt("third_party_count"); got != 0 {
		t.Errorf("third_party_count = %d, want 0 — torch is behind a deferred import", got)
	}
}

// TestExplain_NoPythonNoInsights — the explainer must be silent on repositories it has
// nothing to say about.
func TestExplain_NoPythonNoInsights(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: "main.Run", File: "main.go", Props: map[string]any{"language": "go"}})
	got, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d insight(s) on a Go-only repository, want 0", len(got))
	}
}
