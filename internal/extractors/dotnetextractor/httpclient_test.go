package dotnetextractor

import (
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// clientRoutesOf runs the pipeline and returns "METHOD path" for every
// role=client route.
func clientRoutesOf(t *testing.T, files map[string]string) []string {
	t.Helper()
	var sc aspnetScaffold
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		_, s := extractFileASTFull([]byte(files[p]), p)
		sc.merge(s)
	}
	var out []string
	for _, f := range clientRouteFacts(sc.clients) {
		if f.Props[facts.PropRole] != facts.RoleClient {
			t.Fatalf("route %q is not role=client", f.Name)
		}
		out = append(out, f.Props["method"].(string)+" "+f.Name)
	}
	sort.Strings(out)
	return out
}

// The eShop shape: the verb is on the call, the path is in a local, and the local
// is an interpolation over a field.
func TestHTTPClient_BaseUrlFieldAndInterpolation(t *testing.T) {
	got := clientRoutesOf(t, map[string]string{
		"src/WebApp/Services/CatalogService.cs": `
namespace eShop.WebApp.Services;
public class CatalogService(HttpClient httpClient)
{
    private readonly string remoteServiceBaseUrl = "api/catalog/";

    public Task<CatalogItem> GetCatalogItem(int id)
    {
        var uri = $"{remoteServiceBaseUrl}items/{id}?api-version=2.0";
        return httpClient.GetFromJsonAsync(uri, Ctx.Default.CatalogItem);
    }

    public Task<CatalogResult> GetBrands()
    {
        var uri = $"{remoteServiceBaseUrl}catalogBrands?api-version=2.0";
        return httpClient.GetFromJsonAsync(uri, Ctx.Default.Brands);
    }
}`,
	})
	want := []string{"GET /api/catalog/catalogBrands", "GET /api/catalog/items/{id}"}
	if len(got) != len(want) {
		t.Fatalf("routes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("route %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// An interpolation hole is a path PARAMETER, not something to drop: keeping its
// name is what lets `items/{id}` match the server template of the same shape.
func TestHTTPClient_InterpolationHoleBecomesAParameter(t *testing.T) {
	got := clientRoutesOf(t, map[string]string{
		"a/C.cs": `public class C {
    public void M(HttpClient h, int orderId) { h.GetAsync($"/api/orders/{orderId}"); }
}`,
	})
	if len(got) != 1 || got[0] != "GET /api/orders/{orderId}" {
		t.Errorf("got %v, want [GET /api/orders/{orderId}]", got)
	}
}

func TestHTTPClient_VerbsFromMethodNames(t *testing.T) {
	got := clientRoutesOf(t, map[string]string{
		"a/C.cs": `public class C {
    public void M(HttpClient h) {
        h.GetAsync("/api/a");
        h.PostAsJsonAsync("/api/b", body);
        h.PutAsync("/api/c", body);
        h.DeleteAsync("/api/d");
        h.PatchAsJsonAsync("/api/e", body);
    }
}`,
	})
	want := []string{"DELETE /api/d", "GET /api/a", "PATCH /api/e", "POST /api/b", "PUT /api/c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// SendAsync takes an HttpRequestMessage whose verb is set elsewhere, so the call
// site cannot classify it. Guessing GET would invent an edge.
func TestHTTPClient_SendAsyncIsNotClassified(t *testing.T) {
	got := clientRoutesOf(t, map[string]string{
		"a/C.cs": `public class C { public void M(HttpClient h) { h.SendAsync(req); } }`,
	})
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

// Under Aspire the host is a SERVICE NAME, not a hostname. Only the path is
// comparable with a server's template.
func TestHTTPClient_AbsoluteUrlKeepsOnlyThePath(t *testing.T) {
	got := clientRoutesOf(t, map[string]string{
		"a/C.cs": `public class C {
    public void M(HttpClient h) {
        h.GetAsync("https+http://catalog-api/api/catalog/items");
        h.GetAsync("https://example.com/v1/things?page=2");
    }
}`,
	})
	want := []string{"GET /api/catalog/items", "GET /v1/things"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A bare host names no endpoint, and a path made only of parameters would match
// every server template at that depth.
func TestHTTPClient_UninformativePathsAreDropped(t *testing.T) {
	got := clientRoutesOf(t, map[string]string{
		"a/C.cs": `public class C {
    public void M(HttpClient h, string x) {
        h.GetAsync("https://example.com");
        h.GetAsync($"/{x}");
        h.GetAsync(ComputedAtRuntime());
    }
}`,
	})
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestHTTPClient_StringConcatenation(t *testing.T) {
	got := clientRoutesOf(t, map[string]string{
		"a/C.cs": `public class C {
    const string Base = "/api/admin/";
    public void M(HttpClient h) { h.GetAsync(Base + "settings"); }
}`,
	})
	if len(got) != 1 || got[0] != "GET /api/admin/settings" {
		t.Errorf("got %v, want [GET /api/admin/settings]", got)
	}
}

// A call reached before its base-URL field is declared must still resolve, which
// is why literals are collected in a separate pass.
func TestHTTPClient_LiteralDeclaredAfterUse(t *testing.T) {
	got := clientRoutesOf(t, map[string]string{
		"a/C.cs": `public class C {
    public void M(HttpClient h) { h.GetAsync($"{Base}items"); }
    private readonly string Base = "api/catalog/";
}`,
	})
	if len(got) != 1 || got[0] != "GET /api/catalog/items" {
		t.Errorf("got %v, want [GET /api/catalog/items]", got)
	}
}

func TestHTTPClient_Refit(t *testing.T) {
	got := clientRoutesOf(t, map[string]string{
		"a/IOrdersApi.cs": `
namespace Acme.Clients;
public interface IOrdersApi
{
    [Get("/api/orders/{id}")]
    Task<Order> GetOrder(int id);

    [Post("/api/orders")]
    Task Create([Body] Order o);
}`,
	})
	want := []string{"GET /api/orders/{id}", "POST /api/orders"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("route %d = %q, want %q", i, got[i], want[i])
		}
	}
}
