package constraints

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func reachRuleIntent(id, forbidReach, to, via, because string) facts.Fact {
	props := map[string]any{"intent_kind": "rule", "rule": id, "forbid_reach": forbidReach,
		"to": to, "because": because, "source": "wiki/p.md"}
	if via != "" {
		props["via"] = via
	}
	return facts.Fact{Kind: facts.KindIntent, Name: "rule: " + id, File: "wiki/p.md", Props: props}
}

func symbolWithCall(name, file, target string) facts.Fact {
	f := facts.Fact{Kind: facts.KindSymbol, Name: name, File: file}
	if target != "" {
		f.Relations = []facts.Relation{{Kind: facts.RelCalls, Target: target}}
	}
	return f
}

func TestExplain_ForbidReach_TransitiveViolationCarriesTheWitness(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("domain", "app/domain/**"),
		componentIntent("adapters", "app/adapters/**"),
		reachRuleIntent("no-transitive-adapters", "domain", "adapters", "", "the domain must not know its delivery mechanisms, not even at a distance"),
		// Three hops, through intermediaries no component declares.
		symbolWithCall("app/domain/billing.Charge", "app/domain/billing.rb", "lib/orchestration.Run"),
		symbolWithCall("lib/orchestration.Run", "lib/orchestration.rb", "lib/transport.Send"),
		symbolWithCall("lib/transport.Send", "lib/transport.rb", "app/adapters/http.Post"),
		symbolWithCall("app/adapters/http.Post", "app/adapters/http.rb", ""),
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	got := insights[0]
	want := "Constraint no-transitive-adapters violated: app/domain/billing.Charge reaches app/adapters/http.Post"
	if got.Title != want {
		t.Errorf("title = %q, want %q", got.Title, want)
	}
	if got.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0: a declared rule's breach is decided, not estimated", got.Confidence)
	}
	witness := "app/domain/billing.Charge -> lib/orchestration.Run -> lib/transport.Send -> app/adapters/http.Post"
	if !strings.Contains(got.Description, witness) {
		t.Errorf("description must render the shortest witness path %q, got: %q", witness, got.Description)
	}
	if !strings.Contains(got.Description, "Because: the domain must not know its delivery mechanisms") {
		t.Errorf("description must surface the rule's rationale, got: %q", got.Description)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Symbol != "app/domain/billing.Charge" ||
		got.Evidence[0].Fact != "app/adapters/http.Post" || got.Evidence[0].Detail != "reachable in 3 hop(s)" {
		t.Errorf("evidence = %+v, want the pair with its hop count", got.Evidence)
	}
}

func TestExplain_ForbidReach_DirectEdgeStillFires(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("domain", "app/domain/**"),
		componentIntent("adapters", "app/adapters/**"),
		reachRuleIntent("no-transitive-adapters", "domain", "adapters", "calls", "the domain must not know its delivery mechanisms"),
		symbolWithCall("app/domain/billing.Charge", "app/domain/billing.rb", "app/adapters/http.Post"),
		symbolWithCall("app/adapters/http.Post", "app/adapters/http.rb", ""),
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	got := insights[0]
	if got.Title != "Constraint no-transitive-adapters violated: app/domain/billing.Charge reaches app/adapters/http.Post" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Evidence[0].Detail != "reachable in 1 hop(s)" {
		t.Errorf("a direct edge is a one-hop path, got %q", got.Evidence[0].Detail)
	}
}

func TestExplain_ForbidReach_DepthCapFailsClosed(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("domain", "app/domain/**"),
		componentIntent("adapters", "app/adapters/**"),
		reachRuleIntent("no-transitive-adapters", "domain", "adapters", "", "the domain must not know its delivery mechanisms"),
	)
	// A chain of reachDepthCap+1 edges: one past the cap, so no path is found.
	prev := "app/domain/billing.Charge"
	store.Add(symbolWithCall(prev, "app/domain/billing.rb", "lib/hop0.Run"))
	for i := 0; i < reachDepthCap-1; i++ {
		store.Add(symbolWithCall(fmt.Sprintf("lib/hop%d.Run", i), fmt.Sprintf("lib/hop%d.rb", i), fmt.Sprintf("lib/hop%d.Run", i+1)))
	}
	store.Add(symbolWithCall(fmt.Sprintf("lib/hop%d.Run", reachDepthCap-1), "lib/last.rb", "app/adapters/http.Post"))
	store.Add(symbolWithCall("app/adapters/http.Post", "app/adapters/http.rb", ""))
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 0 {
		t.Fatalf("insights = %+v, want none: a path past the depth cap is not searched for", insights)
	}
}

func TestExplain_ForbidReach_OversizeComponentDegradesToTheSkipAdvisory(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("domain", "app/domain/**"),
		componentIntent("adapters", "app/adapters/**"),
		reachRuleIntent("no-transitive-adapters", "domain", "adapters", "", "the domain must not know its delivery mechanisms"),
		symbolWithCall("app/adapters/http.Post", "app/adapters/http.rb", ""),
	)
	for i := 0; i <= reachComponentCap; i++ {
		store.Add(symbolWithCall(fmt.Sprintf("app/domain/s%04d.Run", i), fmt.Sprintf("app/domain/s%04d.rb", i), "app/adapters/http.Post"))
	}
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly the skip advisory: %+v", len(insights), insights)
	}
	got := insights[0]
	if got.Title != "forbid_reach rule no-transitive-adapters skipped: component too large for bounded traversal" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Confidence != reachSkipConfidence {
		t.Errorf("confidence = %v, want %v: an honest degrade is an advisory, never a breach", got.Confidence, reachSkipConfidence)
	}
	if !strings.Contains(got.Description, "No verdict was reached") {
		t.Errorf("description must say no verdict was reached, got: %q", got.Description)
	}
}

func TestExplain_ForbidReach_OutputIsDeterministic(t *testing.T) {
	// A diamond: two equal-length paths from the source to the target, so a
	// walk sensitive to neighbor order would flip its witness between runs.
	build := func() *facts.Store {
		store := facts.NewStore()
		store.Add(
			componentIntent("domain", "app/domain/**"),
			componentIntent("adapters", "app/adapters/**"),
			reachRuleIntent("no-transitive-adapters", "domain", "adapters", "", "the domain must not know its delivery mechanisms"),
			facts.Fact{Kind: facts.KindSymbol, Name: "app/domain/billing.Charge", File: "app/domain/billing.rb",
				Relations: []facts.Relation{
					{Kind: facts.RelCalls, Target: "lib/z_route.Run"},
					{Kind: facts.RelCalls, Target: "lib/a_route.Run"},
				}},
			symbolWithCall("lib/z_route.Run", "lib/z_route.rb", "app/adapters/http.Post"),
			symbolWithCall("lib/a_route.Run", "lib/a_route.rb", "app/adapters/http.Post"),
			symbolWithCall("app/adapters/http.Post", "app/adapters/http.rb", ""),
		)
		return store
	}
	first, err := New().Explain(context.Background(), build())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("insights = %d, want 1: %+v", len(first), first)
	}
	if !strings.Contains(first[0].Description, "app/domain/billing.Charge -> lib/a_route.Run -> app/adapters/http.Post") {
		t.Errorf("the witness must take the name-ordered branch of the tie, got: %q", first[0].Description)
	}
	for i := 0; i < 5; i++ {
		again, err := New().Explain(context.Background(), build())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d differs:\n%+v\nvs\n%+v", i, first, again)
		}
	}
}
