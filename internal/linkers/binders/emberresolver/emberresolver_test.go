package emberresolver

import (
	"context"
	"reflect"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func symbol(name, file string, props map[string]any) facts.Fact {
	if props == nil {
		props = map[string]any{}
	}
	if _, ok := props["exported"]; !ok {
		props["exported"] = true
	}
	if _, ok := props["symbol_kind"]; !ok {
		props["symbol_kind"] = facts.SymbolClass
	}
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Line: 1, Props: props}
}

func templateRef(file, owner string, invocations []string) facts.Fact {
	props := map[string]any{templateProp: true}
	if owner != "" {
		props[ownerFileProp] = owner
	}
	if len(invocations) > 0 {
		props[invocationsProp] = invocations
	}
	return facts.Fact{Kind: facts.KindFileRef, Name: file, File: file, Line: 1, Props: props}
}

func bind(t *testing.T, ff ...facts.Fact) *facts.Store {
	t.Helper()
	store := facts.NewStore()
	store.Add(ff...)
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return store
}

func factByName(t *testing.T, store *facts.Store, kind, name string) facts.Fact {
	t.Helper()
	for _, f := range store.ByKind(kind) {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("fact %s %q not in store", kind, name)
	return facts.Fact{}
}

func TestBind_TemplateInvocationToComponent(t *testing.T) {
	store := bind(t,
		symbol("app/components.StarRatings", "app/components/star-ratings.js",
			map[string]any{"web_component": "component"}),
		symbol("app/components/ui.Button", "app/components/ui/button.gts",
			map[string]any{"web_component": "component"}),
		symbol("app/helpers.FormatDate", "app/helpers/format-date.ts", nil),
		templateRef("app/components/star-ratings.hbs", "app/components/star-ratings.js",
			[]string{"Ui::Button", "format-date", "missing-thing"}),
	)

	owner := factByName(t, store, facts.KindSymbol, "app/components.StarRatings")
	if !owner.HasRelation(facts.RelCalls, "app/components/ui.Button") {
		t.Errorf("owner relations = %v, want calls edge to Ui::Button target", owner.Relations)
	}
	if !owner.HasRelation(facts.RelCalls, "app/helpers.FormatDate") {
		t.Errorf("owner relations = %v, want calls edge to the format-date helper", owner.Relations)
	}

	ref := factByName(t, store, facts.KindFileRef, "app/components/star-ratings.hbs")
	unresolved, _ := ref.Props[unresolvedProp].([]string)
	if !reflect.DeepEqual(unresolved, []string{"missing-thing"}) {
		t.Errorf("unresolved = %v, want the miss recorded, not dropped", unresolved)
	}
}

func TestBind_OwnerlessTemplateCarriesEdgesItself(t *testing.T) {
	store := bind(t,
		symbol("app/components.Badge", "app/components/badge.ts",
			map[string]any{"web_component": "component"}),
		templateRef("app/templates/index.hbs", "", []string{"Badge"}),
	)
	ref := factByName(t, store, facts.KindFileRef, "app/templates/index.hbs")
	if !ref.HasRelation(facts.RelCalls, "app/components.Badge") {
		t.Errorf("file_ref relations = %v, want the edge on the carrier when no owner exists", ref.Relations)
	}
}

func TestBind_ServiceInjection_UsesDeclaredClassName(t *testing.T) {
	store := bind(t,
		symbol("app/services.AboardApolloService", "app/services/aboard-apollo.ts", nil),
		symbol("app/components.Toolbar", "app/components/toolbar.ts",
			map[string]any{servicesProp: []string{"aboard-apollo", "unknown-service"}}),
	)
	toolbar := factByName(t, store, facts.KindSymbol, "app/components.Toolbar")
	if !toolbar.HasRelation(facts.RelInjects, "app/services.AboardApolloService") {
		t.Errorf("relations = %v, want injects edge to the DECLARED class name, not a guessed one", toolbar.Relations)
	}
	for _, r := range toolbar.Relations {
		if r.Kind == facts.RelInjects && r.Target != "app/services.AboardApolloService" {
			t.Errorf("unexpected injects edge %v for an unresolvable service", r)
		}
	}
}

func TestBind_AmbiguousFileSkipped(t *testing.T) {
	store := bind(t,
		symbol("apps/main/app/components.Badge", "apps/main/app/components/badge.ts",
			map[string]any{"web_component": "component"}),
		symbol("apps/admin/app/components.Badge", "apps/admin/app/components/badge.ts",
			map[string]any{"web_component": "component"}),
		templateRef("apps/main/app/templates/index.hbs", "", []string{"Badge"}),
	)
	ref := factByName(t, store, facts.KindFileRef, "apps/main/app/templates/index.hbs")
	if len(ref.Relations) != 0 {
		t.Errorf("relations = %v, want none — two candidate files means skip, not guess", ref.Relations)
	}
	unresolved, _ := ref.Props[unresolvedProp].([]string)
	if !reflect.DeepEqual(unresolved, []string{"Badge"}) {
		t.Errorf("unresolved = %v, want the ambiguous name recorded", unresolved)
	}
}

func TestBind_CoLocatedTemplateIsNotAmbiguous(t *testing.T) {
	store := bind(t,
		symbol("app/components/core/form.Field", "app/components/core/form/field.ts",
			map[string]any{"web_component": "component"}),
		templateRef("app/components/core/form/field.hbs", "app/components/core/form/field.ts", nil),
		templateRef("app/templates/index.hbs", "", []string{"Core::Form::Field"}),
	)
	ref := factByName(t, store, facts.KindFileRef, "app/templates/index.hbs")
	if !ref.HasRelation(facts.RelCalls, "app/components/core/form.Field") {
		t.Errorf("relations = %v, want the class file to win over its own co-located template", ref.Relations)
	}
}

func TestBind_LookalikePathOutsideAppTreeDoesNotShadow(t *testing.T) {
	store := bind(t,
		symbol("app/components.Icon", "app/components/icon.gts",
			map[string]any{"web_component": "component"}),
		symbol("app/templates/styleguide/components.Icon", "app/templates/styleguide/components/icon.hbs",
			map[string]any{"web_component": "component"}),
		templateRef("app/templates/index.hbs", "", []string{"Icon"}),
	)
	ref := factByName(t, store, facts.KindFileRef, "app/templates/index.hbs")
	if !ref.HasRelation(facts.RelCalls, "app/components.Icon") {
		t.Errorf("relations = %v, want the app-tree component, unshadowed by the style-guide template", ref.Relations)
	}
}

func TestBind_Idempotent(t *testing.T) {
	store := bind(t,
		symbol("app/components.Badge", "app/components/badge.ts",
			map[string]any{"web_component": "component"}),
		symbol("app/components.Card", "app/components/card.js",
			map[string]any{"web_component": "component"}),
		templateRef("app/components/card.hbs", "app/components/card.js", []string{"Badge"}),
	)
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatalf("second Bind: %v", err)
	}
	card := factByName(t, store, facts.KindSymbol, "app/components.Card")
	count := 0
	for _, r := range card.Relations {
		if r.Kind == facts.RelCalls && r.Target == "app/components.Badge" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("calls edge appears %d times after re-bind, want exactly 1", count)
	}
}
