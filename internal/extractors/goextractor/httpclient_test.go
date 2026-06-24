package goextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// goClientRoutes filters facts to the outbound HTTP-client routes (excludes
// server routes emitted by extractRoutes).
func goClientRoutes(ff []facts.Fact) []facts.Fact {
	var out []facts.Fact
	for _, f := range ff {
		if f.Kind == facts.KindRoute && f.Props["role"] == "client" {
			out = append(out, f)
		}
	}
	return out
}

func clientRouteByPath(ff []facts.Fact, path string) (facts.Fact, bool) {
	for _, f := range goClientRoutes(ff) {
		if f.Name == path {
			return f, true
		}
	}
	return facts.Fact{}, false
}

func TestGoHTTPClient_NewRequestAndHelpers(t *testing.T) {
	src := `package svc

import (
	"context"
	"net/http"
)

type C struct{ baseURL string }

func (c *C) calls(ctx context.Context) {
	http.NewRequest("POST", c.baseURL+"/api/checkout/build", nil)
	http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/api/items/update", nil)
	http.Get(c.baseURL + "/api/items/list")
	http.Post(c.baseURL+"/api/orders/new", "application/json", nil)
}
`
	ff := extractAll(t, map[string]string{"svc/client.go": src})

	cases := map[string]string{
		"/api/checkout/build": "POST",
		"/api/items/update":   "PUT",
		"/api/items/list":     "GET",
		"/api/orders/new":     "POST",
	}
	for path, method := range cases {
		f, ok := clientRouteByPath(ff, path)
		if !ok {
			t.Errorf("missing client route %s", path)
			continue
		}
		if f.Props["method"] != method {
			t.Errorf("%s method = %v, want %s", path, f.Props["method"], method)
		}
		if f.Props["source"] != "go-http-client" {
			t.Errorf("%s source = %v, want go-http-client", path, f.Props["source"])
		}
	}
}

func TestGoHTTPClient_WrapperSingleArg(t *testing.T) {
	src := `package svc

import "net/http"

var _ = http.MethodGet

type api struct{ client interface{ Get(string) error } }

func (a *api) load(baseURL string) {
	a.client.Get(baseURL + "/api/profile/me")
}
`
	ff := extractAll(t, map[string]string{"svc/api.go": src})
	if _, ok := clientRouteByPath(ff, "/api/profile/me"); !ok {
		t.Errorf("missing wrapper client route /api/profile/me: %+v", goClientRoutes(ff))
	}
}

func TestGoHTTPClient_EnvVarHint(t *testing.T) {
	src := `package svc

import (
	"net/http"
	"os"
)

func call() {
	http.Get(os.Getenv("XENDO_URL") + "/api/things/list")
}
`
	ff := extractAll(t, map[string]string{"svc/x.go": src})
	f, ok := clientRouteByPath(ff, "/api/things/list")
	if !ok {
		t.Fatalf("missing client route /api/things/list")
	}
	if f.Props["target_hint"] != "xendo" {
		t.Errorf("target_hint = %v, want xendo", f.Props["target_hint"])
	}
}

func TestGoHTTPClient_NotServerHandler(t *testing.T) {
	// chi route registration (2 args) and handler writes must NOT become client
	// routes; only the server route from extractRoutes should exist.
	src := `package svc

import "net/http"

func register(r interface {
	Get(string, http.HandlerFunc)
}) {
	r.Get("/api/widgets", handleWidgets)
}

func handleWidgets(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(200)
	w.Write([]byte("ok"))
}
`
	ff := extractAll(t, map[string]string{"svc/server.go": src})
	for _, f := range goClientRoutes(ff) {
		t.Errorf("unexpected client route from server code: %s", f.Name)
	}
}

func TestGoHTTPClient_SkipsExternal(t *testing.T) {
	src := `package svc

import "net/http"

func call() {
	http.Get("https://api.stripe.com/v1/charges")
}
`
	ff := extractAll(t, map[string]string{"svc/ext.go": src})
	if rs := goClientRoutes(ff); len(rs) != 0 {
		t.Errorf("got %d client routes for external URL, want 0: %+v", len(rs), rs)
	}
}
