package intentcheck

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func consumesFact(repo, target, via string) facts.Fact {
	return facts.Fact{Kind: facts.KindIntent, Repo: repo, Name: "consumes " + target + " via " + via,
		Props: map[string]any{"intent_kind": "consumes", "target": target, "via": via, "source": "enola-intent.yaml"}}
}

func edgeFact(consumer, provider string, vias ...string) facts.Fact {
	return facts.Fact{Kind: facts.KindDependency, Repo: consumer, Name: consumer + " -> " + provider,
		Props: map[string]any{"type": "cross_repo", "via": vias}}
}

func repoMarker(repo string) facts.Fact {
	return facts.Fact{Kind: facts.KindModule, Repo: repo, Name: repo + "/src"}
}

func explain(t *testing.T, ff ...facts.Fact) []facts.Insight {
	t.Helper()
	store := facts.NewStore()
	store.Add(ff...)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	return insights
}

func titles(insights []facts.Insight) string {
	var out []string
	for _, i := range insights {
		out = append(out, i.Title)
	}
	return strings.Join(out, " | ")
}

func TestIntentCheck_DeclaredAndMeasuredAgreeIsSilent(t *testing.T) {
	got := explain(t,
		repoMarker("app"), repoMarker("api"),
		consumesFact("app", "api", "http-client"),
		edgeFact("app", "api", "http-client"),
	)
	if len(got) != 0 {
		t.Fatalf("agreement produced findings: %s", titles(got))
	}
}

func TestIntentCheck_UnexpectedSeamIsProofClass(t *testing.T) {
	got := explain(t,
		repoMarker("app"), repoMarker("api"), repoMarker("other"),
		consumesFact("app", "api", "http-client"),
		edgeFact("app", "api", "http-client"),
		edgeFact("app", "other", "http-client"),
	)
	if len(got) != 1 || !strings.Contains(got[0].Title, "Unexpected seam: app -> other") {
		t.Fatalf("findings = %s", titles(got))
	}
	if got[0].Confidence != 1.0 {
		t.Fatalf("unexpected seam is set difference — confidence %v, want 1.0", got[0].Confidence)
	}
}

func TestIntentCheck_MisViaNamedSeparately(t *testing.T) {
	got := explain(t,
		repoMarker("app"), repoMarker("api"),
		consumesFact("app", "api", "graphql"),
		edgeFact("app", "api", "http-client"),
	)
	if len(got) != 1 || !strings.Contains(got[0].Title, "mis-via") || got[0].Confidence != 1.0 {
		t.Fatalf("findings = %s (conf %v)", titles(got), got[0].Confidence)
	}
}

func TestIntentCheck_MissingSeamCappedAndSkippedWithoutCounterparty(t *testing.T) {
	got := explain(t,
		repoMarker("app"), repoMarker("api"),
		consumesFact("app", "api", "kafka"),
		consumesFact("app", "ghost", "http-client"),
	)
	if len(got) != 1 || !strings.Contains(got[0].Title, "Missing intended seam: app -> api") {
		t.Fatalf("findings = %s — the ghost target is absent from the graph and must be skipped, not failed", titles(got))
	}
	if got[0].Confidence != missingSeamConfidence {
		t.Fatalf("missing seam confidence %v, want capped %v", got[0].Confidence, missingSeamConfidence)
	}
}

func TestIntentCheck_UndeclaredRepoIsUnasked(t *testing.T) {
	got := explain(t,
		repoMarker("app"), repoMarker("api"),
		edgeFact("app", "api", "http-client"),
	)
	if len(got) != 0 {
		t.Fatalf("a repo with no declarations was verdicted: %s", titles(got))
	}
}

func TestIntentCheck_OverrideNoticeSurfaced(t *testing.T) {
	f := consumesFact("app", "api", "http-client")
	f.Props["overridden"] = true
	f.Props["source"] = "cluster-config"
	got := explain(t, repoMarker("app"), repoMarker("api"), f, edgeFact("app", "api", "http-client"))
	if len(got) != 1 || !strings.Contains(got[0].Title, "Intent override") {
		t.Fatalf("findings = %s", titles(got))
	}
}

func pageFact(repo, file string) facts.Fact {
	return facts.Fact{Kind: facts.KindIntent, Repo: repo, Name: "page: " + file, File: file,
		Props: map[string]any{"intent_kind": "page", "page_type": "decision", "source": file}}
}

func relationFact(repo, from, rel, to string) facts.Fact {
	return facts.Fact{Kind: facts.KindIntent, Repo: repo, Name: from + " " + rel + " " + to, File: from,
		Props: map[string]any{"intent_kind": "relation", "rel": rel, "to": to, "source": from}}
}

// TestIntentCheck_DanglingRelationCapped pins the knowledge-graph verdict: a
// relation edge to a page absent from the compiled set surfaces at capped
// confidence (the target may be deleted or merely not opted in), and an edge
// whose target compiles is silence.
func TestIntentCheck_DanglingRelationCapped(t *testing.T) {
	insights := explain(t,
		pageFact("wiki", "wiki/a/adrs/one.md"),
		pageFact("wiki", "wiki/a/adrs/two.md"),
		relationFact("wiki", "wiki/a/adrs/one.md", "depends-on", "wiki/a/adrs/two.md"),
		relationFact("wiki", "wiki/a/adrs/two.md", "supersedes", "wiki/a/adrs/gone.md"),
	)
	if len(insights) != 1 || !strings.Contains(insights[0].Title, "Dangling knowledge relation") {
		t.Fatalf("exactly the broken edge must surface, got %s", titles(insights))
	}
	if insights[0].Confidence != danglingRelationConfidence {
		t.Fatalf("a dangling relation is an estimate, got confidence %v", insights[0].Confidence)
	}
}

// TestIntentCheck_PageOnlyGraphStillVerdicts pins the guard fix: a store
// carrying only page/relation intent (no consumes declarations) must not
// early-return before the relation verdicts run.
func TestIntentCheck_PageOnlyGraphStillVerdicts(t *testing.T) {
	insights := explain(t,
		pageFact("wiki", "wiki/a/prds/spec.md"),
		relationFact("wiki", "wiki/a/prds/spec.md", "part-of", "wiki/a/epics/missing.md"),
	)
	if len(insights) != 1 {
		t.Fatalf("page-only intent must still verdict relations, got %s", titles(insights))
	}
}

// TestIntentCheck_RelationJoinsAcrossLabelPrefix pins the union-store shape:
// a multi-repo snapshot prefixes fact files with the repo label, while a
// relation's `to` stays repo-relative — the join must trim the label or every
// edge in a cluster graph dangles.
func TestIntentCheck_RelationJoinsAcrossLabelPrefix(t *testing.T) {
	target := pageFact("wiki", "wiki/wiki/a/adrs/two.md")
	insights := explain(t,
		target,
		relationFact("wiki", "wiki/wiki/a/adrs/one.md", "depends-on", "wiki/a/adrs/two.md"),
	)
	if len(insights) != 0 {
		t.Fatalf("a label-prefixed page must satisfy its repo-relative relation, got %s", titles(insights))
	}
}
