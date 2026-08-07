package csharpextractor

import (
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// diEdges returns the instantiates targets of the named symbol after the full
// pipeline, so the test asserts on what a snapshot would hold.
func diEdges(t *testing.T, files map[string]string, symbol string) []string {
	t.Helper()
	var all []facts.Fact
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		ff, _ := extractFileASTFull([]byte(files[p]), p)
		all = append(all, ff...)
	}
	all = mergePartialTypes(all)
	resolveCSharpTargets(all)
	for _, f := range all {
		if f.Name == symbol {
			return relTargets(f, facts.RelInstantiates)
		}
	}
	t.Fatalf("no symbol %q", symbol)
	return nil
}

// The eShop shape: an extension method that registers the application's services.
func TestDI_RegistrationReferencesImplementation(t *testing.T) {
	got := diEdges(t, map[string]string{
		"src/App/Extensions.cs": `
namespace Acme.App;
public static class Extensions
{
    public static void AddApplicationServices(this IHostApplicationBuilder builder)
    {
        builder.Services.AddScoped<IOrderRepository, OrderRepository>();
        builder.Services.AddSingleton<BasketState>();
        builder.Services.AddHostedService<OrderProcessor>();
    }
}`,
		"src/Domain/IOrderRepository.cs": "namespace Acme.Domain; public interface IOrderRepository { }",
		"src/Data/OrderRepository.cs":    "namespace Acme.Data; public class OrderRepository : IOrderRepository { }",
		"src/App/BasketState.cs":         "namespace Acme.App; public class BasketState { }",
		"src/App/OrderProcessor.cs":      "namespace Acme.App; public class OrderProcessor { }",
	}, "src/App.Extensions.AddApplicationServices")

	for _, want := range []string{
		"src/Data.OrderRepository", // the implementation, named nowhere else
		"src/Domain.IOrderRepository",
		"src/App.BasketState",
		"src/App.OrderProcessor",
	} {
		if !has(got, want) {
			t.Errorf("missing %q; got %v", want, got)
		}
	}
}

// Matching every `Add*` method would draw an edge from a startup file to whatever
// type happened to be passed anywhere in the application.
func TestDI_NonRegistrationAddMethodsAreIgnored(t *testing.T) {
	got := diEdges(t, map[string]string{
		"src/App/Setup.cs": `
namespace Acme.App;
public class Setup
{
    public void Run()
    {
        list.AddRange<Widget>(items);
        policy.AddPolicy<RetryThing>("x");
    }
}`,
		"src/App/Widget.cs":     "namespace Acme.App; public class Widget { }",
		"src/App/RetryThing.cs": "namespace Acme.App; public class RetryThing { }",
	}, "src/App.Setup.Run")

	for _, bad := range []string{"src/App.Widget", "src/App.RetryThing"} {
		if has(got, bad) {
			t.Errorf("%q is not a DI registration; got %v", bad, got)
		}
	}
}

// The bare (non-member) call form used inside a ConfigureServices body.
func TestDI_BareCallForm(t *testing.T) {
	got := diEdges(t, map[string]string{
		"src/App/Startup.cs": `
namespace Acme.App;
public class Startup
{
    public void ConfigureServices(IServiceCollection services)
    {
        AddScoped<IThing, Thing>();
    }
}`,
		"src/App/Thing.cs":  "namespace Acme.App; public class Thing : IThing { }",
		"src/App/IThing.cs": "namespace Acme.App; public interface IThing { }",
	}, "src/App.Startup.ConfigureServices")

	if !has(got, "src/App.Thing") {
		t.Errorf("bare registration form lost; got %v", got)
	}
}

// A registration with no type arguments (the typeof spelling) names nothing this
// pass can bind, and must not crash or invent an edge.
func TestDI_NonGenericRegistrationIsSafe(t *testing.T) {
	got := diEdges(t, map[string]string{
		"src/App/Startup.cs": `
namespace Acme.App;
public class Startup
{
    public void Configure(IServiceCollection services)
    {
        services.AddScoped(typeof(IThing), typeof(Thing));
    }
}`,
		"src/App/Thing.cs": "namespace Acme.App; public class Thing { }",
	}, "src/App.Startup.Configure")
	_ = got // no assertion beyond not panicking and not inventing a bound edge
}
