package tsextractor

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// httpClientCall matches a fetch()/makeRequest() call whose first argument is a
// string or template literal, capturing the URL literal into one of three
// groups (double-quote, single-quote, or backtick — RE2 has no backreferences).
// e.g. this.makeRequest<T>('/api/settings/feedback', { method: 'POST' })
//
//	fetch(`${API_BASE_URL}/api/user/current`, { method: 'GET' })
var httpClientCall = regexp.MustCompile("(?:fetch|makeRequest)\\s*(?:<[^>]*>)?\\s*\\(\\s*(?:\"([^\"]*)\"|'([^']*)'|`([^`]*)`)")

// httpClientMethod matches a `method: 'POST'` option within a call's options
// object.
var httpClientMethod = regexp.MustCompile(`method\s*:\s*['"]([A-Za-z]+)['"]`)

// verbNamedCall matches an openapi-fetch–style verb-named method call whose first
// positional argument is a string/template literal path, where the HTTP method is
// the (uppercase) method name itself, e.g.
//
//	API.getApi().GET('/api/v3/items/{id}', { params: … })
//	ApiV3.getApi().DELETE('/api/v3/widgets/{id}/follow')
//
// Only uppercase verbs are matched: that is the generated-client convention and it
// avoids colliding with ordinary lowercase methods like map.get()/cache.delete().
var verbNamedCall = regexp.MustCompile("\\.(GET|POST|PUT|DELETE|PATCH)\\s*(?:<[^>]*>)?\\s*\\(\\s*(?:\"([^\"]*)\"|'([^']*)'|`([^`]*)`)")

// urlProperty matches a `url:` object property whose value is a string/template
// literal — the options-object client idiom, e.g.
//
//	request({ token, type: 'query', url: `/v2/messages/${id}.json` })
//	{ type: 'post', payload: {…}, url: '/v2/messages.json' }
var urlProperty = regexp.MustCompile("\\burl\\s*:\\s*(?:\"([^\"]*)\"|'([^']*)'|`([^`]*)`)")

// requestVerbProperty extracts the verb of a request-options object from its
// `type:`/`method:` property. The value may be an HTTP verb or an action verb
// (query/post/put/delete); mapClientVerb reconciles both.
var requestVerbProperty = regexp.MustCompile(`\b(?:type|method)\s*:\s*['"]([A-Za-z]+)['"]`)

// requestDescriptorKey marks an object literal as an HTTP request descriptor
// (rather than a plain link/href object): it carries a verb or a request-payload
// sibling key. Requiring one of these next to a `url:` keeps router links and
// config objects from being mistaken for outbound calls.
var requestDescriptorKey = regexp.MustCompile(`\b(?:type|method|token|payload|pagination|signal|headers|body|query|params)\s*:`)

// tsInterpolation matches a template-literal interpolation, e.g. ${id}.
var tsInterpolation = regexp.MustCompile(`\$\{[^}]*\}`)

// httpMethodWindow is how many bytes after the URL literal to scan for the
// request's method option.
const httpMethodWindow = 200

// objectScanCap bounds how far on each side of a `url:` property to scan for the
// braces of its enclosing object literal, so a pathological input cannot make the
// scan run over a whole file.
const objectScanCap = 4096

