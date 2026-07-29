package facts

import "strings"

// JoinRoutePath composes a route prefix with a leaf path, the way every server-side
// route DSL does: a controller/router base plus the path declared on the handler.
//
// This is the single definition. Four extractors carried their own copy — Axum
// (`joinAxumPath`), FastAPI (`joinPyPath`), Spring (`joinRoutePath`) and Symfony
// (`joinSymfonyPath`) — and they had drifted into two different contracts: Spring and
// Symfony guaranteed a leading slash, Axum and FastAPI returned the leaf unchanged
// when the prefix was empty, so an unrooted leaf stayed unrooted. The cross-repo
// linker matches on "/"-rooted path suffixes, which makes the guarantee load-bearing:
// the same route emitted through two of these helpers would match differently. Same
// reason IsTestPath exists once (see testpath.go) rather than once per consumer.
//
// The contract is the strictest of the four:
//
//   - the result always begins with "/"
//   - empty + empty is "/", not ""
//   - empty, duplicated and trailing separators collapse ("/a/" + "/b/" -> "/a/b")
//   - a path parameter is opaque: ":id", "{id}" and "*" are ordinary segments and
//     are never rewritten here. Placeholder normalisation belongs to the linker
//     (crossrepo.normalizePath), which owns it for every language at match time.
//
// Deliberately NOT folded in, though both are named "join…Path":
//
//   - swiftextractor.joinURLPath composes a CLIENT base URL with a request path and
//     strips leading slashes on purpose (`strings.Trim(a, "/")`). Giving it this
//     contract would rename every Swift client route, changing what the linker
//     matches. It is a separate change with its own before/after.
//   - rustextractor.joinRustPath resolves MODULE directories against a known-dirs
//     set. It is not a URL join at all; only the name rhymes.
func JoinRoutePath(base, sub string) string {
	segs := make([]string, 0, 8)
	segs = appendPathSegments(segs, base)
	segs = appendPathSegments(segs, sub)
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/")
}

// appendPathSegments splits p on "/" and appends its non-empty segments, so empty,
// duplicated and trailing separators all collapse.
func appendPathSegments(dst []string, p string) []string {
	for _, s := range strings.Split(strings.TrimSpace(p), "/") {
		if s != "" {
			dst = append(dst, s)
		}
	}
	return dst
}
