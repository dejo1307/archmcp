package rustextractor

import (
	"reflect"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// utoipaRoutes returns the route facts extracted from src, which is the whole
// point of the utoipa attribute: the path never appears at the registration
// site, only here.
func utoipaRoutes(t *testing.T, src string) []facts.Fact {
	t.Helper()
	var out []facts.Fact
	for _, f := range findFactsByKind(extractAST(t, src), facts.KindRoute) {
		if f.Props["framework"] == "utoipa" {
			out = append(out, f)
		}
	}
	return out
}

func routeKeys(ff []facts.Fact) [][2]string {
	out := make([][2]string, 0, len(ff))
	for _, f := range ff {
		method, _ := f.Props["method"].(string)
		out = append(out, [2]string{method, f.Name})
	}
	return out
}

func TestExtract_UtoipaAttributePathIsAServerRoute(t *testing.T) {
	ff := utoipaRoutes(t, `
#[utoipa::path(
    get,
    path = "/api/v1/widgets/{widget}",
    tag = "widgets",
    responses((status = 200, description = "found", body = WidgetResponse)),
)]
pub async fn find_widget(state: AppState) -> AppResult<Json<WidgetResponse>> {
    Ok(())
}
`)
	if len(ff) != 1 {
		t.Fatalf("expected 1 route fact, got %d: %+v", len(ff), ff)
	}
	r := ff[0]
	if r.Name != "/api/v1/widgets/{widget}" {
		t.Errorf("route Name = %q, want /api/v1/widgets/{widget}", r.Name)
	}
	if r.Props["method"] != "GET" {
		t.Errorf("method = %v, want GET", r.Props["method"])
	}
	if r.Props["language"] != "rust" {
		t.Errorf("language = %v, want rust", r.Props["language"])
	}
	if r.Props["handler"] != "find_widget" {
		t.Errorf("handler = %v, want find_widget", r.Props["handler"])
	}
	// No "role": every consumer reads role == "client" to mean a call site, so a
	// route without one is already a served endpoint — same as the Axum facts.
	if _, ok := r.Props["role"]; ok {
		t.Errorf("route carries a role prop %v; a served route sets none", r.Props["role"])
	}
	if r.File != "pkg/lib.rs" || r.Line != 2 {
		t.Errorf("route located at %s:%d, want pkg/lib.rs:2 (the attribute)", r.File, r.Line)
	}
	if !hasRelation(r, facts.RelDeclares, "pkg") {
		t.Errorf("expected a declares relation to pkg, got %+v", r.Relations)
	}
}

func TestExtract_UtoipaMultiVerbOperationIsOneFactPerVerb(t *testing.T) {
	ff := utoipaRoutes(t, `
#[utoipa::path(method(get, head), path = "/api/v1/widgets")]
pub async fn list_widgets() {}
`)
	got := routeKeys(ff)
	want := [][2]string{{"GET", "/api/v1/widgets"}, {"HEAD", "/api/v1/widgets"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

func TestExtract_UtoipaContextPathPrefixesThePath(t *testing.T) {
	ff := utoipaRoutes(t, `
#[utoipa::path(delete, context_path = "/api/v1", path = "/widgets/{name}")]
pub async fn delete_widget() {}
`)
	if len(ff) != 1 {
		t.Fatalf("expected 1 route fact, got %d: %+v", len(ff), ff)
	}
	if ff[0].Name != "/api/v1/widgets/{name}" {
		t.Errorf("route Name = %q, want /api/v1/widgets/{name}", ff[0].Name)
	}
	if ff[0].Props["method"] != "DELETE" {
		t.Errorf("method = %v, want DELETE", ff[0].Props["method"])
	}
}

func TestExtract_UtoipaNonLiteralPathIsNotGuessed(t *testing.T) {
	for name, src := range map[string]string{
		"const":   `#[utoipa::path(post, path = PATH)]` + "\npub async fn h() {}\n",
		"concat":  `#[utoipa::path(post, path = concat!("/a", "/b"))]` + "\npub async fn h() {}\n",
		"no path": `#[utoipa::path(post, tag = "x")]` + "\npub async fn h() {}\n",
		"no verb": `#[utoipa::path(path = "/a")]` + "\npub async fn h() {}\n",
	} {
		if ff := utoipaRoutes(t, src); len(ff) != 0 {
			t.Errorf("%s: expected no route fact, got %+v", name, ff)
		}
	}
}

// The attribute's own options nest token_trees that carry `key = value` pairs of
// their own, so reading the path from anywhere but the attribute's direct
// children stores a parameter description as the route.
func TestExtract_UtoipaNestedOptionsDoNotSupplyThePath(t *testing.T) {
	ff := utoipaRoutes(t, `
#[utoipa::path(
    get,
    params(("widget" = String, Path, description = "/not/the/path", example = "x")),
    path = "/api/v1/widgets/{widget}",
    responses((status = 200, description = "/also/not/it")),
)]
pub async fn find_widget() {}
`)
	got := routeKeys(ff)
	want := [][2]string{{"GET", "/api/v1/widgets/{widget}"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

// #[deprecated] and #[cfg(…)] sit between the utoipa attribute and its function
// in real code; the pending attribute has to survive them.
func TestExtract_UtoipaSurvivesInterveningAttributes(t *testing.T) {
	ff := utoipaRoutes(t, `
#[utoipa::path(put, path = "/api/v1/widgets/new")]
#[deprecated]
#[allow(clippy::too_many_arguments)]
pub async fn publish() {}
`)
	got := routeKeys(ff)
	want := [][2]string{{"PUT", "/api/v1/widgets/new"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
	if ff[0].Props["handler"] != "publish" {
		t.Errorf("handler = %v, want publish", ff[0].Props["handler"])
	}
}

func TestExtract_UtoipaHandlerInAModOrImplIsStillARoute(t *testing.T) {
	ff := utoipaRoutes(t, `
mod controllers {
    #[utoipa::path(get, path = "/api/v1/session")]
    pub async fn me() {}
}

impl Admin {
    #[utoipa::path(get, path = "/api/private/admin")]
    pub async fn list() {}
}
`)
	got := routeKeys(ff)
	want := [][2]string{{"GET", "/api/v1/session"}, {"GET", "/api/private/admin"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

func TestExtract_UtoipaRouteInATestModuleIsNotServed(t *testing.T) {
	ff := utoipaRoutes(t, `
#[utoipa::path(get, path = "/api/v1/real")]
pub async fn real() {}

#[cfg(test)]
mod tests {
    #[utoipa::path(get, path = "/api/v1/fixture")]
    pub async fn fixture() {}
}
`)
	got := routeKeys(ff)
	want := [][2]string{{"GET", "/api/v1/real"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

// A bare #[path(…)] is Rust's own module-path attribute and says nothing about
// HTTP, so only the scoped spelling may produce a route.
func TestExtract_UtoipaBarePathAttributeIsNotARoute(t *testing.T) {
	if ff := utoipaRoutes(t, `
#[path = "/api/v1/nope"]
mod other;

#[path(get, path = "/api/v1/still-nope")]
pub async fn h() {}
`); len(ff) != 0 {
		t.Errorf("expected no route facts, got %+v", ff)
	}
}

func TestExtract_UtoipaRouteOrderIsDeterministic(t *testing.T) {
	src := `
#[utoipa::path(method(post, put), path = "/b")]
pub async fn b() {}

#[utoipa::path(get, path = "/a")]
pub async fn a() {}
`
	want := routeKeys(utoipaRoutes(t, src))
	if len(want) != 3 {
		t.Fatalf("expected 3 route facts, got %d: %v", len(want), want)
	}
	for i := 0; i < 5; i++ {
		if got := routeKeys(utoipaRoutes(t, src)); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: routes = %v, want %v", i, got, want)
		}
	}
}