// extractHTTPClientFacts emits a client-route fact for every hand-written HTTP
// call to the backend, recognizing three shapes: (1) positional fetch()/
// makeRequest() calls, (2) verb-named generated-client calls (.GET(/.POST(/…), and
// (3) options-object clients carrying a `url:` property with a `type:`/`method:`
// verb. Paths are kept as written (with the /api or /v2 prefix and any .json
// suffix); the cross-repo linker's normalization reconciles prefixes and format
// suffixes.
func extractHTTPClientFacts(src []byte, relFile string) []facts.Fact {
	dir := filepath.ToSlash(filepath.Dir(relFile))
	api := tsAPIHint(relFile)

	var out []facts.Fact
	seen := map[string]bool{}
	// add appends a client-route fact, de-duplicating on method+path+line so the
	// three passes below cannot double-emit the same call site.
	add := func(rawPath, method, framework string, off int) {
		path, ok := cleanTSPath(rawPath)
		if !ok {
			return
		}
		line := 1 + bytes.Count(src[:off], []byte("\n"))
		key := method + "\x00" + path + "\x00" + strconv.Itoa(line)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, facts.Fact{
			Kind: facts.KindRoute,
			Name: path,
			File: relFile,
			Line: line,
			Props: map[string]any{
				"role":      "client",
				"method":    method,
				"framework": framework,
				"language":  "typescript",
				"source":    "ts-http-client",
				"api":       api,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}

	// Pass 1 — positional fetch()/makeRequest(), method from a nearby `method:`.
	for _, m := range httpClientCall.FindAllSubmatchIndex(src, -1) {
		raw := firstNonEmptyGroup(src, m, 1, 2, 3)
		method := "GET"
		end := m[1] + httpMethodWindow
		if end > len(src) {
			end = len(src)
		}
		if mm := httpClientMethod.FindSubmatch(src[m[1]:end]); mm != nil {
			method = strings.ToUpper(string(mm[1]))
		}
		add(raw, method, "fetch", m[0])
	}

	// Pass 2 — verb-named generated-client calls; the method is the call name.
	for _, m := range verbNamedCall.FindAllSubmatchIndex(src, -1) {
		method := strings.ToUpper(string(src[m[2]:m[3]]))
		raw := firstNonEmptyGroup(src, m, 2, 3, 4)
		add(raw, method, "openapi-fetch", m[0])
	}

	// Pass 3 — options-object clients: a `url:` property inside an object literal
	// that also carries a request-descriptor key, with the verb from a sibling
	// `type:`/`method:` (default GET). The scan is scoped to the enclosing object so
	// a neighbouring object's verb cannot bleed in.
	for _, m := range urlProperty.FindAllSubmatchIndex(src, -1) {
		window := enclosingObject(src, m[0], m[1])
		if window == nil || !requestDescriptorKey.Match(window) {
			continue // no enclosing object, or a plain link/config object
		}
		method := "GET"
		if vm := requestVerbProperty.FindSubmatch(window); vm != nil {
			if v := mapClientVerb(string(vm[1])); v != "" {
				method = v
			}
		}
		raw := firstNonEmptyGroup(src, m, 1, 2, 3)
		add(raw, method, "request-options", m[0])
	}

	return out
}

// enclosingObject returns the bytes of the object literal that immediately
// encloses the [s,e) range (a `url:` property), by brace-matching outward from
// either side while skipping the [s,e) value itself. Returns nil when the braces
// are not found within objectScanCap bytes on a side. Nested braces (a `{ zip }`
// sibling value, a `${…}` interpolation) are balanced by depth counting.
func enclosingObject(src []byte, s, e int) []byte {
	open := -1
	depth := 0
left:
	for i := s - 1; i >= 0 && s-1-i < objectScanCap; i-- {
		switch src[i] {
		case '}':
			depth++
		case '{':
			if depth == 0 {
				open = i
				break left
			}
			depth--
		}
	}
	if open < 0 {
		return nil
	}
	shut := -1
	depth = 0
right:
	for i := e; i < len(src) && i-e < objectScanCap; i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				shut = i
				break right
			}
			depth--
		}
	}
	if shut < 0 {
		return nil
	}
	return src[open : shut+1]
}

// mapClientVerb maps a request-descriptor verb token to an HTTP method. Standard
// HTTP verbs pass through; the action-style token "query" maps to GET (a read).
// Returns "" for an unrecognized token so the caller can fall back to GET.
func mapClientVerb(tok string) string {
	switch strings.ToUpper(strings.TrimSpace(tok)) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return strings.ToUpper(strings.TrimSpace(tok))
	case "QUERY":
		return "GET"
	}
	return ""
}

// firstNonEmptyGroup returns the text of the first matched capture group among
// the given group indices (FindAllSubmatchIndex layout).
func firstNonEmptyGroup(src []byte, m []int, groups ...int) string {
	for _, g := range groups {
		s, e := m[2*g], m[2*g+1]
		if s >= 0 && e > s {
			return string(src[s:e])
		}
	}
	return ""
}

// cleanTSPath turns a fetch/makeRequest URL literal into a matchable route path,
// or returns ok=false when it is not a backend path (fully dynamic, external,
// or empty). It strips a leading ${...} base-URL token, drops the query string,
// and collapses interpolations to the {} placeholder.
func cleanTSPath(raw string) (string, bool) {
	p := strings.TrimSpace(raw)
	// Strip a leading ${...} base-URL token (e.g. ${API_BASE_URL}).
	if strings.HasPrefix(p, "${") {
		if i := strings.IndexByte(p, '}'); i >= 0 {
			p = p[i+1:]
		}
	}
	// A remaining absolute URL points at a third-party API, not our backend.
	if strings.HasPrefix(p, "http") {
		return "", false
	}
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	p = tsInterpolation.ReplaceAllString(p, "{}")
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return "", false
	}
	// Require at least one concrete (non-placeholder) segment so a fully dynamic
	// URL (e.g. just "${endpoint}") is skipped.
	for _, seg := range strings.Split(p, "/") {
		if seg != "" && seg != "{}" {
			return p, true
		}
	}
	return "", false
}

// tsAPIHint returns the source file's base name without extension (e.g.
// "feedback"), used as the cross-repo linker's disambiguation hint.
func tsAPIHint(relFile string) string {
	base := filepath.Base(relFile)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
