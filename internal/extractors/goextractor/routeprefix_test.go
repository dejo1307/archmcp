package goextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// routeNames returns the set of route Names (paths) with the given method.
func routeNames(ff []facts.Fact, method string) map[string]bool {
	set := map[string]bool{}
	for _, f := range ff {
		if f.Kind == facts.KindRoute && f.Props["method"] == method {
			set[f.Name] = true
		}
	}
	return set
}

func hasRoute(ff []facts.Fact, path, method string) bool {
	for _, f := range ff {
		if f.Kind == facts.KindRoute && f.Name == path && f.Props["method"] == method {
			return true
		}
	}
	return false
}

// A subrouter created in one function and passed to a registration function in
// the SAME package must carry its prefix into the callee's bare-path routes.
func TestExtractRoutes_Interprocedural_SamePackage(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"internal/server/routes.go": `package server

import "github.com/gorilla/mux"

func Setup() {
	router := mux.NewRouter()
	apiRouter := router.PathPrefix("/api").Subrouter()
	RegisterCourseRoutes(apiRouter)
	_ = router
}

func RegisterCourseRoutes(r *mux.Router) {
	r.HandleFunc("/courses", listCourses).Methods("GET")
	r.HandleFunc("/courses/{id}", getCourse).Methods("GET")
}

func listCourses() {}
func getCourse()   {}
`,
	})

	if !hasRoute(ff, "/api/courses", "GET") {
		t.Errorf("want /api/courses GET; routes = %v", routeNames(ff, "GET"))
	}
	if !hasRoute(ff, "/api/courses/{id}", "GET") {
		t.Errorf("want /api/courses/{id} GET; routes = %v", routeNames(ff, "GET"))
	}
	// The bare (un-prefixed) forms must NOT appear.
	if hasRoute(ff, "/courses", "GET") {
		t.Errorf("bare /courses GET should have been prefixed away")
	}
}

// Two hops across packages, with the callee itself creating a nested subrouter:
// server → (mounts /api/settings) → courses.Register (mounts /courses under it).
func TestExtractRoutes_Interprocedural_CrossPackage(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"internal/server/server.go": `package server

import (
	"github.com/gorilla/mux"
	"testmod/internal/courses"
)

func Setup() {
	router := mux.NewRouter()
	apiRouter := router.PathPrefix("/api").Subrouter()
	settingsRouter := apiRouter.PathPrefix("/settings").Subrouter()
	courses.Register(settingsRouter)
	_ = router
}
`,
		"internal/courses/courses.go": `package courses

import "github.com/gorilla/mux"

func Register(r *mux.Router) {
	rulesRouter := r.PathPrefix("/courses").Subrouter()
	rulesRouter.HandleFunc("/{id}/rules", listRules).Methods("GET")
}

func listRules() {}
`,
	})

	if !hasRoute(ff, "/api/settings/courses/{id}/rules", "GET") {
		t.Errorf("want /api/settings/courses/{id}/rules GET; routes = %v", routeNames(ff, "GET"))
	}
}

// A registration function mounted at two different prefixes must emit its routes
// once per mount point.
func TestExtractRoutes_Interprocedural_MultipleMountPoints(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"internal/server/routes.go": `package server

import "github.com/gorilla/mux"

func Setup() {
	router := mux.NewRouter()
	apiRouter := router.PathPrefix("/api").Subrouter()
	adminRouter := router.PathPrefix("/admin").Subrouter()
	register(apiRouter)
	register(adminRouter)
	_ = router
}

func register(r *mux.Router) {
	r.HandleFunc("/things", listThings).Methods("GET")
}

func listThings() {}
`,
	})

	if !hasRoute(ff, "/api/things", "GET") {
		t.Errorf("want /api/things GET; routes = %v", routeNames(ff, "GET"))
	}
	if !hasRoute(ff, "/admin/things", "GET") {
		t.Errorf("want /admin/things GET; routes = %v", routeNames(ff, "GET"))
	}
}

// The chi router interface variant must compose across functions too.
func TestExtractRoutes_Interprocedural_Chi(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"internal/server/routes.go": `package server

import "github.com/go-chi/chi/v5"

func Setup() {
	router := chi.NewRouter()
	router.Route("/api", func(r chi.Router) {})
	registerChi(router)
	_ = router
}

func registerChi(r chi.Router) {
	r.Get("/widgets", listWidgets)
}

func listWidgets() {}
`,
	})

	// registerChi is called with the root router (prefix ""), so /widgets is bare —
	// but the point is the cross-function seed path works for a chi.Router param
	// without panicking and the route is still emitted.
	if !hasRoute(ff, "/widgets", "GET") {
		t.Errorf("want /widgets GET; routes = %v", routeNames(ff, "GET"))
	}
}

