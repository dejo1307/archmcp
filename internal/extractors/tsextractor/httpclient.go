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
// string or template literal. Group 1 is the verb name; the URL literal is
// captured into one of groups 2-4 (double-quote, single-quote, or backtick — RE2
// has no backreferences). e.g. this.makeRequest<T>('/api/settings/feedback', { method: 'POST' })
//
//	fetch(`${API_BASE_URL}/api/user/current`, { method: 'GET' })
//
// The leading `(?:^|[^\w])` is a left word-boundary: it keeps member forms like
// `window.fetch(` / `this.makeRequest(` (preceded by `.`) while rejecting calls
// whose name merely ENDS in "fetch" — `router.prefetch(...)`, `query.refetch(...)`
// — which are navigation/cache primitives, not outbound HTTP.
// The optional type-argument group is bounded on "(" rather than ">", because a
// TypeScript type argument is routinely NESTED — fetch<ApiResponse<Foo>>(…) — and
// "<[^>]*>" stops at the inner ">", leaving the following "\s*\(" to meet a ">"
// and fail. RE2 has no recursion, so the empty alternative is no rescue: it then
// meets "<" and fails too, and the call is silently not a call. A type argument
// never contains "(", so "[^()]*" runs greedily to the last ">" before the call
// parenthesis and spans any nesting depth. Same reasoning at verbNamedCall and
// lowerVerbCall — all three shared the defect.
var httpClientCall = regexp.MustCompile("(?:^|[^\\w])(fetch|makeRequest)\\s*(?:<[^()]*>)?\\s*\\(\\s*(?:\"([^\"]*)\"|'([^']*)'|`([^`]*)`)")

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
// Only uppercase verbs are matched here: that is the generated-client convention,
// and it avoids colliding with ordinary lowercase methods like map.get()/
// cache.delete(). The lowercase idiom is handled separately by lowerVerbCall,
// which pays for admitting it with a stricter argument rule.
var verbNamedCall = regexp.MustCompile("\\.(GET|POST|PUT|DELETE|PATCH)\\s*(?:<[^()]*>)?\\s*\\(\\s*(?:\"([^\"]*)\"|'([^']*)'|`([^`]*)`)")

// lowerVerbCall matches the hand-written client idiom that verbNamedCall's
// uppercase-only rule deliberately excludes — axios.get('/path'), http.post('/path'),
// apiClient.put('/path') — which is the dominant shape in TypeScript codebases and
// contributed no route fact at all until now.
//
// The collision that motivated the uppercase-only rule (map.get("key"),
// cache.delete(id), searchParams.get("q"), headers.get("content-type")) is answered
// here by requiring the first argument to be a "/"-ROOTED literal. That is not a new
// heuristic: cleanTSPath already rejects every non-"/"-rooted path downstream, so
// admitting one here that it would drop anyway is the only case this widening adds.
// A collection key beginning with "/" is vanishingly rare; a request path not
// beginning with one is not a request path.
//
// Deliberately NOT matched: a lowercase call whose argument is a template starting
// with an interpolation (axios.get(`${base}/x`)). Recovering those needs the base
// resolution of cleanTSPath, and admitting them here would re-open the collision
// this rule closes. They stay missed — see GAP-TS-06 for the base-URL half.
var lowerVerbCall = regexp.MustCompile("\\.(get|post|put|delete|patch)\\s*(?:<[^()]*>)?\\s*\\(\\s*(?:\"(/[^\"]*)\"|'(/[^']*)'|`(/[^`]*)`)")

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

// requestPayloadKey marks an object literal as an HTTP request descriptor by a
// request-payload sibling key (not a verb). Requiring one of these — or a
// verb-valued `type:`/`method:`, checked separately — next to a `url:` keeps
// router links, config objects, and SEO metadata (a Next.js `openGraph: { url,
// type: 'website', siteName, … }` block, JSON-LD) from being mistaken for
// outbound calls: those carry a `type:` whose value is not an HTTP verb and none
// of these payload keys.
var requestPayloadKey = regexp.MustCompile(`\b(?:token|payload|pagination|signal|headers|body|query|params)\s*:`)

// tsInterpolation matches a template-literal interpolation, e.g. ${id}.
var tsInterpolation = regexp.MustCompile(`\$\{[^}]*\}`)

