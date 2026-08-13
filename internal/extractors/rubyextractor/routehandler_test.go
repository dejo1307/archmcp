package rubyextractor

import (
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// routeIndex parses a route file and returns "<METHOD> <path>" -> fact.
func routeIndex(t *testing.T, src string) map[string]facts.Fact {
	t.Helper()
	out := map[string]facts.Fact{}
	for _, f := range parseRouteFileAST([]byte(src), "config/routes.rb") {
		m, _ := f.Props["method"].(string)
		out[m+" "+f.Name] = f
	}
	return out
}

func routeKeys(m map[string]facts.Fact) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, "\n  ")
}

// handledBy returns the target of a fact's handled_by relation, or "".
func handledBy(f facts.Fact) string {
	for _, r := range f.Relations {
		if r.Kind == facts.RelHandledBy {
			return r.Target
		}
	}
	return ""
}

// TestRouteHandler_ResourcesDeriveController is the core of the change: before it, a
// `resources` declaration produced routes with no handler at all, so a Rails route was
// an isolated graph node — impact analysis from a controller could not reach the
// endpoints it serves, and a controller reached only through the route table read as
// dead code.
func TestRouteHandler_ResourcesDeriveController(t *testing.T) {
	idx := routeIndex(t, `
Rails.application.routes.draw do
  namespace :api do
    namespace :v2 do
      resources :posts, only: [:index, :show]
    end
  end
end
`)
	for _, tc := range []struct{ key, handler, symbol string }{
		{"GET /api/v2/posts", "api/v2/posts#index", "Api::V2::PostsController#index"},
		{"GET /api/v2/posts/:id", "api/v2/posts#show", "Api::V2::PostsController#show"},
	} {
		f, ok := idx[tc.key]
		if !ok {
			t.Fatalf("missing %q; have:\n  %s", tc.key, routeKeys(idx))
		}
		if got := f.Props["handler"]; got != tc.handler {
			t.Errorf("%s handler = %v, want %q", tc.key, got, tc.handler)
		}
		if got := handledBy(f); got != tc.symbol {
			t.Errorf("%s handled_by = %q, want %q", tc.key, got, tc.symbol)
		}
	}
}

// A `scope module:` contributes a controller namespace without changing the URL; a bare
// `scope '/path'` does the opposite. Conflating the two mis-resolves every handler
// underneath.
func TestRouteHandler_ScopeModuleVsPath(t *testing.T) {
	idx := routeIndex(t, `
Rails.application.routes.draw do
  scope '/internal' do
    resources :jobs, only: [:index]
  end
  scope module: :admin do
    resources :users, only: [:index]
  end
end
`)
	if f := idx["GET /internal/jobs"]; f.Props["handler"] != "jobs#index" {
		t.Errorf("path-only scope leaked into the controller: %v", f.Props["handler"])
	}
	if f := idx["GET /users"]; f.Props["handler"] != "admin/users#index" {
		t.Errorf("module scope not applied: %v", f.Props["handler"])
	}
	if f := idx["GET /users"]; handledBy(f) != "Admin::UsersController#index" {
		t.Errorf("handled_by = %q", handledBy(idx["GET /users"]))
	}
}

// A bare verb inside a resources block names an action on that resource's controller.
// This is how most non-REST Rails endpoints are written.
func TestRouteHandler_MemberVerbUsesEnclosingController(t *testing.T) {
	idx := routeIndex(t, `
Rails.application.routes.draw do
  resources :posts do
    member do
      post :publish
    end
    collection do
      get :drafts
    end
  end
end
`)
	if f, ok := idx["POST /posts/:id/publish"]; !ok {
		t.Fatalf("missing member route; have:\n  %s", routeKeys(idx))
	} else if f.Props["handler"] != "posts#publish" {
		t.Errorf("member handler = %v, want posts#publish", f.Props["handler"])
	}
	if f, ok := idx["GET /posts/drafts"]; !ok {
		t.Fatalf("missing collection route; have:\n  %s", routeKeys(idx))
	} else if f.Props["handler"] != "posts#drafts" {
		t.Errorf("collection handler = %v, want posts#drafts", f.Props["handler"])
	}
}

// An explicit `controller:` option and the `controller do` block form both override the
// name derived from the resource.
func TestRouteHandler_ExplicitControllerOverride(t *testing.T) {
	idx := routeIndex(t, `
Rails.application.routes.draw do
  resources :posts, controller: 'articles', only: [:index]
  controller :photos do
    get 'search'
  end
end
`)
	if f := idx["GET /posts"]; f.Props["handler"] != "articles#index" {
		t.Errorf("controller: option ignored: %v", f.Props["handler"])
	}
	if f, ok := idx["GET /search"]; !ok {
		t.Fatalf("missing controller-block route; have:\n  %s", routeKeys(idx))
	} else if f.Props["handler"] != "photos#search" {
		t.Errorf("controller block handler = %v, want photos#search", f.Props["handler"])
	}
}

