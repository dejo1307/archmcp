package routeindex

import (
	"testing"

	"github.com/enola-labs/enola/internal/linkers/vocab"
)

func TestNormalizePath(t *testing.T) {
	m := New(vocab.Default())
	cases := map[string]string{
		"/api/items/{id}":         "/api/items/{}",
		"/api/items/:id":          "/api/items/{}",
		"/api/items/<id>":         "/api/items/{}",
		"/api/items/[id]":         "/api/items/{}",
		"/api/items/":             "/api/items",
		"/api/items":              "/api/items",
		"/":                       "/",
		"/users/{uid}/pets/{pid}": "/users/{}/pets/{}",
		// Response-format suffix stripped from the final segment (Rails ".:format").
		"/v2/devices/{id}.json": "/v2/devices/{}",
		"/v2/bookmarks.json":    "/v2/bookmarks",
		"/v2/report.xml":        "/v2/report",
		// A version-like or genuinely dotted segment is not a format suffix.
		"/api/v2.5/items": "/api/v2.5/items",
	}
	for in, want := range cases {
		if got := m.NormalizePath(in); got != want {
			t.Errorf("m.NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsGenericPath(t *testing.T) {
	m := New(vocab.Default())
	generic := []string{"/health", "/status", "/metrics", "/"}
	for _, p := range generic {
		if !m.IsGenericPath(m.NormalizePath(p)) {
			t.Errorf("m.IsGenericPath(%q) = false, want true", p)
		}
	}
	specific := []string{"/api/items", "/items/{id}", "/a/b"}
	for _, p := range specific {
		if m.IsGenericPath(m.NormalizePath(p)) {
			t.Errorf("m.IsGenericPath(%q) = true, want false", p)
		}
	}
	// The rest of the infra/mount vocabulary: a lone segment is generic because
	// of what it is NAMED, so every one of these must stay unlinkable.
	for _, p := range []string{
		"/healthz", "/healthcheck", "/ping", "/ready", "/readyz",
		"/live", "/livez", "/version", "/info", "/api", "/graphql", "/HEALTH",
	} {
		if !m.IsGenericPath(m.NormalizePath(p)) {
			t.Errorf("m.IsGenericPath(%q) = false, want true (generic vocabulary)", p)
		}
	}
	// Named single-segment endpoints are linkable: a pure segment count used to
	// make these unlinkable, silently dropping real cross-repo edges (an
	// /activate client could never resolve to its /activate server).
	for _, p := range []string{"/activate", "/checkout", "/subscribe", "/{id}"} {
		if m.IsGenericPath(m.NormalizePath(p)) {
			t.Errorf("m.IsGenericPath(%q) = true, want false (named endpoint)", p)
		}
	}
}

func TestSingleSegmentPath(t *testing.T) {
	m := New(vocab.Default())
	for _, p := range []string{"/activate", "/checkout", "activate"} {
		if !SingleSegmentPath(m.NormalizePath(p)) {
			t.Errorf("SingleSegmentPath(%q) = false, want true", p)
		}
	}
	// Multi-segment, parameterized, and empty paths do not take the stricter
	// unambiguous-provider gate in linkHTTP.
	for _, p := range []string{"/a/b", "/items/{id}", "/{id}", "/"} {
		if SingleSegmentPath(m.NormalizePath(p)) {
			t.Errorf("SingleSegmentPath(%q) = true, want false", p)
		}
	}
}
