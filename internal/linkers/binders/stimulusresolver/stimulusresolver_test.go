package stimulusresolver

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func binding(view, identifier, controllerFile, handlers string) facts.Fact {
	f := facts.Fact{
		Kind: facts.KindDependency,
		Name: "stimulus-binding: " + view + " -> " + identifier,
		File: view,
		Props: map[string]any{
			"language": "ruby", "framework": "stimulus",
			"binding": "data-action", "resolution_level": "markup-declared",
			handlersProp: handlers,
		},
	}
	if controllerFile != "" {
		f.Relations = []facts.Relation{{Kind: facts.RelDependsOn, Target: controllerFile}}
	}
	return f
}

func member(name, file, receiver string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindSymbol, Name: name, File: file,
		Props: map[string]any{"symbol_kind": facts.SymbolMethod, "language": "typescript", "receiver": receiver},
	}
}

func bind(t *testing.T, ff ...facts.Fact) []facts.Fact {
	t.Helper()
	store := facts.NewStore()
	store.Add(ff...)
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return store.ByKind(facts.KindDependency)
}

func calls(f facts.Fact) []string {
	var out []string
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls {
			out = append(out, r.Target)
		}
	}
	return out
}

// TestBindsHandlerToDeclaredMember: the method a data-action names becomes an
// edge to the member the controller file declares. Without it the 121 handlers
// one measured Rails application invokes only from markup have no inbound edge
// at all — the residual finding 0007 named.
func TestBindsHandlerToDeclaredMember(t *testing.T) {
	ff := bind(t,
		binding("app/views/x.html.erb", "dropdown", "app/javascript/controllers/dropdown_controller.js", "close toggle"),
		member("app/javascript/controllers.DropdownController", "app/javascript/controllers/dropdown_controller.js", ""),
		member("app/javascript/controllers.DropdownController.toggle", "app/javascript/controllers/dropdown_controller.js", "DropdownController"),
		member("app/javascript/controllers.DropdownController.close", "app/javascript/controllers/dropdown_controller.js", "DropdownController"),
	)
	got := calls(ff[0])
	if len(got) != 2 {
		t.Fatalf("expected both handlers bound, got %v", got)
	}
	if ff[0].Props[unresolvedProp] != nil {
		t.Errorf("nothing was missed, so nothing should be reported unresolved: %v", ff[0].Props[unresolvedProp])
	}
}

// TestMissingMethodIsReportedNotGuessed: a method the controller does not define
// is a real defect in the application. It is named as unresolved, never bound to
// a class-name-derived target — that derivation is the one findings 0009 and
// 0010 were filed against.
func TestMissingMethodIsReportedNotGuessed(t *testing.T) {
	ff := bind(t,
		binding("app/views/x.html.erb", "org-chart", "app/javascript/controllers/org_chart_controller.js", "debouncedSearch search"),
		member("app/javascript/controllers.OrgChartController.search", "app/javascript/controllers/org_chart_controller.js", "OrgChartController"),
	)
	if got := calls(ff[0]); len(got) != 1 || got[0] != "app/javascript/controllers.OrgChartController.search" {
		t.Fatalf("only the declared method may bind, got %v", got)
	}
	if ff[0].Props[unresolvedProp] != "debouncedSearch" {
		t.Errorf("the missing handler must be named, got %v", ff[0].Props[unresolvedProp])
	}
}

// TestUngroundedControllerBindsNothing: an identifier the markup pass could not
// map to a file grounds no handler. The class name is derivable from the
// identifier and deriving it is exactly what must not happen.
func TestUngroundedControllerBindsNothing(t *testing.T) {
	ff := bind(t,
		binding("app/views/x.html.erb", "dropdown", "", "toggle"),
		member("app/javascript/controllers.DropdownController.toggle", "app/javascript/controllers/dropdown_controller.js", "DropdownController"),
	)
	if got := calls(ff[0]); len(got) != 0 {
		t.Fatalf("a name-only binding must bind nothing, got %v", got)
	}
	if ff[0].Props[unresolvedProp] != "toggle" {
		t.Errorf("the handler is still declared and still unresolved, got %v", ff[0].Props[unresolvedProp])
	}
}

// TestAmbiguousMemberIsSkipped: one file declaring the same member name twice
// cannot say which one a descriptor means.
func TestAmbiguousMemberIsSkipped(t *testing.T) {
	ff := bind(t,
		binding("app/views/x.html.erb", "dropdown", "app/javascript/controllers/dropdown_controller.js", "toggle"),
		member("app/javascript/controllers.DropdownController.toggle", "app/javascript/controllers/dropdown_controller.js", "DropdownController"),
		member("app/javascript/controllers.LegacyDropdown.toggle", "app/javascript/controllers/dropdown_controller.js", "LegacyDropdown"),
	)
	if got := calls(ff[0]); len(got) != 0 {
		t.Fatalf("an ambiguous member must bind nothing, got %v", got)
	}
}

// TestRepoPrefixedFilesStillMatch: in a multi-repo snapshot a symbol's File
// carries its repo label and the markup fact's relation target does not.
func TestRepoPrefixedFilesStillMatch(t *testing.T) {
	b := binding("monolith/app/views/x.html.erb", "dropdown", "app/javascript/controllers/dropdown_controller.js", "toggle")
	b.Repo = "monolith"
	m := member("app/javascript/controllers.DropdownController.toggle", "monolith/app/javascript/controllers/dropdown_controller.js", "DropdownController")
	m.Repo = "monolith"
	ff := bind(t, b, m)
	if got := calls(ff[0]); len(got) != 1 {
		t.Fatalf("the repo label must not stop the join, got %v", got)
	}
}
