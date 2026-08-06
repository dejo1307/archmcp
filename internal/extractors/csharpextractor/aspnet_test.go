package csharpextractor

import (
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// routeSet runs the full extract-and-compose pipeline over a set of files and
// returns "METHOD path" strings, so a test asserts on the composed URL rather than
// on the scaffold that produced it.
func routeSet(t *testing.T, files map[string]string) ([]string, []facts.Fact) {
	t.Helper()
	var all []facts.Fact
	var sc aspnetScaffold
	// Deterministic file order: the composer must not depend on it, and a map
	// range would hide that if it ever did.
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
	routes := composeControllerRoutes(all, sc)
	all = append(all, routes...)

	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, r.Props["method"].(string)+" "+r.Name)
	}
	sort.Strings(out)
	return out, all
}

func assertRoutes(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("routes = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("route %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestASPNet_ClassRouteComposedWithMethodTemplate(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/ActivityLogController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

[Route("System/ActivityLog")]
public class ActivityLogController : ControllerBase
{
    [HttpGet("Entries")]
    public IActionResult GetLogEntries() => Ok();

    [HttpDelete]
    public IActionResult Clear() => Ok();
}
`,
	})
	// A bare [HttpDelete] carries no template and serves the class path itself.
	assertRoutes(t, got, []string{
		"GET /System/ActivityLog/Entries",
		"DELETE /System/ActivityLog",
	})
}

// TestASPNet_RouteInheritedFromBaseClass is the case a per-file composition cannot
// do: 40 of jellyfin's 64 controllers declare no [Route] and inherit
// [Route("[controller]")] from a shared base in another file. Without the
// inheritance walk they get no path at all; without the token substitution they
// all get the same one.
func TestASPNet_RouteInheritedFromBaseClass(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/BaseApiController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

[ApiController]
[Route("[controller]")]
public class BaseApiController : ControllerBase { }
`,
		"Api/AudioController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

public class AudioController : BaseApiController
{
    [HttpGet("{itemId}/stream")]
    public IActionResult GetAudioStream() => Ok();
}
`,
		"Api/VideoController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

public class VideoController : BaseApiController
{
    [HttpGet("{itemId}/stream")]
    public IActionResult GetVideoStream() => Ok();
}
`,
	})
	// Each controller resolves [controller] to its OWN name, so one shared base
	// attribute yields distinct paths.
	assertRoutes(t, got, []string{
		"GET /Audio/{itemId}/stream",
		"GET /Video/{itemId}/stream",
	})
}

// TestASPNet_EmptyClassTemplateIsNotAMissingOne pins the distinction the HasRoute
// flag exists for: [Route("")] declares a template that happens to be empty, and
// its actions are served at the method template alone. That is NOT the same as
// declaring no [Route], which is conventional routing and yields nothing.
func TestASPNet_EmptyClassTemplateIsNotAMissingOne(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/TimeSyncController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

[Route("")]
public class TimeSyncController : ControllerBase
{
    [HttpGet("GetUtcTime")]
    public IActionResult GetUtcTime() => Ok();
}
`,
	})
	assertRoutes(t, got, []string{"GET /GetUtcTime"})
}

// TestASPNet_ConventionalRoutingEmitsNothing is the false-positive guard. An MVC
// controller with bare [HttpGet]s and no [Route] anywhere gets its URL from a
// template registered in Program.cs. Composing from what IS visible produced "/"
// for every action — the wrong path, and (facts being name-keyed) several actions
// collapsing onto a single root node.
func TestASPNet_ConventionalRoutingEmitsNothing(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/AccountController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

public class AccountController : Controller
{
    [HttpGet]
    public IActionResult Login() => View();

    [HttpPost]
    public IActionResult Login(LoginModel model) => View();

    [HttpGet]
    public IActionResult Logout() => View();
}
`,
	})
	if len(got) != 0 {
		t.Errorf("conventional routing should emit no routes, got %v", got)
	}
}

