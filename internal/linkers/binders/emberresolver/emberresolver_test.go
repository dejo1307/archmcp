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

func TestBind_ModifierResolves(t *testing.T) {
	store := bind(t,
		symbol("app/modifiers.Autofocus", "app/modifiers/auto-focus.ts", nil),
		symbol("app/components.Field", "app/components/field.js",
			map[string]any{"web_component": "component"}),
		templateRef("app/components/field.hbs", "app/components/field.js", []string{"auto-focus"}),
	)
	field := factByName(t, store, facts.KindSymbol, "app/components.Field")
	if !field.HasRelation(facts.RelCalls, "app/modifiers.Autofocus") {
		t.Errorf("relations = %v, want the app-local modifier resolved", field.Relations)
	}
}

func TestBind_RouteTemplateOwnedByRouteClass(t *testing.T) {
	store := bind(t,
		symbol("app/routes.CatalogRoute", "app/routes/catalog.ts", nil),
		symbol("app/components.BookCard", "app/components/book-card.gts",
			map[string]any{"web_component": "component"}),
		templateRef("app/templates/catalog.hbs", "", []string{"BookCard"}),
	)
	route := factByName(t, store, facts.KindSymbol, "app/routes.CatalogRoute")
	if !route.HasRelation(facts.RelCalls, "app/components.BookCard") {
		t.Errorf("route relations = %v, want the route class to own its template's edges", route.Relations)
	}
	ref := factByName(t, store, facts.KindFileRef, "app/templates/catalog.hbs")
	if len(ref.Relations) != 0 {
		t.Errorf("carrier relations = %v, want none once a route owner exists", ref.Relations)
	}
}

func TestBind_RouteTemplateFallsBackToController(t *testing.T) {
	store := bind(t,
		symbol("app/controllers.SearchController", "app/controllers/search/results.ts", nil),
		symbol("app/components.Hit", "app/components/hit.gts",
			map[string]any{"web_component": "component"}),
		templateRef("app/templates/search/results.hbs", "", []string{"Hit"}),
	)
	ctrl := factByName(t, store, facts.KindSymbol, "app/controllers.SearchController")
	if !ctrl.HasRelation(facts.RelCalls, "app/components.Hit") {
		t.Errorf("controller relations = %v, want ownership when no route class exists", ctrl.Relations)
	}
}

func TestBind_ModelRelationships(t *testing.T) {
	author := facts.Fact{Kind: facts.KindStorage, Name: "app/models.Author",
		File: "app/models/author.ts", Line: 1,
		Props: map[string]any{"framework": "ember-data", "storage_kind": "model"}}
	post := facts.Fact{Kind: facts.KindStorage, Name: "app/models.Post",
		File: "app/models/post.ts", Line: 1,
		Props: map[string]any{"framework": "ember-data", "storage_kind": "model",
			relationshipsProp: []string{"belongs_to:author", "has_many:missing-model"}}}
	store := bind(t, author, post)
	got := factByName(t, store, facts.KindStorage, "app/models.Post")
	if !got.HasRelation(facts.RelDependsOn, "app/models.Author") {
		t.Errorf("relations = %v, want depends_on to the resolved Author model", got.Relations)
	}
	for _, r := range got.Relations {
		if r.Kind == facts.RelDependsOn && r.Target != "app/models.Author" {
			t.Errorf("unexpected edge %v for an unresolvable model", r)
		}
	}
}

func TestBind_DefaultExportBeatsSiblingBaseClass(t *testing.T) {
	store := bind(t,
		symbol("app/components/core.CoreDropdownMenuBase", "app/components/core/dropdown-menu.gts",
			map[string]any{"web_component": "component"}),
		symbol("app/components/core.CoreDropdownMenu", "app/components/core/dropdown-menu.gts",
			map[string]any{"web_component": "component", defaultExportProp: true}),
		templateRef("app/templates/index.hbs", "", []string{"Core::DropdownMenu"}),
	)
	ref := factByName(t, store, facts.KindFileRef, "app/templates/index.hbs")
	if !ref.HasRelation(facts.RelCalls, "app/components/core.CoreDropdownMenu") {
		t.Errorf("relations = %v, want the DEFAULT export — a sibling base class is not an ambiguity", ref.Relations)
	}
}

