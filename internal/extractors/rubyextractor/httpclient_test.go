package rubyextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// clientRoutes filters facts to the outbound HTTP-client routes.
func clientRoutes(ff []facts.Fact) []facts.Fact {
	var out []facts.Fact
	for _, f := range ff {
		if f.Kind == facts.KindRoute && f.Props["role"] == "client" {
			out = append(out, f)
		}
	}
	return out
}

func TestRubyHTTPClient_WrapperClient(t *testing.T) {
	src := `class PurchaseService
  def build
    SvcCheckoutClient.post('purchase/build', body: payload)
  end
end
`
	got := clientRoutes(extractRubyHTTPClientFacts([]byte(src), "app/services/purchase_service.rb"))
	if len(got) != 1 {
		t.Fatalf("got %d client routes, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Name != "purchase/build" {
		t.Errorf("path = %q, want purchase/build", f.Name)
	}
	if f.Props["method"] != "POST" {
		t.Errorf("method = %v, want POST", f.Props["method"])
	}
	if f.Props["source"] != "ruby-http-client" {
		t.Errorf("source = %v, want ruby-http-client", f.Props["source"])
	}
	if f.Props["framework"] != "http-client" {
		t.Errorf("framework = %v, want http-client", f.Props["framework"])
	}
	if f.Props["target_hint"] != "svccheckout" {
		t.Errorf("target_hint = %v, want svccheckout", f.Props["target_hint"])
	}
}

func TestRubyHTTPClient_Faraday(t *testing.T) {
	src := `conn.get('users/123')
Faraday.post("orders/new")
`
	got := clientRoutes(extractRubyHTTPClientFacts([]byte(src), "app/clients/api.rb"))
	if len(got) != 2 {
		t.Fatalf("got %d client routes, want 2: %+v", len(got), got)
	}
	for _, f := range got {
		if f.Props["framework"] != "faraday" {
			t.Errorf("%s framework = %v, want faraday", f.Name, f.Props["framework"])
		}
	}
}

func TestRubyHTTPClient_Interpolation(t *testing.T) {
	src := `client.get("users/#{id}/posts")`
	got := clientRoutes(extractRubyHTTPClientFacts([]byte(src), "app/clients/api.rb"))
	if len(got) != 1 {
		t.Fatalf("got %d client routes, want 1", len(got))
	}
	if got[0].Name != "users/{}/posts" {
		t.Errorf("path = %q, want users/{}/posts", got[0].Name)
	}
}

func TestRubyHTTPClient_EnvVarHint(t *testing.T) {
	src := `class XendoClient
  BASE = ENV.fetch('XENDO_URL')
  def fetch_thing
    conn.get('things/list')
  end
end
`
	got := clientRoutes(extractRubyHTTPClientFacts([]byte(src), "app/clients/xendo_client.rb"))
	if len(got) != 1 {
		t.Fatalf("got %d client routes, want 1", len(got))
	}
	if got[0].Props["target_hint"] != "xendo" {
		t.Errorf("target_hint = %v, want xendo (from XENDO_URL)", got[0].Props["target_hint"])
	}
}

func TestRubyHTTPClient_SkipsNonHTTP(t *testing.T) {
	src := `User.where(active: true)
params.get(:id)
cache.get('user:123')
record.posts.get(0)
client.get('https://api.stripe.com/v1/charges')
`
	got := clientRoutes(extractRubyHTTPClientFacts([]byte(src), "app/models/user.rb"))
	if len(got) != 0 {
		t.Fatalf("got %d client routes, want 0: %+v", len(got), got)
	}
}

func TestRubyHTTPClient_WrapperLiteral(t *testing.T) {
	src := []byte("class Insights\n  def pageview(attributes)\n    connection.post(build_url(\"/pageview\"), attributes)\n  end\nend\n")
	ff := extractRubyHTTPClientFacts(src, "app/services/insights.rb")
	if len(ff) != 1 || ff[0].Name != "/pageview" || ff[0].Props["method"] != "POST" {
		t.Fatalf("wrapper-literal call = %+v, want one POST /pageview", ff)
	}
	if ff[0].Props["derived"] != "wrapper-literal" {
		t.Fatalf("wrapper route must carry its derivation form, got %v", ff[0].Props["derived"])
	}
	nonPath := []byte("conn.post(build_url(\"pageview\"))\nconn.get(t(\"labels.title\"))\n")
	if got := extractRubyHTTPClientFacts(nonPath, "app/services/other.rb"); len(got) != 0 {
		t.Fatalf("non-/-rooted wrapper literals derived: %+v", got)
	}
}

// TestRubyHTTPClient_TyphoeusWrapperMethod pins the request-wrapper derivation:
// a file whose single Typhoeus sink templates "#{base}#{path}" over a def
// parameter threads the rooted path literals from same-file call sites, with
// the sink's literal verb and the file's env-var hint.
func TestRubyHTTPClient_TyphoeusWrapperMethod(t *testing.T) {
	src := []byte(`class AnalyticsClient
  def events(company_id:, date:, page:)
    make_request(
      path: "/exports/#{company_id}/events",
      params: { date: date, page: page }
    )
  end

  def visits(company_id:)
    make_request(
      path: "/exports/#{company_id}/visits",
      params: {}
    )
  end

  private

  def initialize
    @base_url = ENV["ANALYTICS_BASE_URL"]
  end

  def make_request(path:, params:)
    request = Typhoeus::Request.new(
      "#{base_url}#{path}",
      method: :get,
      params: params
    )
    request.run
  end
end
`)
	ff := extractRubyHTTPClientFacts(src, "app/models/analytics_client.rb")
	var derived []facts.Fact
	for _, f := range ff {
		if f.Props["derived"] == "wrapper-method" {
			derived = append(derived, f)
		}
	}
	if len(derived) != 2 {
		t.Fatalf("got %d wrapper-method routes, want 2: %+v", len(derived), derived)
	}
	if derived[0].Name != "/exports/{}/events" || derived[0].Props["method"] != "GET" {
		t.Fatalf("first derived route = %+v, want GET /exports/{}/events", derived[0])
	}
	if derived[0].Props["target_hint"] != "analytics" {
		t.Fatalf("env base must hint the provider, got %v", derived[0].Props["target_hint"])
	}
	if derived[0].Props["framework"] != "typhoeus" {
		t.Fatalf("framework = %v, want typhoeus", derived[0].Props["framework"])
	}
}

// TestRubyHTTPClient_TyphoeusWrapperAmbiguityDerivesNothing pins the single-sink
// rule: two Typhoeus sinks in one file, or a tail identifier that is not a
// parameter of the enclosing def, derive nothing.
func TestRubyHTTPClient_TyphoeusWrapperAmbiguityDerivesNothing(t *testing.T) {
	twoSinks := []byte(`class C
  def a(path:)
    Typhoeus::Request.new("#{base}#{path}", method: :get)
  end
  def b(path:)
    Typhoeus::Request.new("#{base}#{path}", method: :post)
  end
  def use
    a(path: "/x/y")
  end
end
`)
	if got := extractRubyHTTPClientFacts(twoSinks, "app/models/c.rb"); len(got) != 0 {
		t.Fatalf("two sinks must derive nothing, got %+v", got)
	}
	notParam := []byte(`class C
  def fire(other:)
    Typhoeus::Request.new("#{base}#{path}", method: :get)
  end
  def use
    fire(other: "/x/y")
  end
end
`)
	if got := extractRubyHTTPClientFacts(notParam, "app/models/c2.rb"); len(got) != 0 {
		t.Fatalf("tail not a def parameter must derive nothing, got %+v", got)
	}
}

// TestRubyHTTPClient_TyphoeusDirectVerb pins the direct form: Typhoeus.get with
// a literal path is a plain client call under the typhoeus framework.
func TestRubyHTTPClient_TyphoeusDirectVerb(t *testing.T) {
	src := []byte("Typhoeus.get(\"/health/check\")\n")
	ff := extractRubyHTTPClientFacts(src, "app/services/probe.rb")
	if len(ff) != 1 || ff[0].Name != "/health/check" || ff[0].Props["framework"] != "typhoeus" {
		t.Fatalf("direct Typhoeus verb = %+v, want one GET /health/check via typhoeus", ff)
	}
}

// TestRubyHTTPClient_HintOnlyFromURLShapedEnvVars pins the garbage-hint
// regression from the estate: a file whose FIRST env read is a rate limiter
// (INSIGHTS_CONCURRENT_RATE_LIMIT) must not hint at all from it, and the
// wrapper derivation must take its hint from the sink's base-identifier
// assignment instead — a wrong hint steers the matcher toward a wrong edge.
func TestRubyHTTPClient_HintOnlyFromURLShapedEnvVars(t *testing.T) {
	if h := envVarHint(`LIMIT = Sidekiq::Limiter.concurrent("x", ENV.fetch("ANALYTICS_CONCURRENT_RATE_LIMIT", 1))`); h != "" {
		t.Fatalf("a non-URL env name must yield no hint, got %q", h)
	}
	src := []byte(`LIMIT = ENV.fetch("ANALYTICS_CONCURRENT_RATE_LIMIT", 1)
class AnalyticsClient
  def events(company_id:)
    make_request(
      path: "/exports/#{company_id}/events",
      params: {}
    )
  end

  private

  def initialize
    @base_url = ENV["ANALYTICS_BASE_URL"]
  end

  def make_request(path:, params:)
    request = Typhoeus::Request.new(
      "#{base_url}#{path}",
      method: :get,
      params: params
    )
    request.run
  end
end
`)
	ff := extractRubyHTTPClientFacts(src, "app/models/analytics_client.rb")
	found := false
	for _, f := range ff {
		if f.Props["derived"] == "wrapper-method" {
			found = true
			if f.Props["target_hint"] != "analytics" {
				t.Fatalf("hint must come from the base assignment, got %v", f.Props["target_hint"])
			}
		}
	}
	if !found {
		t.Fatal("wrapper-method route not derived")
	}
}
