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
