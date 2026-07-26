package pythonextractor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// extractRepo writes files to a temp repo and runs the full Python extractor, so
// the repo-wide include_router fixpoint runs (mount prefixes routinely cross
// module boundaries, so a per-file helper cannot exercise it).
func extractRepo(t *testing.T, files map[string]string) []facts.Fact {
	t.Helper()
	dir := t.TempDir()
	rel := make([]string, 0, len(files))
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		rel = append(rel, name)
	}
	sort.Strings(rel)
	got, err := New().Extract(context.Background(), dir, rel)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// routePaths returns "METHOD path" for every server route, sorted.
func routePaths(ff []facts.Fact) []string {
	var out []string
	for _, f := range ff {
		if f.Kind != facts.KindRoute || f.Props["role"] != "server" {
			continue
		}
		method, _ := f.Props["method"].(string)
		out = append(out, method+" "+f.Name)
	}
	sort.Strings(out)
	return out
}

func wantRoutes(t *testing.T, ff []facts.Fact, want ...string) {
	t.Helper()
	got := routePaths(ff)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes =\n  %q\nwant\n  %q", got, want)
	}
}

const fastapiProject = "[project]\nname = \"svc\"\ndependencies = [\"fastapi\"]\n"

// A router built inside a factory function is the canonical FastAPI idiom, and
// the decorator sits in a function body — which module-level statement walking
// never reaches. Before this, such a backend emitted no route facts at all.
func TestRouterPrefix_FactoryRouterRoutesExtracted(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"pyproject.toml": fastapiProject,
		"api/search.py": `
from fastapi import APIRouter

def get_search_router() -> APIRouter:
    router = APIRouter()

    @router.get("/results")
    async def results():
        return []

    return router
`,
	})
	// Unmounted factory: routes are extracted, at their bare path.
	wantRoutes(t, ff, "GET /results")
}

// A class body is walked twice by design (handleClass: walkBody for owners, then
// walkForCalls for class-level expressions), so a decorated method reaches both
// the module-level and the nested-scope emit paths. It must still emit once.
func TestRouterPrefix_ClassMethodRouteEmittedOnce(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"pyproject.toml": fastapiProject,
		"api/views.py": `
class HealthView(BaseView):
    @expose("/health", methods=["GET"])
    def health(self):
        pass
`,
	})
	wantRoutes(t, ff, "GET /health")
}

func TestRouterPrefix_IncludeRouterComposesPrefix(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"pyproject.toml": fastapiProject,
		"api/search.py": `
from fastapi import APIRouter

def get_search_router() -> APIRouter:
    router = APIRouter()

    @router.get("/results")
    async def results():
        return []

    return router
`,
		"api/client.py": `
from fastapi import FastAPI
from api.search import get_search_router

app = FastAPI()
app.include_router(get_search_router(), prefix="/api/v1/search", tags=["search"])
`,
	})
	wantRoutes(t, ff, "GET /api/v1/search/results")
}

// `@router.post("/")` is FastAPI's idiom for "the router root". Composed with a
// mount prefix it must collapse to the prefix, not produce a trailing slash.
func TestRouterPrefix_RouterRootCollapsesToPrefix(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"pyproject.toml": fastapiProject,
		"api/cognify.py": `
from fastapi import APIRouter

def get_cognify_router() -> APIRouter:
    router = APIRouter()

    @router.post("/")
    async def cognify(payload: dict):
        return {}

    return router
`,
		"api/client.py": `
from fastapi import FastAPI
from api.cognify import get_cognify_router

app = FastAPI()
app.include_router(get_cognify_router(), prefix="/api/v1/cognify")
`,
	})
	wantRoutes(t, ff, "POST /api/v1/cognify")
}

// The other dominant idiom: a module-level `router = APIRouter(prefix=...)`
// mounted by name. The constructor prefix and the mount prefix both apply.
func TestRouterPrefix_ModuleRouterCtorPrefix(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"pyproject.toml": fastapiProject,
		"api/items.py": `
from fastapi import APIRouter

router = APIRouter(prefix="/items")

@router.get("/{item_id}")
async def get_item(item_id: str):
    return {}
`,
		"api/client.py": `
from fastapi import FastAPI
from api import items

app = FastAPI()
app.include_router(items.router, prefix="/api/v1")
`,
	})
	wantRoutes(t, ff, "GET /api/v1/items/{item_id}")
}

// Real projects re-export the factory through a package __init__, so the import
// target names the package, not the file the factory lives in. The unique-name
// fallback carries the mount across that indirection.
func TestRouterPrefix_ReexportedFactoryResolves(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"pyproject.toml":             fastapiProject,
		"api/v1/routers/__init__.py": "from .get_search_router import get_search_router\n",
		"api/v1/routers/get_search_router.py": `
from fastapi import APIRouter

def get_search_router() -> APIRouter:
    router = APIRouter()

    @router.get("/results")
    async def results():
        return []

    return router
`,
		"api/client.py": `
from fastapi import FastAPI
from api.v1.routers import get_search_router

app = FastAPI()
app.include_router(get_search_router(), prefix="/api/v1/search")
`,
	})
	wantRoutes(t, ff, "GET /api/v1/search/results")
}