// baseLiteralDecl binds an identifier to a "/"-rooted string literal: a const/let/
// var, a class field (with optional modifiers preceding the name), or a constructor
// default parameter. Group 1 is the identifier; groups 2-4 are the literal
// (single/double/backtick — the backtick form excludes "{" so a template base that
// carries its own ${…} is not treated as a static base). Only "/"-rooted values are
// matched, so an absolute (http…) or env-derived base is deliberately not captured.
var baseLiteralDecl = regexp.MustCompile("(\\w+)\\s*(?::[\\w<>\\[\\].,| ]*)?=\\s*(?:'(/[^']*)'|\"(/[^\"]*)\"|`(/[^`{]*)`)")

// fileBaseLiterals maps an identifier to the "/"-rooted base-path literal it is
// bound to in this file (e.g. basePath -> "/api/settings/pricing"), so a client call
// written as `${this.basePath}/calculate` can be reconstructed to its full path
// instead of collapsing to the single-segment suffix "/calculate". An identifier
// bound to two different literals in the same file is ambiguous and dropped, so the
// resolver never guesses.
func fileBaseLiterals(src []byte) map[string]string {
	out := map[string]string{}
	ambiguous := map[string]bool{}
	for _, m := range baseLiteralDecl.FindAllSubmatchIndex(src, -1) {
		id := string(src[m[2]:m[3]])
		lit := firstNonEmptyGroup(src, m, 2, 3, 4)
		if lit == "" || ambiguous[id] {
			continue
		}
		if prev, ok := out[id]; ok && prev != lit {
			delete(out, id) // conflicting bindings -> unresolvable
			ambiguous[id] = true
			continue
		}
		out[id] = lit
	}
	return out
}