func route(path, name string) facts.Fact {
	return facts.Fact{Kind: facts.KindRoute, Name: path, File: "app/router.ts", Line: 1,
		Props: map[string]any{"framework": "ember", "type": "page", "method": "GET",
			routeNameProp: name}}
}

func TestBind_RouteHandledByRouteClass(t *testing.T) {
	store := bind(t,
		route("/catalog/:book_id", "catalog.book"),
		symbol("app/routes/catalog.BookRoute", "app/routes/catalog/book.ts", nil),
	)
	r := factByName(t, store, facts.KindRoute, "/catalog/:book_id")
	if !r.HasRelation(facts.RelHandledBy, "app/routes/catalog.BookRoute") {
		t.Errorf("route relations = %v, want handled_by the nested route class", r.Relations)
	}
}

func TestBind_RouteLinksBecomeNavigationEdges(t *testing.T) {
	store := bind(t,
		route("/catalog", "catalog"),
		symbol("app/components.NavBar", "app/components/nav-bar.gts",
			map[string]any{"web_component": "component",
				routeLinksProp: []string{"catalog", "unknown.route"}}),
		symbol("app/components.Footer", "app/components/footer.js",
			map[string]any{"web_component": "component"}),
		templateRef("app/components/footer.hbs", "app/components/footer.js", nil),
	)
	nav := factByName(t, store, facts.KindSymbol, "app/components.NavBar")
	if !nav.HasRelation(facts.RelCalls, "/catalog") {
		t.Errorf("relations = %v, want a navigation edge to the /catalog route fact", nav.Relations)
	}
	for _, r := range nav.Relations {
		if r.Kind == facts.RelCalls && r.Target != "/catalog" {
			t.Errorf("unexpected edge %v for an unknown route name", r)
		}
	}
}

func TestBind_DataRoleLinksToItsModel(t *testing.T) {
	book := facts.Fact{Kind: facts.KindStorage, Name: "app/models.Book",
		File: "app/models/book.ts", Line: 1,
		Props: map[string]any{"framework": "ember-data", "storage_kind": "model"}}
	store := bind(t, book,
		symbol("app/serializers.BookSerializer", "app/serializers/book.ts",
			map[string]any{dataRoleProp: "serializer"}),
		symbol("app/adapters.ApplicationAdapter", "app/adapters/application.ts",
			map[string]any{dataRoleProp: "adapter"}),
	)
	ser := factByName(t, store, facts.KindSymbol, "app/serializers.BookSerializer")
	if !ser.HasRelation(facts.RelDependsOn, "app/models.Book") {
		t.Errorf("relations = %v, want the serializer bound to its model", ser.Relations)
	}
	ad := factByName(t, store, facts.KindSymbol, "app/adapters.ApplicationAdapter")
	for _, r := range ad.Relations {
		if r.Kind == facts.RelDependsOn {
			t.Errorf("application adapter gained %v — the app-wide fallback names no model", r)
		}
	}
}

func TestBind_TemplateTagRouteTemplateOwnedByRouteClass(t *testing.T) {
	store := bind(t,
		symbol("app/routes.AccountRoute", "app/routes/account.ts", nil),
		symbol("app/templates.Account", "app/templates/account.gjs",
			map[string]any{"web_component": "component", "framework": "ember",
				"symbol_kind": facts.SymbolFunc}),
	)
	route := factByName(t, store, facts.KindSymbol, "app/routes.AccountRoute")
	if !route.HasRelation(facts.RelCalls, "app/templates.Account") {
		t.Errorf("relations = %v, want the route class to own its .gjs template component", route.Relations)
	}
}

func reexportStub(stub, target string) facts.Fact {
	return facts.Fact{Kind: facts.KindDependency, Name: "x -> " + target, File: stub, Line: 1,
		Props:     map[string]any{"reexport": true, "source": "internal"},
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: target}}}
}

