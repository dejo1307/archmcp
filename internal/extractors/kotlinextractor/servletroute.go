package kotlinextractor

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// servletRegistration matches a programmatic route registration in a JVM fluent
// builder DSL, capturing the path string literal and the handler expression:
//
//	.addServlet("/v1/things", thingsServlet)
//	.addServletWithMapping("/v1/x", XServlet::class.java)
//
// Unlike Spring/JAX-RS annotations, these register routes at runtime through a
// builder call, so the annotation-based route extractors never see them — the path
// lives in an ordinary string-literal argument. Embedded servlet containers wire
// their endpoints this way. One registration per line is the universal
// shape (a fluent chain puts each .addServlet on its own line), so a line scan is
// sufficient and avoids a full parse.
var servletRegistration = regexp.MustCompile(`\.addServlet(?:WithMapping)?\s*\(\s*"([^"]+)"\s*,\s*([^,)]+)`)

// extractServletRouteFacts emits a server-route fact for every programmatic servlet
// registration in a Kotlin source file. role=server and method=facts.MethodAny (a
// raw servlet serves every verb), so the cross-repo linker matches an inbound client
// call of any method — from any repo — against the route that serves it. Path params
// are left as written; the linker's normalizePath collapses {x}/:x/<x>/* forms.
func extractServletRouteFacts(src []byte, relFile string) []facts.Fact {
	var out []facts.Fact
	dir := filepath.ToSlash(filepath.Dir(relFile))

	for i, line := range strings.Split(string(src), "\n") {
		m := servletRegistration.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		path := cleanServletPath(m[1])
		if path == "" {
			continue
		}
		out = append(out, facts.Fact{
			Kind: facts.KindRoute,
			Name: path,
			File: relFile,
			Line: i + 1,
			Props: map[string]any{
				"role":      "server",
				"method":    facts.MethodAny,
				"framework": "servlet",
				"language":  "kotlin",
				"handler":   strings.TrimSpace(m[2]),
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}
	return out
}

// cleanServletPath trims a servlet mapping path to the form the cross-repo linker
// matches on: drop a trailing servlet path-spec wildcard ("/v1/things/*" ->
// "/v1/things") and any query string, keep the rest as written.
func cleanServletPath(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimSuffix(p, "/*")
	p = strings.TrimSuffix(p, "*")
	return strings.TrimRight(strings.TrimSpace(p), "/")
}