// A router mounted into a router mounted into the app composes both hops.
func TestRouterPrefix_NestedMountsCompose(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"pyproject.toml": fastapiProject,
		"api/users.py": `
from fastapi import APIRouter

router = APIRouter()

@router.get("/me")
async def me():
    return {}
`,
		"api/v1.py": `
from fastapi import APIRouter
from api import users

api_router = APIRouter()
api_router.include_router(users.router, prefix="/users")
`,
		"api/client.py": `
from fastapi import FastAPI
from api import v1

app = FastAPI()
app.include_router(v1.api_router, prefix="/api/v1")
`,
	})
	wantRoutes(t, ff, "GET /api/v1/users/me")
}

// A router mounted at two prefixes serves both paths, so it emits both — the
// same fan-out the Go and Axum composers perform.
func TestRouterPrefix_MultipleMountPoints(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"pyproject.toml": fastapiProject,
		"api/health.py": `
from fastapi import APIRouter

router = APIRouter()

@router.get("/ping")
async def ping():
    return {}
`,
		"api/client.py": `
from fastapi import FastAPI
from api import health

app = FastAPI()
app.include_router(health.router, prefix="/api/v1")
app.include_router(health.router, prefix="/internal")
`,
	})
	wantRoutes(t, ff, "GET /api/v1/ping", "GET /internal/ping")
}

// A prefix the extractor cannot read (an f-string, a settings lookup) must leave
// the route at its bare path rather than guess.
func TestRouterPrefix_UnresolvedMountKeepsBarePath(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"pyproject.toml": fastapiProject,
		"api/items.py": `
from fastapi import APIRouter

router = APIRouter()

@router.get("/{item_id}")
async def get_item(item_id: str):
    return {}
`,
		"api/client.py": `
from fastapi import FastAPI
from api import items
from api.settings import API_PREFIX

app = FastAPI()
app.include_router(items.router, prefix=f"{API_PREFIX}/items")
`,
	})
	wantRoutes(t, ff, "GET /{item_id}")
}

// Two factories sharing a name in different modules cannot be told apart by the
// bare-name fallback, so the mount is dropped and both keep their bare paths —
// a miss, never a fabricated prefix.
func TestRouterPrefix_AmbiguousNameKeepsBarePath(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"pyproject.toml": fastapiProject,
		"api/a/routers.py": `
from fastapi import APIRouter

def get_router() -> APIRouter:
    router = APIRouter()

    @router.get("/alpha")
    async def alpha():
        return {}

    return router
`,
		"api/b/routers.py": `
from fastapi import APIRouter

def get_router() -> APIRouter:
    router = APIRouter()

    @router.get("/beta")
    async def beta():
        return {}

    return router
`,
		"api/client.py": `
from fastapi import FastAPI
from api.pkg.routers import get_router

app = FastAPI()
app.include_router(get_router(), prefix="/api/v1")
`,
	})
	wantRoutes(t, ff, "GET /alpha", "GET /beta")
}

// Flask Blueprint url_prefix folding is a separate gap (GAP-PY-06): only routers
// built by APIRouter()/FastAPI() form groups, so Flask keeps its bare-leaf Name.
func TestRouterPrefix_FlaskBlueprintNotFolded(t *testing.T) {
	ff := extractRepo(t, map[string]string{
		"requirements.txt": "flask\n",
		"app/views.py": `
from flask import Blueprint

bp = Blueprint("api", __name__, url_prefix="/api")

@bp.route("/ping")
def ping():
    return "pong"
`,
	})
	wantRoutes(t, ff, "GET /ping")
}

// Mount resolution and prefix emission are sorted throughout, so repeated runs
// over the same sources produce byte-identical route facts.
func TestRouterPrefix_Deterministic(t *testing.T) {
	files := map[string]string{
		"pyproject.toml": fastapiProject,
		"api/one.py": `
from fastapi import APIRouter

router = APIRouter()

@router.get("/a")
async def a():
    return {}
`,
		"api/two.py": `
from fastapi import APIRouter

router = APIRouter()

@router.get("/b")
async def b():
    return {}
`,
		"api/client.py": `
from fastapi import FastAPI
from api import one, two

app = FastAPI()
app.include_router(one.router, prefix="/x")
app.include_router(two.router, prefix="/y")
app.include_router(one.router, prefix="/z")
`,
	}
	first := routePaths(extractRepo(t, files))
	for i := 0; i < 5; i++ {
		if got := routePaths(extractRepo(t, files)); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d = %q, want %q", i, got, first)
		}
	}
	want := []string{"GET /x/a", "GET /y/b", "GET /z/a"}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("routes = %q, want %q", first, want)
	}
}
