package constraints

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func rubyMethod(name, file string, calls ...string) facts.Fact {
	rels := make([]facts.Relation, 0, len(calls))
	for _, c := range calls {
		rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: c})
	}
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Repo: "app", Line: 3,
		Props: map[string]any{"symbol_kind": "method", "language": "ruby", "exported": true}, Relations: rels}
}

func requireEdgeIntent(id, from, to string) facts.Fact {
	return facts.Fact{Kind: facts.KindIntent, Name: "rule: " + id, File: "wiki/p.md",
		Props: map[string]any{"intent_kind": "rule", "rule": id, "require_edge": from, "to": to, "direction": "outbound",
			"via": "calls", "because": "every " + from + " member reaches " + to, "source": "wiki/p.md"}}
}

func radiusStore() *facts.Store {
	store := facts.NewStore()
	store.Add(
		componentIntent("models", "app/models/**"),
		componentIntent("controllers", "app/controllers/**"),
		componentIntent("jobs", "app/jobs/**"),
		ruleIntent("models-stay-off-controllers", "models", "controllers", "calls", "a model that knows the request cannot run off it"),
		requireEdgeIntent("jobs-touch-models", "jobs", "models"),
		rubyClass("Order", "app/models/order.rb", ""),
		rubyMethod("Order#total", "app/models/order.rb", "OrdersController#render_total"),
		rubyClass("OrdersController", "app/controllers/orders_controller.rb", ""),
		rubyMethod("OrdersController#render_total", "app/controllers/orders_controller.rb", "Order#total"),
		rubyClass("SyncJob", "app/jobs/sync_job.rb", ""),
		rubyMethod("SyncJob#perform", "app/jobs/sync_job.rb", "Order#total"),
	)
	return store
}

// A breach whose witness lives in the file vanishes once the file leaves its
// part; a rule that demanded an edge onto the part starts failing.
func TestBlastRadius_NamesVanishingAndAppearingVerdicts(t *testing.T) {
	store := radiusStore()
	radius := BlastRadiusFor(store, []string{"app/models/order.rb"})
	if radius.RulesRun != 2 || len(radius.NotComputed) != 0 {
		t.Fatalf("rules_run = %d, not computed = %v", radius.RulesRun, radius.NotComputed)
	}
	if len(radius.Vanish) != 1 || radius.Vanish[0].Rule != "models-stay-off-controllers" {
		t.Fatalf("vanish = %+v", radius.Vanish)
	}
	if radius.Vanish[0].Because == "" || !strings.Contains(radius.Vanish[0].Title, "violated") {
		t.Fatalf("a vanishing verdict carries the rule's reason and its violation title: %+v", radius.Vanish[0])
	}
	if len(radius.Appear) != 1 || radius.Appear[0].Rule != "jobs-touch-models" {
		t.Fatalf("appear = %+v", radius.Appear)
	}
}

// A file that belongs to no part changes nothing.
func TestBlastRadius_FileOutsideEveryPartChangesNothing(t *testing.T) {
	store := radiusStore()
	store.Add(rubyMethod("Helper#fmt", "lib/helper.rb"))
	radius := BlastRadiusFor(store, []string{"lib/helper.rb"})
	if len(radius.Appear) != 0 || len(radius.Vanish) != 0 {
		t.Fatalf("appear = %+v, vanish = %+v", radius.Appear, radius.Vanish)
	}
}

// A rule that cannot be computed is named, never folded into unchanged.
func TestBlastRadius_ListsARuleThatFailsAsNotComputed(t *testing.T) {
	verdictSeam = func(r rule) {
		if r.id == "jobs-touch-models" {
			panic("the census is unavailable")
		}
	}
	defer func() { verdictSeam = nil }()
	radius := BlastRadiusFor(radiusStore(), []string{"app/models/order.rb"})
	if len(radius.NotComputed) != 1 || radius.NotComputed[0].Rule != "jobs-touch-models" || !strings.Contains(radius.NotComputed[0].Cause, "census") {
		t.Fatalf("not computed = %+v", radius.NotComputed)
	}
	if len(radius.Appear) != 0 || len(radius.Vanish) != 1 {
		t.Fatalf("the failing rule is excluded from the comparison: appear = %+v, vanish = %+v", radius.Appear, radius.Vanish)
	}
}

// The verdict pass is unchanged: a panic still propagates without the guard.
func TestExplain_PanicsWithoutTheGuard(t *testing.T) {
	verdictSeam = func(r rule) { panic("boom") }
	defer func() { verdictSeam = nil }()
	defer func() {
		if recover() == nil {
			t.Fatal("the verdict pass must not swallow a rule's panic")
		}
	}()
	_, _ = New().Explain(t.Context(), radiusStore())
}

// Incoming edges are grouped by kind and by the part the source belongs to.
func TestExplainFile_GroupsIncomingEdgesByKindAndPart(t *testing.T) {
	store := radiusStore()
	store.Add(rubyMethod("Script#run", "bin/script.rb", "Order#total"))
	e := ExplainFile(store, "app/models/order.rb")
	if len(e.Incoming) != 3 {
		t.Fatalf("incoming = %+v", e.Incoming)
	}
	byPart := map[string]IncomingGroup{}
	for _, g := range e.Incoming {
		if g.Kind != facts.RelCalls {
			t.Fatalf("kind = %s", g.Kind)
		}
		byPart[g.Component] = g
	}
	if byPart["controllers"].Count != 1 || byPart["jobs"].Count != 1 || byPart[NoPart].Count != 1 {
		t.Fatalf("counts = %+v", byPart)
	}
	if byPart["jobs"].Sources[0] != "SyncJob#perform" {
		t.Fatalf("sources = %+v", byPart["jobs"].Sources)
	}
}

// A part explanation names members by file, fan-in and fan-out by part, and
// the laws naming the part.
func TestExplainPart_MembersEdgesAndLaws(t *testing.T) {
	store := radiusStore()
	p, ok := ExplainPart(store, "models")
	if !ok {
		t.Fatal("models is declared")
	}
	if len(p.Files) != 1 || p.Files[0].File != "app/models/order.rb" || len(p.Files[0].Members) != 2 {
		t.Fatalf("files = %+v", p.Files)
	}
	if len(p.FanIn) != 2 || p.FanIn[0].Count != 1 || len(p.FanOut) != 1 || p.FanOut[0].Component != "controllers" {
		t.Fatalf("fan-in = %+v, fan-out = %+v", p.FanIn, p.FanOut)
	}
	if len(p.Laws) != 2 || p.Laws[0].Rule != "jobs-touch-models" || p.Laws[1].Rule != "models-stay-off-controllers" || p.Laws[0].Mode != "ratchet" {
		t.Fatalf("laws = %+v", p.Laws)
	}
	if _, ok := ExplainPart(store, "nothing"); ok {
		t.Fatal("an undeclared part is not explained")
	}
}

// The snapshot's own constraint verdicts stand in for the before pass; the
// comparison reads the same as two passes.
func TestBlastRadiusAgainst_ReadsTheSnapshotAsBefore(t *testing.T) {
	store := radiusStore()
	snapshot, err := New().Explain(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	for i := range snapshot {
		snapshot[i].Source = New().Name()
	}
	radius := BlastRadiusAgainst(store, []string{"app/models/order.rb"}, snapshot)
	if len(radius.Vanish) != 1 || radius.Vanish[0].Rule != "models-stay-off-controllers" || len(radius.Appear) != 1 {
		t.Fatalf("vanish = %+v, appear = %+v", radius.Vanish, radius.Appear)
	}
}
