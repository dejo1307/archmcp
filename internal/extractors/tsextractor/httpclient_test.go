package tsextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// byNameMethod indexes emitted client routes by path -> method.
func byNameMethod(ff []facts.Fact) map[string]string {
	out := map[string]string{}
	for _, f := range ff {
		out[f.Name] = f.Props["method"].(string)
	}
	return out
}

// TestExtractHTTPClientFacts_VerbNamedCalls covers the openapi-fetch generated
// client where the method is the (uppercase) call name and the path is the first
// positional literal.
func TestExtractHTTPClientFacts_VerbNamedCalls(t *testing.T) {
	src := "async function load(id: number) {\n" +
		"  await API.getApi().GET('/api/v3/items/{id}', { params });\n" +
		"  API.getApi().POST('/api/v3/widgets/{id}/follow', { body });\n" +
		"  API.getApi().DELETE('/api/v3/sessions');\n" +
		"  const v = map.get('some-key');\n" + // lowercase .get must NOT match
		"}\n"
	got := byNameMethod(extractHTTPClientFacts([]byte(src), "client/feed/network.ts"))

	// Literal {id} braces are left as written (the linker's normalizePath collapses
	// them to {} at match time), like the Retrofit/Swift extractors.
	if got["/api/v3/items/{id}"] != "GET" {
		t.Errorf("GET call: got %+v", got)
	}
	if got["/api/v3/widgets/{id}/follow"] != "POST" {
		t.Errorf("POST call: got %+v", got)
	}
	if got["/api/v3/sessions"] != "DELETE" {
		t.Errorf("DELETE call: got %+v", got)
	}
	if _, found := got["some-key"]; found {
		t.Errorf("lowercase map.get('some-key') must not be detected: %+v", got)
	}
}

// TestExtractHTTPClientFacts_OptionsObject covers the options-object client where
// the URL is a `url:` property and the verb is a `type:` property (with the
// action verb "query" mapping to GET), including the declarative request object.
func TestExtractHTTPClientFacts_OptionsObject(t *testing.T) {
	src := "const a = createRequest({ url: '/v2/items.json', type: 'query', token });\n" +
		"const b = createRequest({ url: `/v2/items/${id}.json`, type: 'delete', token });\n" +
		"const post = { type: 'post', payload: {}, url: '/v2/messages.json' };\n" +
		"const c = request({ query: { zip }, url: '/v2/lookup/address.json' });\n" + // no verb -> GET
		"const link = { url: '/settings/profile', label: 'Profile' };\n" // plain link -> not a request
	got := byNameMethod(extractHTTPClientFacts([]byte(src), "client/items/network.jsx"))

	if got["/v2/items.json"] != "GET" {
		t.Errorf("query verb should map to GET: %+v", got)
	}
	if got["/v2/items/{}.json"] != "DELETE" {
		t.Errorf("delete verb + interpolation: %+v", got)
	}
	if got["/v2/messages.json"] != "POST" {
		t.Errorf("declarative request object post: %+v", got)
	}
	if got["/v2/lookup/address.json"] != "GET" {
		t.Errorf("verb-less request should default GET: %+v", got)
	}
	if _, found := got["/settings/profile"]; found {
		t.Errorf("a plain link object (no request-descriptor key) must not be detected: %+v", got)
	}
}

func TestExtractHTTPClientFacts(t *testing.T) {
	src := "class FeedbackApi {\n" +
		"  async createFeedback(data: any) {\n" +
		"    return this.makeRequest<any>('/api/settings/feedback', { method: 'POST' });\n" +
		"  }\n" +
		"  async respond(id: number) {\n" +
		"    return this.makeRequest<any>(`/api/settings/feedback/${id}/respond`, {\n" +
		"      method: 'PUT',\n" +
		"    });\n" +
		"  }\n" +
		"  async stats() {\n" +
		"    return this.makeRequest<any>('/api/settings/feedback/statistics');\n" + // default GET
		"  }\n" +
		"  async external() {\n" +
		"    return fetch('https://third.party/v1/thing', { method: 'GET' });\n" + // skipped
		"  }\n" +
		"}\n"

	ff := extractHTTPClientFacts([]byte(src), "src/lib/api/feedback.ts")

	byName := map[string]string{} // name -> method
	for _, f := range ff {
		if f.Props["role"] != "client" || f.Props["framework"] != "fetch" {
			t.Errorf("%s wrong props: %+v", f.Name, f.Props)
		}
		if f.Props["api"] != "feedback" {
			t.Errorf("%s api hint = %v, want feedback", f.Name, f.Props["api"])
		}
		byName[f.Name] = f.Props["method"].(string)
	}

	if len(byName) != 3 {
		t.Fatalf("expected 3 backend client routes (external skipped), got %d: %+v", len(byName), byName)
	}
	if byName["/api/settings/feedback"] != "POST" {
		t.Errorf("createFeedback: want POST, got %+v", byName)
	}
	if byName["/api/settings/feedback/{}/respond"] != "PUT" {
		t.Errorf("respond: want PUT with {} param, got %+v", byName)
	}
	if byName["/api/settings/feedback/statistics"] != "GET" {
		t.Errorf("stats: want default GET, got %+v", byName)
	}
	if _, found := byName["https://third.party/v1/thing"]; found {
		t.Errorf("external URL should have been skipped: %+v", byName)
	}
}

