package phpextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// clientRoutes filters the route facts that represent outbound HTTP-client calls.
func clientRoutes(ff []facts.Fact) []facts.Fact {
	var out []facts.Fact
	for _, f := range ff {
		if f.Kind == facts.KindRoute && f.Props["role"] == "client" {
			out = append(out, f)
		}
	}
	return out
}

func findClient(t *testing.T, ff []facts.Fact, path string) facts.Fact {
	t.Helper()
	for _, f := range clientRoutes(ff) {
		if f.Name == path {
			return f
		}
	}
	t.Fatalf("no client route with path %q; got %+v", path, clientRoutes(ff))
	return facts.Fact{}
}

func TestPHPHTTPClient_GuzzleVerb(t *testing.T) {
	src := `<?php
class OrderService {
    public function fetch() {
        return $this->client->get('/api/orders/1');
    }
}
`
	got := clientRoutes(extractPHPHTTPClientFacts([]byte(src), "app/Services/OrderService.php"))
	if len(got) != 1 {
		t.Fatalf("got %d client routes, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Name != "/api/orders/1" {
		t.Errorf("path = %q, want /api/orders/1", f.Name)
	}
	if f.Props["method"] != "GET" {
		t.Errorf("method = %v, want GET", f.Props["method"])
	}
	if f.Props["framework"] != "guzzle" {
		t.Errorf("framework = %v, want guzzle", f.Props["framework"])
	}
}

func TestPHPHTTPClient_GuzzleRequest(t *testing.T) {
	src := `<?php
$resp = $client->request('POST', '/payments');
`
	f := findClient(t, extractPHPHTTPClientFacts([]byte(src), "src/pay.php"), "/payments")
	if f.Props["method"] != "POST" {
		t.Errorf("method = %v, want POST", f.Props["method"])
	}
	if f.Props["framework"] != "guzzle" {
		t.Errorf("framework = %v, want guzzle", f.Props["framework"])
	}
}

func TestPHPHTTPClient_LaravelFacade(t *testing.T) {
	src := `<?php
$r = Http::get('/widgets/42');
`
	f := findClient(t, extractPHPHTTPClientFacts([]byte(src), "app/foo.php"), "/widgets/42")
	if f.Props["framework"] != "laravel-http" {
		t.Errorf("framework = %v, want laravel-http", f.Props["framework"])
	}
	if f.Props["method"] != "GET" {
		t.Errorf("method = %v, want GET", f.Props["method"])
	}
}

func TestPHPHTTPClient_LaravelFacadeChain(t *testing.T) {
	src := `<?php
$r = Http::withToken($token)->post('/things');
`
	f := findClient(t, extractPHPHTTPClientFacts([]byte(src), "app/foo.php"), "/things")
	if f.Props["framework"] != "laravel-http" {
		t.Errorf("framework = %v, want laravel-http", f.Props["framework"])
	}
	if f.Props["method"] != "POST" {
		t.Errorf("method = %v, want POST", f.Props["method"])
	}
}

func TestPHPHTTPClient_SymfonyHttpClient(t *testing.T) {
	src := `<?php
$response = $httpClient->request('GET', '/api/v1/items');
`
	f := findClient(t, extractPHPHTTPClientFacts([]byte(src), "src/Client.php"), "/api/v1/items")
	if f.Props["framework"] != "symfony-httpclient" {
		t.Errorf("framework = %v, want symfony-httpclient", f.Props["framework"])
	}
}

func TestPHPHTTPClient_CurlAndFileGetContents(t *testing.T) {
	src := `<?php
$ch = curl_init();
curl_setopt($ch, CURLOPT_URL, '/internal/sync');
$body = file_get_contents('/local/data');
`
	got := clientRoutes(extractPHPHTTPClientFacts([]byte(src), "src/sync.php"))
	if len(got) != 2 {
		t.Fatalf("got %d client routes, want 2: %+v", len(got), got)
	}
	curl := findClient(t, got, "/internal/sync")
	if curl.Props["framework"] != "curl" {
		t.Errorf("curl framework = %v, want curl", curl.Props["framework"])
	}
	fgc := findClient(t, got, "/local/data")
	if fgc.Props["framework"] != "file-get-contents" {
		t.Errorf("file_get_contents framework = %v, want file-get-contents", fgc.Props["framework"])
	}
}

func TestPHPHTTPClient_SkipsAbsoluteAndDynamic(t *testing.T) {
	src := `<?php
$a = $client->get('https://api.example.com/x');
$b = $client->get("/users/{$id}");
$c = $client->get('/orders/' . $id);
`
	got := clientRoutes(extractPHPHTTPClientFacts([]byte(src), "src/x.php"))
	if len(got) != 0 {
		t.Fatalf("expected absolute + interpolated + concatenated to be skipped, got %d: %+v", len(got), got)
	}
}

func TestPHPHTTPClient_TargetHintFromEnv(t *testing.T) {
	src := `<?php
$base = getenv('BILLING_BASE_URL');
$r = $client->get('/invoices');
`
	f := findClient(t, extractPHPHTTPClientFacts([]byte(src), "src/billing.php"), "/invoices")
	if f.Props["target_hint"] != "billing" {
		t.Errorf("target_hint = %v, want billing", f.Props["target_hint"])
	}
}

func TestPHPHTTPClient_IgnoresNonClientReceiver(t *testing.T) {
	src := `<?php
$record->posts->get(1);
$collection->get('first');
`
	got := clientRoutes(extractPHPHTTPClientFacts([]byte(src), "src/x.php"))
	// $collection->get('first') has a string arg but 'first' is a non-path token and
	// $collection is not a client receiver, so nothing should be emitted.
	if len(got) != 0 {
		t.Fatalf("expected no client routes, got %d: %+v", len(got), got)
	}
}
