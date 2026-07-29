package facts

import (
	"strings"
	"testing"
)

// TestJoinRoutePath pins the contract the four extractor copies were consolidated
// onto. The cases marked "was unrooted" are the only behavioural change: Axum and
// FastAPI previously returned the leaf as written when the prefix was empty, so a
// route declared without a leading slash stayed unrooted and could not match the
// linker's "/"-rooted suffix index.
func TestJoinRoutePath(t *testing.T) {
	for _, tc := range []struct{ base, sub, want, note string }{
		// The shape every DSL produces: rooted prefix, rooted leaf.
		{"/v2/slots", "/available", "/v2/slots/available", ""},
		{"/api/v1/search", "/results", "/api/v1/search/results", ""},

		// Only one side present.
		{"", "/results", "/results", ""},
		{"/v2/slots", "", "/v2/slots", ""},
		{"/v2/slots", "/", "/v2/slots", "FastAPI @router.post('/') under a prefix"},

		// Separator hygiene: trailing, duplicated and interior empties collapse.
		{"/a/", "/b", "/a/b", ""},
		{"/a/", "/b/", "/a/b", ""},
		{"/a//b", "//c", "/a/b/c", ""},
		{"  /a  ", "  /b  ", "/a/b", "surrounding whitespace"},

		// The behavioural change.
		{"", "results", "/results", "was unrooted under joinAxumPath/joinPyPath"},
		{"", "", "/", "was \"\" under joinAxumPath/joinPyPath"},

		// Path parameters are opaque segments; normalisation is the linker's job.
		{"/users", "/:id", "/users/:id", ""},
		{"/users", "/{id}/posts", "/users/{id}/posts", ""},
		{"/files", "/*", "/files/*", ""},
	} {
		if got := JoinRoutePath(tc.base, tc.sub); got != tc.want {
			t.Errorf("JoinRoutePath(%q, %q) = %q, want %q  %s", tc.base, tc.sub, got, tc.want, tc.note)
		}
	}
}

// TestJoinRoutePath_AlwaysRooted is the invariant the cross-repo linker depends on:
// whatever goes in, the result is a "/"-rooted path, because indexServerRoutes keys
// on rooted suffixes. Asserted as a property so a future edit cannot quietly
// reintroduce the unrooted branch this consolidation removed.
func TestJoinRoutePath_AlwaysRooted(t *testing.T) {
	for _, base := range []string{"", "/", "a", "/a", "/a/", "//"} {
		for _, sub := range []string{"", "/", "b", "/b", "/b/", "//"} {
			got := JoinRoutePath(base, sub)
			if got == "" || got[0] != '/' {
				t.Errorf("JoinRoutePath(%q, %q) = %q; must be \"/\"-rooted", base, sub, got)
			}
			if strings.Contains(got, "//") {
				t.Errorf("JoinRoutePath(%q, %q) = %q; must not contain an empty segment", base, sub, got)
			}
		}
	}
}