func TestBind_ReexportChaseResolvesAddonPublish(t *testing.T) {
	store := bind(t,
		symbol("lib/badge-kit/addon/components.KitBadge", "lib/badge-kit/addon/components/kit-badge.gjs",
			map[string]any{"web_component": "component", defaultExportProp: true}),
		reexportStub("lib/badge-kit/app/components/kit-badge.js", "lib/badge-kit/addon/components/kit-badge"),
		symbol("app/components.Host", "app/components/host.js",
			map[string]any{"web_component": "component"}),
		templateRef("app/components/host.hbs", "app/components/host.js", []string{"KitBadge"}),
	)
	host := factByName(t, store, facts.KindSymbol, "app/components.Host")
	if !host.HasRelation(facts.RelCalls, "lib/badge-kit/addon/components.KitBadge") {
		t.Errorf("relations = %v, want the addon component through its app-tree re-export stub", host.Relations)
	}
}

func TestBind_ContextualComponentJoins(t *testing.T) {
	store := bind(t,
		symbol("app/components.Card", "app/components/card.gjs",
			map[string]any{"web_component": "component", defaultExportProp: true,
				yieldHashProp: []string{"Item=card-item"}}),
		symbol("app/components.CardItem", "app/components/card-item.ts",
			map[string]any{"web_component": "component", defaultExportProp: true}),
		symbol("app/components.List", "app/components/list.js",
			map[string]any{"web_component": "component"}),
		templateRef("app/components/list.hbs", "app/components/list.js", nil),
	)
	store.UpdateWhere(func(f *facts.Fact) {
		if f.Kind == facts.KindFileRef && f.Name == "app/components/list.hbs" {
			f.Props[contextualProp] = []string{"Card#Item", "Card#Missing"}
		}
	})
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	list := factByName(t, store, facts.KindSymbol, "app/components.List")
	if !list.HasRelation(facts.RelCalls, "app/components.CardItem") {
		t.Errorf("relations = %v, want Card#Item joined through Card's yield hash", list.Relations)
	}
	ref := factByName(t, store, facts.KindFileRef, "app/components/list.hbs")
	unresolved, _ := ref.Props[unresolvedProp].([]string)
	found := false
	for _, u := range unresolved {
		if u == "Card#Missing" {
			found = true
		}
	}
	if !found {
		t.Errorf("unresolved = %v, want the unknown key recorded", unresolved)
	}
}

func TestBind_AttrTransformBindsToAppTransform(t *testing.T) {
	book := facts.Fact{Kind: facts.KindStorage, Name: "app/models.Book",
		File: "app/models/book.ts", Line: 1,
		Props: map[string]any{"framework": "ember-data", "storage_kind": "model",
			attrTransformsProp: []string{"duration", "date"}}}
	store := bind(t, book,
		symbol("app/transforms.Duration", "app/transforms/duration.ts", nil),
	)
	got := factByName(t, store, facts.KindStorage, "app/models.Book")
	if !got.HasRelation(facts.RelDependsOn, "app/transforms.Duration") {
		t.Errorf("relations = %v, want the app-defined transform bound", got.Relations)
	}
	for _, r := range got.Relations {
		if r.Kind == facts.RelDependsOn && r.Target != "app/transforms.Duration" {
			t.Errorf("built-in 'date' has no app file and must draw nothing; got %v", r)
		}
	}
}

func TestBind_PodsComponentResolves(t *testing.T) {
	store := bind(t,
		symbol("app/pods/user-card.UserCard", "app/pods/user-card/component.js",
			map[string]any{"web_component": "component", defaultExportProp: true}),
		templateRef("app/templates/index.hbs", "", []string{"UserCard"}),
	)
	ref := factByName(t, store, facts.KindFileRef, "app/templates/index.hbs")
	if !ref.HasRelation(facts.RelCalls, "app/pods/user-card.UserCard") {
		t.Errorf("relations = %v, want the pods component resolved", ref.Relations)
	}
}