// TestExtractHTTPClientFacts_PrefetchNotMatched covers the left word-boundary on
// the fetch()/makeRequest() matcher: a call whose name merely ends in "fetch"
// (router.prefetch / query.refetch — navigation and cache primitives) must NOT be
// captured, while a genuine `window.fetch(` / member `this.makeRequest(` still is.
func TestExtractHTTPClientFacts_PrefetchNotMatched(t *testing.T) {
	src := "function onFocus() {\n" +
		"  router.prefetch('/dashboard/premium');\n" + // navigation, not HTTP
		"  router.prefetch('/dashboard/maintenance');\n" +
		"  query.refetch('/api/should-not-appear');\n" + // react-query cache, not HTTP
		"  window.fetch('/api/real/thing');\n" + // genuine fetch -> kept
		"}\n"
	got := byNameMethod(extractHTTPClientFacts([]byte(src), "src/app/login/page.tsx"))

	for _, junk := range []string{"/dashboard/premium", "/dashboard/maintenance", "/api/should-not-appear"} {
		if _, found := got[junk]; found {
			t.Errorf("prefetch/refetch target %q must not be detected: %+v", junk, got)
		}
	}
	if got["/api/real/thing"] != "GET" {
		t.Errorf("window.fetch should still be detected: %+v", got)
	}
}

// TestExtractHTTPClientFacts_SEOMetadataNotRequest covers Pass 3's tightened
// request-descriptor test: a Next.js `openGraph` block carries a `url:` and a
// non-verb `type: 'website'` but no request-payload key, so it is metadata, not an
// outbound call. A sibling request object with a real verb still resolves.
func TestExtractHTTPClientFacts_SEOMetadataNotRequest(t *testing.T) {
	src := "export const metadata = {\n" +
		"  openGraph: {\n" +
		"    type: 'website',\n" +
		"    url: `${baseUrl}/journal`,\n" +
		"    siteName: 'FairwayHub',\n" +
		"    locale: 'en_US',\n" +
		"  },\n" +
		"};\n" +
		"const call = request({ url: '/api/settings/app', type: 'post', payload: {} });\n"
	got := byNameMethod(extractHTTPClientFacts([]byte(src), "src/app/journal/layout.tsx"))

	if _, found := got["/journal"]; found {
		t.Errorf("openGraph SEO url (type:'website') must not be detected as a call: %+v", got)
	}
	if got["/api/settings/app"] != "POST" {
		t.Errorf("a real request object with a verb should still resolve: %+v", got)
	}
}

// TestExtractHTTPClientFacts_NonPathLiteralSkipped covers cleanTSPath's leading-"/"
// requirement: a non-path string literal reaching the matcher (e.g. an analysis
// script's own source scanning for `fetch(`) must not become a phantom route.
func TestExtractHTTPClientFacts_NonPathLiteralSkipped(t *testing.T) {
	src := "const markers = [ 'fetch(', ',' ];\n" + // fetch(',' -> URL literal is ","
		"if (line.includes('fetch(')) { hasAPICall = true; }\n"
	got := byNameMethod(extractHTTPClientFacts([]byte(src), "fitness-functions.js"))

	for name := range got {
		if len(name) == 0 || name[0] != '/' {
			t.Errorf("non-path literal %q must be skipped, got routes: %+v", name, got)
		}
	}
}

// TestExtractHTTPClientFacts_GluedQueryPlaceholderStripped covers cleanTSPath's
// query-string-placeholder strip: a `${queryParams}` fused to the final segment
// collapses to a trailing "{}" glued to text, which must be dropped so the path
// matches its server route — while an own-segment "/{}" path param is preserved.
func TestExtractHTTPClientFacts_GluedQueryPlaceholderStripped(t *testing.T) {
	src := "class A {\n" +
		"  a() { return this.makeRequest(`/api/settings/analytics/role-distribution${queryParams}`); }\n" +
		"  b() { return this.makeRequest(`${this.baseUrl}/api/settings/vat-rates/active${qs}`); }\n" +
		"  c() { return this.makeRequest(`/api/settings/files/${id}`); }\n" + // real path param -> keep {}
		"  d() { return this.makeRequest(`/api/settings/files/${id}/versions`); }\n" + // {} mid-path -> keep
		"}\n"
	got := byNameMethod(extractHTTPClientFacts([]byte(src), "src/lib/api/analyticsApi.ts"))

	if _, found := got["/api/settings/analytics/role-distribution"]; !found {
		t.Errorf("glued query placeholder should be stripped to a clean path: %+v", got)
	}
	if _, found := got["/api/settings/vat-rates/active"]; !found {
		t.Errorf("glued query placeholder after a base-URL token should be stripped: %+v", got)
	}
	if _, found := got["/api/settings/files/{}"]; !found {
		t.Errorf("own-segment /{} path param must be preserved: %+v", got)
	}
	if _, found := got["/api/settings/files/{}/versions"]; !found {
		t.Errorf("mid-path {} param must be preserved: %+v", got)
	}
}

