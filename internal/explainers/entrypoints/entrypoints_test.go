package entrypoints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/linkers/binders/frameworkroots"
)

func TestARoutedActionIsAnEntryPoint(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: "Admin::AccountsController#index", Repo: "app",
		Props: map[string]any{"language": "ruby", "symbol_kind": "method", frameworkroots.RootProp: frameworkroots.MechanismRoute}})
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: "Admin::AccountsController#load_account", Repo: "app",
		Props: map[string]any{"language": "ruby", "symbol_kind": "method", frameworkroots.ReachedFromProp: "route"}})
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: "Admin::AccountsController#orphan", Repo: "app",
		Props: map[string]any{"language": "ruby", "symbol_kind": "method"}})
	s.Add(facts.Fact{Kind: facts.KindRoute, Name: "/admin/accounts", Repo: "app",
		Props: map[string]any{"handler": "admin/accounts#index", frameworkroots.RootProp: frameworkroots.MechanismRoute}})
	got, err := New().Explain(context.Background(), s)
	if err != nil || len(got) != 1 {
		t.Fatalf("want one insight, got %v (%v)", got, err)
	}
	if !strings.Contains(got[0].Title, "1 routed") {
		t.Fatalf("routed action not counted: %s", got[0].Title)
	}
	if len(got[0].Evidence) != 2 || !strings.Contains(got[0].Evidence[1].Detail, "1 of 2 non-root methods (50.0%)") {
		t.Fatalf("reached share not carried as the ceiling: %+v", got[0].Evidence)
	}
}

// admin/accounts is Admin::AccountsController, not AdminAccountsController.
// Getting this wrong resolved 63 of 2,528 handlers instead of 1,538, and the
// reachability number built on it read 91% dead.
func TestNamespacedControllersResolve(t *testing.T) {
	if got := controllerClass("admin/accounts"); got != "Admin::AccountsController" {
		t.Fatalf("controllerClass = %q", got)
	}
	if got := controllerClass("app/api/candidate_notes"); got != "App::Api::CandidateNotesController" {
		t.Fatalf("controllerClass = %q", got)
	}
}

// A handler naming a controller the graph never extracted is an extraction gap,
// not an entry point. Counting it as one silently shrinks the root set.
func TestAnUnresolvedHandlerIsReportedSeparately(t *testing.T) {
	s := facts.NewStore()
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: "Thing#perform", Repo: "app",
		Props: map[string]any{"language": "ruby", "symbol_kind": "method", frameworkroots.RootProp: frameworkroots.MechanismJob}})
	s.Add(facts.Fact{Kind: facts.KindRoute, Name: "/x", Repo: "app",
		Props: map[string]any{"handler": "ghost/missing#index"}})
	got, _ := New().Explain(context.Background(), s)
	if len(got) != 1 || !strings.Contains(got[0].Evidence[0].Detail, "1 route handlers") {
		t.Fatalf("unresolved handler not surfaced: %+v", got)
	}
}