func TestBind_SubstateTemplateOwnedByParentRoute(t *testing.T) {
	store := bind(t,
		symbol("app/routes.CatalogRoute", "app/routes/catalog.ts", nil),
		symbol("app/components.Spinner", "app/components/spinner.hbs",
			map[string]any{"web_component": "component"}),
		templateRef("app/templates/catalog-loading.hbs", "", []string{"Spinner"}),
	)
	route := factByName(t, store, facts.KindSymbol, "app/routes.CatalogRoute")
	if !route.HasRelation(facts.RelCalls, "app/components.Spinner") {
		t.Errorf("relations = %v, want the loading substate owned by catalog's route", route.Relations)
	}
}

func TestBind_EngineTemplatesResolveInOwnTreeOnly(t *testing.T) {
	store := bind(t,
		symbol("lib/shop/addon/components.Price", "lib/shop/addon/components/price.js",
			map[string]any{"web_component": "component", defaultExportProp: true}),
		symbol("app/components.Price", "app/components/price.js",
			map[string]any{"web_component": "component", defaultExportProp: true}),
		symbol("lib/shop/addon/components.Cart", "lib/shop/addon/components/cart.js",
			map[string]any{"web_component": "component"}),
		templateRef("lib/shop/addon/components/cart.hbs", "lib/shop/addon/components/cart.js",
			[]string{"Price"}),
	)
	cart := factByName(t, store, facts.KindSymbol, "lib/shop/addon/components.Cart")
	if !cart.HasRelation(facts.RelCalls, "lib/shop/addon/components.Price") {
		t.Errorf("relations = %v, want the ENGINE's Price, isolated from the host's", cart.Relations)
	}
	if cart.HasRelation(facts.RelCalls, "app/components.Price") {
		t.Error("engine template resolved into the host tree — isolation broken")
	}
}

func TestBind_CarrierYieldHashReachesConsumers(t *testing.T) {
	store := bind(t,
		symbol("app/components/core/form.Field", "app/components/core/form/field.ts",
			map[string]any{"web_component": "component", defaultExportProp: true}),
		func() facts.Fact {
			f := templateRef("app/components/core/form/field.hbs", "app/components/core/form/field.ts", nil)
			f.Props[yieldHashProp] = []string{"Input=core/input"}
			return f
		}(),
		symbol("app/components/core.Input", "app/components/core/input.ts",
			map[string]any{"web_component": "component", defaultExportProp: true}),
		symbol("app/components.Form", "app/components/form.js",
			map[string]any{"web_component": "component"}),
		func() facts.Fact {
			f := templateRef("app/components/form.hbs", "app/components/form.js", nil)
			f.Props[contextualProp] = []string{"Core::Form::Field#Input"}
			return f
		}(),
	)
	form := factByName(t, store, facts.KindSymbol, "app/components.Form")
	if !form.HasRelation(facts.RelCalls, "app/components/core.Input") {
		t.Errorf("relations = %v, want the .hbs-declared yield hash visible across carriers", form.Relations)
	}
}

func TestBind_DefaultFunctionBeatsSiblingTypeExports(t *testing.T) {
	store := bind(t,
		symbol("app/helpers.translateResources", "app/helpers/translate-resources.ts",
			map[string]any{"symbol_kind": "function", defaultExportProp: true}),
		symbol("app/helpers.GroupedResource", "app/helpers/translate-resources.ts",
			map[string]any{"symbol_kind": "interface"}),
		symbol("app/components.Picker", "app/components/picker.js",
			map[string]any{"web_component": "component"}),
		templateRef("app/components/picker.hbs", "app/components/picker.js",
			[]string{"translate-resources"}),
	)
	picker := factByName(t, store, facts.KindSymbol, "app/components.Picker")
	if !picker.HasRelation(facts.RelCalls, "app/helpers.translateResources") {
		t.Errorf("relations = %v, want the default-exported helper, unambiguous beside its type exports", picker.Relations)
	}
}