// TestExtractHTTPClientFacts_BaseLiteralResolved covers file-local base-URL
// resolution: a leading `${...}` base interpolated at the head of a call path is
// rebuilt from a "/"-rooted literal declared in the same file — as a class field,
// a file-scope const, or a constructor default param — so the full path (which
// matches the server route) is emitted instead of a bare single-segment suffix.
func TestExtractHTTPClientFacts_BaseLiteralResolved(t *testing.T) {
	// GET calls are placed last so the method-scan window (200 bytes after the URL)
	// cannot pick up a later call's `method:` option and mis-label them.
	src := "const ROOT = '/api/v2/things';\n" +
		"class PricingApi {\n" +
		"  private readonly basePath = '/api/settings/pricing';\n" +
		"  constructor(private baseUrl: string = '/api/settings/engagement') {}\n" +
		"  calc() { return this.makeRequest(`${this.basePath}/calculate`, { method: 'POST' }); }\n" +
		"  send() { return this.makeRequest(`${this.baseUrl}/send`, { method: 'POST' }); }\n" +
		"  list() { return fetch(`${ROOT}/list`); }\n" +
		"}\n"
	got := byNameMethod(extractHTTPClientFacts([]byte(src), "src/lib/api/pricingEngine.ts"))

	if got["/api/settings/pricing/calculate"] != "POST" {
		t.Errorf("class-field base should resolve to full path: %+v", got)
	}
	if got["/api/v2/things/list"] != "GET" {
		t.Errorf("file-const base should resolve to full path: %+v", got)
	}
	if got["/api/settings/engagement/send"] != "POST" {
		t.Errorf("constructor-default base should resolve to full path: %+v", got)
	}
	if _, found := got["/calculate"]; found {
		t.Errorf("bare suffix must not survive once the base resolves: %+v", got)
	}
}

// TestExtractHTTPClientFacts_BaseLiteralUnresolvedFallsBackToSuffix covers the
// fallback: a base that is not a known "/"-rooted literal (injected via a
// non-defaulted param, or an absolute/env base) is still stripped, yielding the
// suffix exactly as before — the resolver never fabricates a base.
func TestExtractHTTPClientFacts_BaseLiteralUnresolvedFallsBackToSuffix(t *testing.T) {
	src := "class A {\n" +
		"  constructor(private baseURL: string) {}\n" + // injected, no literal
		"  ext = 'https://third.party/v1';\n" + // absolute -> not "/"-rooted, not captured
		"  a() { return fetch(`${this.baseURL}/quick-action-preferences`); }\n" +
		"  b() { return fetch(`${this.ext}/thing`); }\n" +
		"}\n"
	got := byNameMethod(extractHTTPClientFacts([]byte(src), "src/lib/api/quickActionPreferences.ts"))

	if _, found := got["/quick-action-preferences"]; !found {
		t.Errorf("injected base should fall back to the stripped suffix: %+v", got)
	}
	if _, found := got["/thing"]; !found {
		t.Errorf("absolute base is not a resolvable literal; suffix kept: %+v", got)
	}
}

// TestExtractHTTPClientFacts_BaseLiteralAmbiguousNotResolved covers the ambiguity
// guard: an identifier bound to two different literals in the same file is dropped
// from the base map, so the resolver falls back to stripping rather than guessing.
func TestExtractHTTPClientFacts_BaseLiteralAmbiguousNotResolved(t *testing.T) {
	src := "const base = '/api/one';\n" +
		"const base = '/api/two';\n" + // conflicting binding -> ambiguous
		"function go() { return fetch(`${base}/x`); }\n"
	got := byNameMethod(extractHTTPClientFacts([]byte(src), "src/lib/api/thing.ts"))

	if _, found := got["/x"]; !found {
		t.Errorf("ambiguous base must fall back to the suffix: %+v", got)
	}
	for _, bad := range []string{"/api/one/x", "/api/two/x"} {
		if _, found := got[bad]; found {
			t.Errorf("ambiguous base must not resolve to %q: %+v", bad, got)
		}
	}
}