// A singular `resource` is served by a PLURAL controller in Rails.
func TestRouteHandler_SingularResourceUsesPluralController(t *testing.T) {
	idx := routeIndex(t, `
Rails.application.routes.draw do
  resource :profile, only: [:show]
end
`)
	if f := idx["GET /profile"]; f.Props["handler"] != "profiles#show" {
		t.Errorf("handler = %v, want profiles#show", f.Props["handler"])
	}
}

// TestRouteConcerns_ReplayedPerReference: a `concern` block serves nothing where it is
// defined and everything where it is referenced. Emitting its routes at the definition
// site (which the previous default-case descent did) puts them at the wrong path and
// misses every real one.
func TestRouteConcerns_ReplayedPerReference(t *testing.T) {
	idx := routeIndex(t, `
Rails.application.routes.draw do
  concern :commentable do
    resources :comments, only: [:index]
  end

  resources :posts, only: [:index], concerns: :commentable
  resources :photos, only: [:index] do
    concerns :commentable
  end
end
`)
	for _, want := range []string{
		"GET /posts/:post_id/comments",
		"GET /photos/:photo_id/comments",
	} {
		if _, ok := idx[want]; !ok {
			t.Errorf("missing concern route %q; have:\n  %s", want, routeKeys(idx))
		}
	}
	// Nothing at the definition site.
	if _, ok := idx["GET /comments"]; ok {
		t.Errorf("concern emitted routes at its definition site; have:\n  %s", routeKeys(idx))
	}
}

func TestControllerSymbol(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"posts#index", "PostsController#index"},
		{"api/v2/posts#show", "Api::V2::PostsController#show"},
		{"admin/user_sessions#create", "Admin::UserSessionsController#create"},
		// Already a class path.
		{"Api::V2::PostsController#index", "Api::V2::PostsController#index"},
		// Not a controller action: no action, a redirect, a Rack app.
		{"posts", ""},
		{"", ""},
		{"redirect('/x')#call", ""},
		{"Sidekiq::Web#call", ""},
	} {
		if got := controllerSymbol(tc.in); got != tc.want {
			t.Errorf("controllerSymbol(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseMountShapes(t *testing.T) {
	for _, tc := range []struct{ name, src, wantConst, wantAt string }{
		{"at keyword", "mount Spree::Core::Engine, at: '/shop'", "Spree::Core::Engine", "/shop"},
		{"hash rocket", "mount Sidekiq::Web => '/sidekiq'", "Sidekiq::Web", "/sidekiq"},
		{"no path defaults to root", "mount API::API", "API::API", "/"},
		{"rack app built inline", "mount Coverband::Reporters::Web.new, at: '/coverage'", "Coverband::Reporters::Web", "/coverage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "Rails.application.routes.draw do\n  " + tc.src + "\nend\n"
			ff := parseRouteFileAST([]byte(src), "config/routes.rb")
			if len(ff) != 1 {
				t.Fatalf("want 1 mount fact, got %d: %+v", len(ff), ff)
			}
			if got := ff[0].Props["mounts"]; got != tc.wantConst {
				t.Errorf("constant = %v, want %q", got, tc.wantConst)
			}
			wantName := strings.TrimSuffix(tc.wantAt, "/") + "/"
			if ff[0].Name != wantName {
				t.Errorf("name = %q, want %q", ff[0].Name, wantName)
			}
			if ff[0].Props["method"] != "MOUNT" {
				t.Errorf("method = %v, want MOUNT", ff[0].Props["method"])
			}
		})
	}
}

// A mount inside a scope is served below that scope's prefix.
func TestMountInsideScopeComposesPrefix(t *testing.T) {
	ff := parseRouteFileAST([]byte(`
Rails.application.routes.draw do
  namespace :admin do
    mount Sidekiq::Web, at: '/sidekiq'
  end
end
`), "config/routes.rb")
	if len(ff) != 1 {
		t.Fatalf("want 1 fact, got %d", len(ff))
	}
	if ff[0].Name != "/admin/sidekiq/" {
		t.Errorf("name = %q, want /admin/sidekiq/", ff[0].Name)
	}
}

// TestRoutesInsideControlFlow covers the shape that silently emptied five route files
// across the corpus: a route file is Ruby, and real ones guard their contents with
// conditionals. Walking only direct `call` children skipped every branch body, and a
// file that parses cleanly and yields nothing is indistinguishable from a file with no
// routes.
func TestRoutesInsideControlFlow(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      int
	}{
		{
			// GitLab guards whole route files this way.
			name: "unless at file scope",
			src:  "unless @organization_scoped_routes\n  resources :organizations, only: [:index]\n  get '/x' => 'y#z'\nend\n",
			want: 2,
		},
		{
			name: "if inside draw",
			src:  "Rails.application.routes.draw do\n  if Rails.env.development?\n    get '/letter_opener' => 'x#y'\n  end\nend\n",
			want: 1,
		},
		{
			// Rails' own activestorage/config/routes.rb closes the draw block with one.
			name: "if modifier wrapping the whole draw",
			src:  "Rails.application.routes.draw do\n  get '/blobs/:id' => 'blobs#show'\nend if ActiveStorage.draw_routes\n",
			want: 1,
		},
		{
			name: "else branch is walked too",
			src:  "Rails.application.routes.draw do\n  if x?\n    get '/a' => 'c#a'\n  else\n    get '/b' => 'c#b'\n  end\nend\n",
			want: 2,
		},
		{
			// solidus wraps its admin routes in a conditional inside a constraints block.
			name: "conditional nested under constraints and scope",
			src: "SolidusPromotions::Engine.routes.draw do\n  if SolidusSupport.admin_available?\n" +
				"    constraints(->(r) { true }) do\n      scope :admin do\n" +
				"        resources :promotions, only: [:index]\n      end\n    end\n  end\nend\n",
			want: 1,
		},
		{
			// The condition must NOT be dispatched as a route DSL call.
			name: "condition is not a route",
			src:  "if Rails.env.development?\n  nil\nend\n",
			want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ff := parseRouteFileAST([]byte(tc.src), "config/routes.rb")
			if len(ff) != tc.want {
				t.Errorf("got %d routes, want %d:\n  %s", len(ff), tc.want, routeKeys(routeIndex(t, tc.src)))
			}
		})
	}
}