// optionsObjectAfter returns the options-object literal that is the call's second
// argument — the slice from its "{" to the matching "}" — or nil when the call has
// no options, or passes them as a variable rather than a literal.
//
// The method must be read from THIS call's options and nothing else. Scanning a
// flat byte window forward instead lets a later call's `method:` bleed backwards,
// so a plain `fetch("/a/b")` sitting above a POST reports POST — a wrong verb on a
// real path, which then mis-resolves (or fails to resolve) in the cross-repo
// linker. Pass 3 already scopes its scan with enclosingObject for the same reason.
func optionsObjectAfter(src []byte, pos int) []byte {
	i := pos
	skipSpace := func() {
		for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r') {
			i++
		}
	}
	skipSpace()
	if i >= len(src) || src[i] != ',' {
		return nil // single-argument call -> no options
	}
	i++
	skipSpace()
	if i >= len(src) || src[i] != '{' {
		return nil // options passed as a variable/expression -> no literal to read
	}
	open := i
	depth := 0
	for ; i < len(src) && i-open < objectScanCap; i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[open : i+1]
			}
		}
	}
	return nil
}

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
	bases := fileBaseLiterals(src)

	var out []facts.Fact
	seen := map[string]bool{}
	// add appends a client-route fact, de-duplicating on method+path+line so the
	// three passes below cannot double-emit the same call site.
	add := func(rawPath, method, framework string, off int) {
		path, ok := cleanTSPath(rawPath, bases)
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
				facts.PropRole:   facts.RoleClient,
				"method":         method,
				"framework":      framework,
				"language":       "typescript",
				facts.PropSource: facts.RouteSourceTSHTTPClient,
				"api":            api,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}

	// Pass 1 — positional fetch()/makeRequest(), method from a nearby `method:`.
	// Group 1 is the verb, so the URL literal is in groups 2-4 and the reported
	// offset is the verb start (m[2]) — not m[0], which now includes the leading
	// word-boundary char and would mis-count the line when that char is a newline.
	for _, m := range httpClientCall.FindAllSubmatchIndex(src, -1) {
		raw := firstNonEmptyGroup(src, m, 2, 3, 4)
		method := "GET"
		if opts := optionsObjectAfter(src, m[1]); opts != nil {
			if mm := httpClientMethod.FindSubmatch(opts); mm != nil {
				method = strings.ToUpper(string(mm[1]))
			}
		}
		add(raw, method, "fetch", m[2])
	}

	// Pass 2 — verb-named generated-client calls; the method is the call name.
	for _, m := range verbNamedCall.FindAllSubmatchIndex(src, -1) {
		method := strings.ToUpper(string(src[m[2]:m[3]]))
		raw := firstNonEmptyGroup(src, m, 2, 3, 4)
		add(raw, method, "openapi-fetch", m[0])
	}

	// Pass 2b — lowercase verb-named calls (axios.get('/x'), http.post('/x')). The
	// method is the call name, as in pass 2; the "/"-rooted argument requirement
	// lives in the pattern (see lowerVerbCall) rather than here, so a collection
	// lookup never reaches add() in the first place.
	//
	// A receiver bound to an app or router in this file is a route REGISTRATION, not
	// an outbound call — `router.get('/x')` and `axios.get('/x')` are the same text.
	// extractServerRouteFacts owns those, so skip them here or the call site would be
	// emitted twice, once in each direction. Only known server receivers are skipped:
	// an unknown one stays a client call, exactly as before this pass existed.
	serverRecv := serverBindings(src)
	for _, m := range lowerVerbCall.FindAllSubmatchIndex(src, -1) {
		if isServerReceiver(serverRecv, identifierEndingAt(src, m[0])) {
			continue
		}
		method := strings.ToUpper(string(src[m[2]:m[3]]))
		raw := firstNonEmptyGroup(src, m, 2, 3, 4)
		add(raw, method, "axios", m[0])
	}

	// Pass 3 — options-object clients: a `url:` property inside an object literal
	// that also carries a request-descriptor key, with the verb from a sibling
	// `type:`/`method:` (default GET). The scan is scoped to the enclosing object so
	// a neighbouring object's verb cannot bleed in.
	for _, m := range urlProperty.FindAllSubmatchIndex(src, -1) {
		window := enclosingObject(src, m[0], m[1])
		if window == nil {
			continue // no enclosing object literal
		}
		// The object is an outbound request only if it carries a real HTTP verb
		// (type:/method: whose value maps to a verb) or a request-payload key. An
		// object whose only descriptor signal is a non-verb type: — SEO openGraph
		// { url, type: 'website' }, JSON-LD — is metadata, not a call.
		method := "GET"
		haveVerb := false
		if vm := requestVerbProperty.FindSubmatch(window); vm != nil {
			if v := mapClientVerb(string(vm[1])); v != "" {
				method = v
				haveVerb = true
			}
		}
		if !haveVerb && !requestPayloadKey.Match(window) {
			continue // a plain link / config / SEO-metadata object
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
// A group index beyond the match layout is skipped rather than read: the
// layout's length depends on the regex, and a caller drifting out of sync with
// its pattern must degrade to a non-match, not a panic mid-extraction.
func firstNonEmptyGroup(src []byte, m []int, groups ...int) string {
	for _, g := range groups {
		if 2*g+1 >= len(m) {
			continue
		}
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
func cleanTSPath(raw string, bases map[string]string) (string, bool) {
	p := strings.TrimSpace(raw)
	// A leading ${...} token is the base URL. Prefer to RESOLVE it against a
	// file-local "/"-rooted literal (e.g. ${this.basePath} -> "/api/settings/pricing")
	// so the full path is reconstructed and can match its server route; fall back to
	// stripping it when the base is not a known literal (an injected/env/absolute
	// base we cannot know statically).
	if strings.HasPrefix(p, "${") {
		if i := strings.IndexByte(p, '}'); i >= 0 {
			token := p[2:i] // inside ${...}
			rest := p[i+1:]
			if dot := strings.LastIndexByte(token, '.'); dot >= 0 {
				token = token[dot+1:] // this.basePath -> basePath
			}
			if base, ok := bases[strings.TrimSpace(token)]; ok {
				p = base + rest
			} else {
				p = rest
			}
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
	// Strip a query-string placeholder fused to the final segment. A `${queryParams}`
	// / `${queryString}` appended to a path collapses (above) to a `{}` glued to the
	// segment tail, e.g. ".../role-distribution{}" or "/overview{}" — the real `?`
	// lives inside the variable so the query strip never saw it. A genuine path
	// param is always its own "/{}" segment, never fused to text, so a trailing
	// "<text>{}" is a query string: drop it. "/files/{}" and "/items/{}.json" are
	// untouched (own-segment param / non-{}-suffixed tail).
	if strings.HasSuffix(p, "{}") && !strings.HasSuffix(p, "/{}") {
		p = strings.TrimSuffix(p, "{}")
	}
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return "", false
	}
	// A backend path is rooted at "/". Requiring a leading slash drops non-path
	// string literals that reach here — a lone ",", a fragment of an analysis
	// script's own source (fitness-functions.js scanning for "fetch(") — which
	// otherwise pass the concrete-segment check below and become phantom routes.
	if !strings.HasPrefix(p, "/") {
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
