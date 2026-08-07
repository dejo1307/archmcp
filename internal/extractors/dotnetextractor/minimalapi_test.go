package dotnetextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestMinimalAPI_GroupPrefixComposedFromLocalVariable(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Apis/OrdersApi.cs": `
namespace Acme.Api;

public static class OrdersApi
{
    public static RouteGroupBuilder MapOrdersApiV1(this IEndpointRouteBuilder app)
    {
        var api = app.MapGroup("api/orders");

        api.MapPut("/cancel", CancelOrderAsync);
        api.MapGet("{orderId:int}", GetOrderAsync);
        api.MapGet("/", GetOrdersByUserAsync);

        return api;
    }

    public static void CancelOrderAsync() { }
    public static void GetOrderAsync() { }
    public static void GetOrdersByUserAsync() { }
}
`,
	})
	assertRoutes(t, got, []string{
		"PUT /api/orders/cancel",
		"GET /api/orders/{orderId:int}",
		"GET /api/orders",
	})
}

// TestMinimalAPI_ChainedAndNestedGroups covers the two shapes eShop and the MCP
// SDK actually write: a fluent call after MapGroup, and a group built on a group.
func TestMinimalAPI_ChainedAndNestedGroups(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Apis/CatalogApi.cs": `
namespace Acme.Api;

public static class CatalogApi
{
    public static void MapCatalogApi(this IEndpointRouteBuilder app)
    {
        // A fluent call after MapGroup must not hide the prefix.
        var api = app.MapGroup("api/catalog").HasApiVersion(1, 0);
        api.MapGet("/items", GetAllItems);

        // A group of a group composes both prefixes.
        var admin = api.MapGroup("admin");
        admin.MapDelete("/items/{id:int}", DeleteItem);
    }

    public static void GetAllItems() { }
    public static void DeleteItem() { }
}
`,
	})
	assertRoutes(t, got, []string{
		"GET /api/catalog/items",
		"DELETE /api/catalog/admin/items/{id:int}",
	})
}

// TestMinimalAPI_NonLiteralGroupPrefixDropsItsRoutes is the false-positive guard.
// The MCP C# SDK mounts its whole surface at a caller-supplied pattern:
//
//	public static IEndpointConventionBuilder MapMcp(this IEndpointRouteBuilder endpoints, string pattern = "")
//	{
//	    var mcpGroup = endpoints.MapGroup(pattern);
//	    ...
//	    streamableHttpGroup.MapPost("", handler);
//	}
//
// The real path is whatever the host application mounts it at. Publishing the
// registration path alone would claim an endpoint the library does not serve — and
// since that path is "", every one of them would land on "/", recreating the
// phantom-root collapse conventional routing produced.
func TestMinimalAPI_NonLiteralGroupPrefixDropsItsRoutes(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/McpEndpoints.cs": `
namespace Acme.Api;

public static class McpEndpoints
{
    public static void MapMcp(this IEndpointRouteBuilder endpoints, string pattern = "")
    {
        var mcpGroup = endpoints.MapGroup(pattern);
        var streamable = mcpGroup.MapGroup("");

        streamable.MapPost("", HandlePost);
        streamable.MapGet("/sse", HandleSse);
    }

    public static void HandlePost() { }
    public static void HandleSse() { }
}
`,
	})
	if len(got) != 0 {
		t.Errorf("routes under an unresolvable group prefix should be dropped, got %v", got)
	}
}

// TestMinimalAPI_RootBuilderReceiver covers a registration straight on the
// `app`/`endpoints` parameter, with no group at all.
func TestMinimalAPI_RootBuilderReceiver(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/Defaults.cs": `
namespace Acme.Api;

public static class Defaults
{
    public static void MapDefaults(this IEndpointRouteBuilder app)
    {
        app.MapGet("/", Redirect);
        app.MapGet("/health", Health);
    }

    public static void Redirect() { }
    public static void Health() { }
}
`,
	})
	assertRoutes(t, got, []string{"GET /", "GET /health"})
}

// TestMinimalAPI_GroupScopeDoesNotLeakBetweenMethods pins the body scoping: a
// group variable bound in one method must not resolve a registration in another,
// which would silently give the second method's routes the first's prefix.
func TestMinimalAPI_GroupScopeDoesNotLeakBetweenMethods(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/Two.cs": `
namespace Acme.Api;

public static class Two
{
    public static void MapA(this IEndpointRouteBuilder app)
    {
        var api = app.MapGroup("api/first");
        api.MapGet("/x", HandlerA);
    }

    public static void MapB(this IEndpointRouteBuilder app)
    {
        // Same variable name, never bound in this body: it is the root builder
        // here, not the group from MapA.
        api.MapGet("/y", HandlerB);
    }

    public static void HandlerA() { }
    public static void HandlerB() { }
}
`,
	})
	assertRoutes(t, got, []string{"GET /api/first/x", "GET /y"})
}

// TestMinimalAPI_NonRouteMapCallsAreIgnored is the over-reach control. The
// string-literal-first-argument rule is what keeps MapControllers, MapHub<T> and
// MapRazorPages — which take no path — out of the route set, without needing a
// list of method names to exclude.
func TestMinimalAPI_NonRouteMapCallsAreIgnored(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/Wiring.cs": `
namespace Acme.Api;

public static class Wiring
{
    public static void MapAll(this IEndpointRouteBuilder app)
    {
        app.MapControllers();
        app.MapRazorPages();
        app.MapHub<ChatHub>("/hub");
        app.MapGet(BuildPath(), Handler);
        app.MapGet("/real", Handler);
    }

    public static void Handler() { }
    public static string BuildPath() => "/computed";
}
`,
	})
	// MapHub<T>("/hub") is a SignalR hub, not a minimal-API endpoint, but it is
	// indistinguishable here by argument shape — it is excluded because MapHub is
	// not in the verb table. A computed path is excluded because it is not a literal.
	assertRoutes(t, got, []string{"GET /real"})
}

func TestMinimalAPI_HandlerBindingAndLambdas(t *testing.T) {
	_, all := routeSet(t, map[string]string{
		"Api/Mixed.cs": `
namespace Acme.Api;

public static class Mixed
{
    public static void Map(this IEndpointRouteBuilder app)
    {
        app.MapGet("/named", GetNamed);
        app.MapGet("/inline", () => Results.Ok());
    }

    public static void GetNamed() { }
}
`,
	})
	var named, inline *facts.Fact
	for i := range all {
		if all[i].Kind != facts.KindRoute {
			continue
		}
		switch all[i].Name {
		case "/named":
			named = &all[i]
		case "/inline":
			inline = &all[i]
		}
	}
	if named == nil || inline == nil {
		t.Fatal("expected both routes")
	}
	want := "Api.Mixed.GetNamed"
	if named.Props["handler"] != want {
		t.Errorf("handler = %v, want %q", named.Props["handler"], want)
	}
	if !hasRel(named, facts.RelHandledBy, want) {
		t.Errorf("missing handled_by; got %v", named.Relations)
	}
	// A lambda has no symbol to point at, so the route carries no handler at all
	// rather than a fabricated one.
	if _, ok := inline.Props["handler"]; ok {
		t.Errorf("lambda route should carry no handler, got %v", inline.Props["handler"])
	}
	for _, r := range inline.Relations {
		if r.Kind == facts.RelHandledBy {
			t.Errorf("lambda route should have no handled_by, got %v", r)
		}
	}
}

// TestMinimalAPI_TopLevelStatements covers a Program.cs with no enclosing class,
// where the whole file is one implicit body.
func TestMinimalAPI_TopLevelStatements(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Program.cs": `
var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();

var api = app.MapGroup("/api");
api.MapGet("/ping", () => "pong");
app.MapPost("/echo", () => "echo");

app.Run();
`,
	})
	assertRoutes(t, got, []string{"GET /api/ping", "POST /echo"})
}