// TestRouteHandler_HashRocketForm: `get 'path' => 'ctrl#action'` puts the handler in the
// pair's VALUE, not in a `to:` keyword. Discourse and lobsters write nearly every route
// this way, so reading only `to:` left thousands of routes with no handler.
func TestRouteHandler_HashRocketForm(t *testing.T) {
	idx := routeIndex(t, `
Rails.application.routes.draw do
  get 'about' => 'static#about'
  post '/u/:username/preferences' => 'users/preferences#update'
  get 'legacy' => redirect('/new')
  get 'rack' => SomeRackApp
  get 'both', to: 'wins#here'
end
`)
	if f := idx["GET /about"]; f.Props["handler"] != "static#about" {
		t.Errorf("handler = %v, want static#about", f.Props["handler"])
	}
	if f := idx["GET /about"]; handledBy(f) != "StaticController#about" {
		t.Errorf("handled_by = %q", handledBy(f))
	}
	if f := idx["POST /u/:username/preferences"]; f.Props["handler"] != "users/preferences#update" {
		t.Errorf("handler = %v", f.Props["handler"])
	}
	// A redirect and a Rack app name no controller action; inventing one would create a
	// handled_by edge to a node that never exists.
	for _, k := range []string{"GET /legacy", "GET /rack"} {
		f, ok := idx[k]
		if !ok {
			t.Fatalf("missing %q; have:\n  %s", k, routeKeys(idx))
		}
		if h := f.Props["handler"]; h != nil {
			t.Errorf("%s got handler %v, want none", k, h)
		}
	}
	if f := idx["GET /both"]; f.Props["handler"] != "wins#here" {
		t.Errorf("explicit to: should win: %v", f.Props["handler"])
	}
}

// TestRouteDSL_StringArguments: Rails accepts a symbol or a string for `namespace`,
// `resources` and `resource`. Reading only the symbol form made a string namespace's
// whole block invisible — four openproject module route files declared 15 routes and
// produced none.
func TestRouteDSL_StringArguments(t *testing.T) {
	idx := routeIndex(t, `
Rails.application.routes.draw do
  namespace "recaptcha" do
    get :settings, to: "admin#show"
    post :verify, to: "request#verify"
  end
  resources "widgets", only: [:index]
end
`)
	for _, want := range []string{
		"GET /recaptcha/settings",
		"POST /recaptcha/verify",
		"GET /widgets",
	} {
		if _, ok := idx[want]; !ok {
			t.Errorf("missing %q; have:\n  %s", want, routeKeys(idx))
		}
	}
}
