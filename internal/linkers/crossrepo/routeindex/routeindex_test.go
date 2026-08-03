package routeindex

import "testing"

func TestNormalizePath(t *testing.T) {
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
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsGenericPath(t *testing.T) {
	generic := []string{"/health", "/status", "/metrics", "/"}
	for _, p := range generic {
		if !IsGenericPath(NormalizePath(p)) {
			t.Errorf("IsGenericPath(%q) = false, want true", p)
		}
	}
	specific := []string{"/api/items", "/items/{id}", "/a/b"}
	for _, p := range specific {
		if IsGenericPath(NormalizePath(p)) {
			t.Errorf("IsGenericPath(%q) = true, want false", p)
		}
	}
	// The rest of the infra/mount vocabulary: a lone segment is generic because
	// of what it is NAMED, so every one of these must stay unlinkable.
	for _, p := range []string{
		"/healthz", "/healthcheck", "/ping", "/ready", "/readyz",
		"/live", "/livez", "/version", "/info", "/api", "/graphql", "/HEALTH",
	} {
		if !IsGenericPath(NormalizePath(p)) {
			t.Errorf("IsGenericPath(%q) = false, want true (generic vocabulary)", p)
		}
	}
	// Named single-segment endpoints are linkable: a pure segment count used to
	// make these unlinkable, silently dropping real cross-repo edges (an
	// /activate client could never resolve to its /activate server).
	for _, p := range []string{"/activate", "/checkout", "/subscribe", "/{id}"} {
		if IsGenericPath(NormalizePath(p)) {
			t.Errorf("IsGenericPath(%q) = true, want false (named endpoint)", p)
		}
	}
}

func TestSingleSegmentPath(t *testing.T) {
	for _, p := range []string{"/activate", "/checkout", "activate"} {
		if !SingleSegmentPath(NormalizePath(p)) {
			t.Errorf("SingleSegmentPath(%q) = false, want true", p)
		}
	}
	// Multi-segment, parameterized, and empty paths do not take the stricter
	// unambiguous-provider gate in linkHTTP.
	for _, p := range []string{"/a/b", "/items/{id}", "/{id}", "/"} {
		if SingleSegmentPath(NormalizePath(p)) {
			t.Errorf("SingleSegmentPath(%q) = true, want false", p)
		}
	}
}