// TestASPNet_AbsoluteMethodTemplateOverridesClass covers ASP.NET's rule that a
// template beginning with "/" or "~/" replaces the controller template rather than
// extending it.
func TestASPNet_AbsoluteMethodTemplateOverridesClass(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/ThingsController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

[Route("api/things")]
public class ThingsController : ControllerBase
{
    [HttpGet("list")]
    public IActionResult List() => Ok();

    [HttpGet("/health")]
    public IActionResult Health() => Ok();

    [HttpGet("~/version")]
    public IActionResult Version() => Ok();
}
`,
	})
	assertRoutes(t, got, []string{
		"GET /api/things/list",
		"GET /health",
		"GET /version",
	})
}

// TestASPNet_NamedArgumentIsNotATemplate pins the first-argument-only rule.
// jellyfin writes [HttpGet("{itemId}/stream", Name = "GetAudioStream")] throughout
// and [HttpHead(Name = "HeadAudioStream")] alongside it. An implementation that
// searched the argument list for "a string literal" — the obvious way to write
// this — would put a route's DISPLAY NAME into its URL, giving
// /Audio/HeadAudioStream for an endpoint served at /Audio.
func TestASPNet_NamedArgumentIsNotATemplate(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/AudioController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

[Route("Audio")]
public class AudioController : ControllerBase
{
    [HttpGet("{itemId}/stream", Name = "GetAudioStream")]
    public IActionResult GetAudioStream() => Ok();

    [HttpHead(Name = "HeadAudioStream")]
    public IActionResult HeadAudioStream() => Ok();
}
`,
	})
	assertRoutes(t, got, []string{
		"GET /Audio/{itemId}/stream",
		"HEAD /Audio",
	})
}

func TestASPNet_ActionTokenAndMethodLevelRoute(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/ReportsController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

[Route("[controller]/[action]")]
public class ReportsController : ControllerBase
{
    [HttpGet]
    public IActionResult Daily() => Ok();
}
`,
		"Api/LegacyController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

[Route("legacy")]
public class LegacyController : ControllerBase
{
    // [HttpPost] with no template plus a separate [Route] is equivalent to
    // [HttpPost("submit")].
    [HttpPost]
    [Route("submit")]
    public IActionResult Submit() => Ok();
}
`,
	})
	assertRoutes(t, got, []string{
		"GET /Reports/Daily",
		"POST /legacy/submit",
	})
}

// TestASPNet_HandlerBinding checks that every route names, and links to, the method
// that serves it — the edge impact_analysis and find_path traverse.
func TestASPNet_HandlerBinding(t *testing.T) {
	_, all := routeSet(t, map[string]string{
		"Api/ItemsController.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

[Route("Items")]
public class ItemsController : ControllerBase
{
    [HttpGet("{id}")]
    public IActionResult GetItem(int id) => Ok();
}
`,
	})
	symbols := map[string]bool{}
	for _, f := range all {
		if f.Kind == facts.KindSymbol {
			symbols[f.Name] = true
		}
	}
	var route *facts.Fact
	for i := range all {
		if all[i].Kind == facts.KindRoute {
			route = &all[i]
		}
	}
	if route == nil {
		t.Fatal("no route emitted")
	}
	want := "Api.ItemsController.GetItem"
	if route.Props["handler"] != want {
		t.Errorf("handler = %v, want %q", route.Props["handler"], want)
	}
	if !symbols[want] {
		t.Fatalf("handler %q names no symbol fact", want)
	}
	if !hasRel(route, facts.RelHandledBy, want) {
		t.Errorf("missing handled_by edge; got %v", route.Relations)
	}
	if route.Props["framework"] != "aspnetcore" {
		t.Errorf("framework = %v, want aspnetcore", route.Props["framework"])
	}
}

// TestASPNet_PartialControllerKeepsItsTemplate covers the interaction with the
// partial-type merge: a controller split across files declares its [Route] in one
// half and its actions in the other.
func TestASPNet_PartialControllerKeepsItsTemplate(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Api/UsersController.Core.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

[Route("Users")]
public partial class UsersController : ControllerBase { }
`,
		"Api/UsersController.Actions.cs": `
using Microsoft.AspNetCore.Mvc;
namespace Acme.Api;

public partial class UsersController
{
    [HttpGet("{id}")]
    public IActionResult GetUser(int id) => Ok();
}
`,
	})
	assertRoutes(t, got, []string{"GET /Users/{id}"})
}

// TestASPNet_NonControllerVerbAttributeStillNeedsATemplate is the control against
// over-reach: a plain class that happens to carry a verb attribute produces no
// route unless a [Route] governs it, so an attribute of the same name on an
// unrelated type mints nothing.
func TestASPNet_NonControllerVerbAttributeStillNeedsATemplate(t *testing.T) {
	got, _ := routeSet(t, map[string]string{
		"Lib/Helper.cs": `
namespace Acme.Lib;

public class Helper
{
    [HttpGet("x")]
    public void NotAnEndpoint() { }
}
`,
	})
	if len(got) != 0 {
		t.Errorf("a class with no [Route] in its hierarchy should mint nothing, got %v", got)
	}
}