// A registration function never reached from any root keeps today's bare-path
// behavior (graceful degradation) rather than being dropped.
func TestExtractRoutes_Interprocedural_Unreached_KeepsBarePath(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"internal/server/routes.go": `package server

import "github.com/gorilla/mux"

// Never called from a NewRouter root.
func RegisterOrphan(r *mux.Router) {
	r.HandleFunc("/orphan", h).Methods("GET")
}

func h() {}
`,
	})

	if !hasRoute(ff, "/orphan", "GET") {
		t.Errorf("want bare /orphan GET (graceful degradation); routes = %v", routeNames(ff, "GET"))
	}
}

// The dominant real-world pattern: routes registered by a METHOD on a handler
// struct (`func (h *Handler) RegisterRoutes(r *mux.Router)`), called via a local
// variable receiver. Resolving it requires the receiver's type, which the shared
// resolveChain resolver recovers from the constructor convention.
func TestExtractRoutes_Interprocedural_MethodLocalReceiver(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"internal/courses/handler.go": `package courses

import "github.com/gorilla/mux"

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRoutes(r *mux.Router) {
	r.HandleFunc("/courses/{id}", h.get).Methods("GET")
}

func (h *Handler) get() {}
`,
		"internal/server/server.go": `package server

import (
	"github.com/gorilla/mux"
	"testmod/internal/courses"
)

func Setup() {
	router := mux.NewRouter()
	api := router.PathPrefix("/api/settings").Subrouter()
	h := courses.NewHandler()
	h.RegisterRoutes(api)
	_ = router
}
`,
	})

	if !hasRoute(ff, "/api/settings/courses/{id}", "GET") {
		t.Errorf("want /api/settings/courses/{id} GET (method via local receiver); routes = %v", routeNames(ff, "GET"))
	}
}

// The golf pattern exactly: a registration method reached through a FIELD of the
// caller's receiver (`h.sub.RegisterRoutes(subrouter)`), resolved via fieldTypes.
func TestExtractRoutes_Interprocedural_MethodFieldReceiver(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"internal/courses/handler.go": `package courses

import "github.com/gorilla/mux"

type Handler struct{}

func (h *Handler) RegisterRoutes(r *mux.Router) {
	r.HandleFunc("/rules/{id}", h.get).Methods("GET")
}

func (h *Handler) get() {}
`,
		"internal/server/server.go": `package server

import (
	"github.com/gorilla/mux"
	"testmod/internal/courses"
)

type App struct {
	coursesHandler *courses.Handler
}

func (a *App) Setup() {
	router := mux.NewRouter()
	api := router.PathPrefix("/api/settings").Subrouter()
	a.coursesHandler.RegisterRoutes(api)
	_ = router
}
`,
	})

	if !hasRoute(ff, "/api/settings/rules/{id}", "GET") {
		t.Errorf("want /api/settings/rules/{id} GET (method via field receiver); routes = %v", routeNames(ff, "GET"))
	}
}

// Mutually-recursive registration functions must terminate (the fixpoint's
// visited-set guards the cycle) and still compose the reachable prefix.
func TestExtractRoutes_Interprocedural_Cycle(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"internal/server/routes.go": `package server

import "github.com/gorilla/mux"

func Setup() {
	router := mux.NewRouter()
	apiRouter := router.PathPrefix("/api").Subrouter()
	a(apiRouter)
	_ = router
}

func a(r *mux.Router) {
	r.HandleFunc("/a", h).Methods("GET")
	b(r)
}

func b(r *mux.Router) {
	r.HandleFunc("/b", h).Methods("GET")
	a(r)
}

func h() {}
`,
	})

	if !hasRoute(ff, "/api/a", "GET") || !hasRoute(ff, "/api/b", "GET") {
		t.Errorf("want /api/a and /api/b GET; routes = %v", routeNames(ff, "GET"))
	}
}

// Extraction must be deterministic: the multiset of route names is identical
// across runs (guards against map-iteration nondeterminism feeding the goldens).
func TestExtractRoutes_Interprocedural_Deterministic(t *testing.T) {
	files := map[string]string{
		"internal/server/routes.go": `package server

import "github.com/gorilla/mux"

func Setup() {
	router := mux.NewRouter()
	apiRouter := router.PathPrefix("/api").Subrouter()
	adminRouter := router.PathPrefix("/admin").Subrouter()
	register(apiRouter)
	register(adminRouter)
	_ = router
}

func register(r *mux.Router) {
	r.HandleFunc("/things", h).Methods("GET")
	r.HandleFunc("/things/{id}", h).Methods("GET")
}

func h() {}
`,
	}
	first := routeNames(extractAll(t, files), "GET")
	for i := 0; i < 5; i++ {
		got := routeNames(extractAll(t, files), "GET")
		if len(got) != len(first) {
			t.Fatalf("run %d: route count %d != %d", i, len(got), len(first))
		}
		for name := range first {
			if !got[name] {
				t.Fatalf("run %d: missing route %q", i, name)
			}
		}
	}
	// Sanity: both mount points present.
	for _, want := range []string{"/api/things", "/admin/things", "/api/things/{id}", "/admin/things/{id}"} {
		if !first[want] {
			t.Errorf("want %s in %v", want, first)
		}
	}
}
