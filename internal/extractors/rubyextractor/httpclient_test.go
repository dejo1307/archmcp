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
