package dotnetextractor

import (
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// conventionalOf runs the pipeline and returns the conventional route facts.
func conventionalOf(t *testing.T, files map[string]string) ([]facts.Fact, int) {
	t.Helper()
	var all []facts.Fact
	var sc aspnetScaffold
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		ff, s := extractFileASTFull([]byte(files[p]), p)
		all = append(all, ff...)
		sc.merge(s)
	}
	all = mergePartialTypes(all)
	resolveCSharpTargets(all)
	symbols := map[string]bool{}
	for _, f := range all {
		if f.Kind == facts.KindSymbol {
			symbols[f.Name] = true
		}
	}
	return conventionalRouteFacts(sc.conventional, symbols), sc.conventionalSkipped
}

// The OrchardCore shape: named arguments, a concrete pattern, and defaults
// naming the controller and action.
func TestConventional_AreaRouteWithDefaults(t *testing.T) {
	got, _ := conventionalOf(t, map[string]string{
		"src/Modules/Lists/Startup.cs": `
namespace OrchardCore.Lists;
public class Startup
{
    public override void Configure(IApplicationBuilder app, IEndpointRouteBuilder routes, IServiceProvider sp)
    {
        routes.MapAreaControllerRoute(
            name: "ListFeed",
            areaName: "OrchardCore.Feeds",
            pattern: "Contents/Lists/{contentItemId}/rss",
            defaults: new { controller = "Feed", action = "Index", format = "rss" }
        );
    }
}`,
		"src/Modules/Feeds/Controllers/FeedController.cs": `
namespace OrchardCore.Feeds.Controllers;
public class FeedController : Controller
{
    public IActionResult Index() => View();
}`,
	})
	if len(got) != 1 {
		t.Fatalf("routes = %d, want 1", len(got))
	}
	r := got[0]
	if r.Name != "/Contents/Lists/{contentItemId}/rss" {
		t.Errorf("path = %q", r.Name)
	}
	if r.Props["routing"] != "conventional" {
		t.Errorf("routing = %v", r.Props["routing"])
	}
	if r.Props["controller"] != "Feed" || r.Props["action"] != "Index" {
		t.Errorf("controller/action = %v/%v", r.Props["controller"], r.Props["action"])
	}
	var handled []string
	for _, rel := range r.Relations {
		if rel.Kind == facts.RelHandledBy {
			handled = append(handled, rel.Target)
		}
	}
	if len(handled) != 1 || handled[0] != "src/Modules/Feeds/Controllers.FeedController.Index" {
		t.Errorf("handled_by = %v", handled)
	}
}

// A template that stays generic after substitution names no single URL. Emitting
// a literal `{controller}` segment would claim a URL the app never serves.
func TestConventional_GenericTemplateIsNotEmitted(t *testing.T) {
	got, skipped := conventionalOf(t, map[string]string{
		"src/Routing/DefaultAreaControllerRouteMapper.cs": `
namespace OrchardCore.Mvc.Routing;
public class DefaultAreaControllerRouteMapper
{
    private const string DefaultAreaPattern = "/{area}/{controller}/{action}/{id?}";
    public void Map(IEndpointRouteBuilder routes)
    {
        routes.MapControllerRoute(name: "default", pattern: DefaultAreaPattern);
    }
}`,
	})
	if len(got) != 0 {
		t.Errorf("routes = %v, want none", got)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1 — the gap must stay visible", skipped)
	}
}

// The template is routinely a const field, not an inline literal, so it has to be
// resolved through the same literal environment the HTTP-client scan builds.
func TestConventional_PatternFromConstField(t *testing.T) {
	got, _ := conventionalOf(t, map[string]string{
		"src/Admin/Startup.cs": `
namespace Acme.Admin;
public class Startup
{
    private const string DashboardPattern = "Admin/Dashboard";
    public void Configure(IEndpointRouteBuilder routes)
    {
        routes.MapControllerRoute(
            name: "dashboard",
            pattern: DashboardPattern,
            defaults: new { controller = "Dashboard", action = "Show" });
    }
}`,
	})
	if len(got) != 1 || got[0].Name != "/Admin/Dashboard" {
		t.Fatalf("got %v, want [/Admin/Dashboard]", got)
	}
}

// areaName substitutes into an {area} token.
func TestConventional_AreaNameSubstitutes(t *testing.T) {
	got, _ := conventionalOf(t, map[string]string{
		"src/M/Startup.cs": `
namespace Acme;
public class Startup
{
    public void Configure(IEndpointRouteBuilder routes)
    {
        routes.MapAreaControllerRoute(
            name: "n", areaName: "Acme.Blog",
            pattern: "{area}/posts",
            defaults: new { controller = "Post", action = "List" });
    }
}`,
	})
	if len(got) != 1 || got[0].Name != "/Acme.Blog/posts" {
		t.Fatalf("got %v, want [/Acme.Blog/posts]", got)
	}
}

// The unnamed spelling puts the pattern second.
func TestConventional_PositionalArguments(t *testing.T) {
	got, _ := conventionalOf(t, map[string]string{
		"src/M/Startup.cs": `
namespace Acme;
public class Startup
{
    public void Configure(IEndpointRouteBuilder routes)
    {
        routes.MapControllerRoute("blog", "blog/archive", new { controller = "Blog", action = "Archive" });
    }
}`,
	})
	if len(got) != 1 || got[0].Name != "/blog/archive" {
		t.Fatalf("got %v, want [/blog/archive]", got)
	}
}

// Two controllers of the same short name cannot be told apart, so binding one
// would be a guess. The route still stands; only the handler is withheld.
func TestConventional_AmbiguousControllerBindsNoHandler(t *testing.T) {
	got, _ := conventionalOf(t, map[string]string{
		"src/M/Startup.cs": `
namespace Acme;
public class Startup
{
    public void Configure(IEndpointRouteBuilder routes)
    {
        routes.MapControllerRoute(name: "n", pattern: "things",
            defaults: new { controller = "Thing", action = "Index" });
    }
}`,
		"src/A/ThingController.cs": "namespace A; public class ThingController : Controller { public IActionResult Index() => View(); }",
		"src/B/ThingController.cs": "namespace B; public class ThingController : Controller { public IActionResult Index() => View(); }",
	})
	if len(got) != 1 {
		t.Fatalf("routes = %d, want 1", len(got))
	}
	for _, rel := range got[0].Relations {
		if rel.Kind == facts.RelHandledBy {
			t.Errorf("bound an ambiguous handler: %v", rel.Target)
		}
	}
}
