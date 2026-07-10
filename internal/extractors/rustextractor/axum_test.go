package rustextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestAxum_SingleRoute(t *testing.T) {
	ff := extractAST(t, `
async fn root() -> &'static str {
    "hello"
}

fn app() -> Router {
    Router::new().route("/", get(root))
}
`)
	routes := findFactsByKind(ff, facts.KindRoute)
	if len(routes) != 1 {
		t.Fatalf("expected 1 route fact, got %d: %+v", len(routes), routes)
	}
	r := routes[0]
	if r.Name != "/" {
		t.Errorf("route Name = %q, want \"/\"", r.Name)
	}
	if r.Props["method"] != "GET" {
		t.Errorf("method = %v, want GET", r.Props["method"])
	}
	if r.Props["framework"] != "axum" {
		t.Errorf("framework = %v, want axum", r.Props["framework"])
	}
	if r.Props["handler"] != "root" {
		t.Errorf("handler = %v, want root", r.Props["handler"])
	}
}

func TestAxum_ChainedMethodsOnSamePath(t *testing.T) {
	ff := extractAST(t, `
fn app() -> Router {
    Router::new().route("/users", get(list_users).post(create_user))
}
`)
	routes := findFactsByKind(ff, facts.KindRoute)
	if len(routes) != 2 {
		t.Fatalf("expected 2 route facts (GET+POST on the same path), got %d: %+v", len(routes), routes)
	}
	var gotGet, gotPost bool
	for _, r := range routes {
		if r.Name != "/users" {
			t.Errorf("route Name = %q, want /users", r.Name)
		}
		switch r.Props["method"] {
		case "GET":
			gotGet = true
			if r.Props["handler"] != "list_users" {
				t.Errorf("GET handler = %v, want list_users", r.Props["handler"])
			}
		case "POST":
			gotPost = true
			if r.Props["handler"] != "create_user" {
				t.Errorf("POST handler = %v, want create_user", r.Props["handler"])
			}
		}
	}
	if !gotGet || !gotPost {
		t.Errorf("expected both GET and POST routes, got %+v", routes)
	}
}

func TestAxum_MultipleRoutesChained(t *testing.T) {
	ff := extractAST(t, `
fn app() -> Router {
    Router::new()
        .route("/", get(root))
        .route("/users", get(list_users).post(create_user))
        .route("/users/:id", delete(delete_user))
}
`)
	routes := findFactsByKind(ff, facts.KindRoute)
	if len(routes) != 4 {
		t.Fatalf("expected 4 route facts, got %d: %+v", len(routes), routes)
	}
	paths := map[string]int{}
	for _, r := range routes {
		paths[r.Name]++
	}
	for _, want := range []string{"/", "/users", "/users/:id"} {
		if paths[want] == 0 {
			t.Errorf("expected at least one route for path %q, got %+v", want, paths)
		}
	}
}

func TestAxum_QualifiedHandlerPath(t *testing.T) {
	ff := extractAST(t, `
fn app() -> Router {
    Router::new().route("/health", get(handlers::health_check))
}
`)
	routes := findFactsByKind(ff, facts.KindRoute)
	if len(routes) != 1 {
		t.Fatalf("expected 1 route fact, got %d", len(routes))
	}
	if routes[0].Props["handler"] != "handlers::health_check" {
		t.Errorf("handler = %v, want handlers::health_check", routes[0].Props["handler"])
	}
}

func TestAxum_NonRouteCallsIgnored(t *testing.T) {
	// A `.route(` call whose shape doesn't match (non-literal path, or a
	// second arg that isn't a verb-builder chain) must not emit anything.
	ff := extractAST(t, `
fn other(x: SomeBuilder, path: String, svc: SomeService) {
    x.route(path, svc);
}
`)
	if routes := findFactsByKind(ff, facts.KindRoute); len(routes) != 0 {
		t.Errorf("expected 0 route facts for a non-matching .route(...) call, got %+v", routes)
	}
}

func TestAxum_NoHandlerStillEmitsRoute(t *testing.T) {
	// A closure handler (or anything else axumFirstArgName can't name) still
	// produces a route fact, just without a "handler" prop.
	ff := extractAST(t, `
fn app() -> Router {
    Router::new().route("/", get(|| async { "hi" }))
}
`)
	routes := findFactsByKind(ff, facts.KindRoute)
	if len(routes) != 1 {
		t.Fatalf("expected 1 route fact, got %d", len(routes))
	}
	if _, present := routes[0].Props["handler"]; present {
		t.Errorf("expected no handler prop for a closure handler, got %v", routes[0].Props["handler"])
	}
}
