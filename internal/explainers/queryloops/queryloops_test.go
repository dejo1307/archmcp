package queryloops

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func store(fs ...facts.Fact) *facts.Store {
	s := facts.NewStore()
	for _, f := range fs {
		s.Add(f)
	}
	return s
}

func model(name string) facts.Fact {
	return facts.Fact{Kind: facts.KindStorage, Name: name, Repo: "app",
		Props: map[string]any{"storage_kind": "model", "language": "ruby"}}
}

func symbol(name string, callsInLoop []string, depth int) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, Repo: "app", File: "app/x.rb",
		Props: map[string]any{"language": "ruby", "loop_depth": depth,
			"calls_in_loop": callsInLoop}}
}

func titles(t *testing.T, s *facts.Store) string {
	got, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, i := range got {
		b.WriteString(i.Title + "\n")
		for _, e := range i.Evidence {
			b.WriteString("  " + e.Detail + "\n")
		}
	}
	return b.String()
}

func TestClassLevelQueryInALoopIsReported(t *testing.T) {
	out := titles(t, store(model("AccessLevel"),
		symbol("Helpers#create_defaults", []string{"AccessLevel.find_or_create_by"}, 1)))
	if !strings.Contains(out, "AccessLevel.find_or_create_by") {
		t.Fatalf("the query-per-iteration was not reported:\n%s", out)
	}
}

// The whole reason this shape is detectable and the association-read shape is
// not: the receiver IS the type. `channel.trigger` matched an association name
// on an unrelated model while `channel` was a PusherChannel — seven such false
// positives, and every one of the seven candidates was one.
func TestAnUnknownReceiverIsNotAModel(t *testing.T) {
	out := titles(t, store(model("AccessLevel"),
		symbol("Requisition#send_pusher_event!", []string{"channel.trigger"}, 1)))
	if out != "" {
		t.Fatalf("a non-model receiver was reported:\n%s", out)
	}
}

func TestANonQueryMethodOnAModelIsNotReported(t *testing.T) {
	out := titles(t, store(model("AccessLevel"),
		symbol("X#y", []string{"AccessLevel.human_name"}, 1)))
	if out != "" {
		t.Fatalf("a non-query class method was reported:\n%s", out)
	}
}

// Ruby's loop_depth already excludes constant-bounded iterators — `6.times`
// walks its block at the same depth — so a call recorded in calls_in_loop is
// already inside a data-sized loop. A symbol with no loop signal has none.
func TestNoLoopSignalMeansNoFinding(t *testing.T) {
	out := titles(t, store(model("AccessLevel"),
		facts.Fact{Kind: facts.KindSymbol, Name: "X#y", Repo: "app", File: "app/x.rb",
			Props: map[string]any{"language": "ruby"}}))
	if out != "" {
		t.Fatalf("a symbol with no loop reported a finding:\n%s", out)
	}
}

func TestNestedLoopsRankAbove(t *testing.T) {
	out := titles(t, store(model("User"), model("AccessLevel"),
		symbol("A#shallow", []string{"AccessLevel.find_by"}, 1),
		symbol("B#nested", []string{"User.find"}, 2)))
	if strings.Index(out, "B#nested") > strings.Index(out, "A#shallow") {
		t.Fatalf("a depth-2 query should rank above a depth-1 one:\n%s", out)
	}
}

func assoc(model, name, target string) facts.Fact {
	return facts.Fact{Kind: facts.KindAssociation, Name: model + "#" + name, Repo: "app",
		Props: map[string]any{"model": model, "association": name, "target": target}}
}

func bound(name string, bindings, calls []string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, Repo: "app", File: "app/x.rb",
		Props: map[string]any{"language": "ruby", "loop_depth": 1,
			"block_bindings": bindings, "calls_in_loop": calls}}
}

// The shape everyone means by N+1, and the one that measured to zero true
// findings before block bindings existed: an association read on the element of
// a collection. `form_questions.each { |q| q.form_answers }` is a query per
// iteration only because `q` is a FormQuestion — and the binding is what says so.
func TestAnAssociationReadOnABoundElementIsReported(t *testing.T) {
	out := titles(t, store(
		assoc("Company", "form_questions", "FormQuestion"),
		assoc("FormQuestion", "form_answers", "FormAnswer"),
		bound("Report#build", []string{"q=form_questions"}, []string{"q.form_answers"})))
	if !strings.Contains(out, "q.form_answers") {
		t.Fatalf("the typed association read was not reported:\n%s", out)
	}
}

// Without the binding the receiver is untyped, and `client.post` matching an
// association named `post` on an unrelated model is exactly how the first
// version produced seven candidates and zero true findings.
func TestAnUnboundReceiverIsNotReported(t *testing.T) {
	out := titles(t, store(
		assoc("Blog", "post", "Post"),
		bound("Poster#send", []string{"x=widgets"}, []string{"client.post"})))
	if out != "" {
		t.Fatalf("an untyped receiver was reported:\n%s", out)
	}
}

// A method that is not an association on the bound element's type is a plain
// call, not a query.
func TestANonAssociationMethodOnABoundElementIsNotReported(t *testing.T) {
	out := titles(t, store(
		assoc("Company", "users", "User"),
		bound("R#run", []string{"u=users"}, []string{"u.full_name"})))
	if out != "" {
		t.Fatalf("a non-association read was reported:\n%s", out)
	}
}
