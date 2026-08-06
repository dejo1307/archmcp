namespace Acme.Api.Apis;

// Minimal APIs: the path is split between a group builder held in a local
// variable and the registrations that use it, both in one method body.
public static class OrdersApi
{
    public static RouteGroupBuilder MapOrdersApiV1(this IEndpointRouteBuilder app)
    {
        // A fluent call after MapGroup must not hide the prefix.
        var api = app.MapGroup("api/orders").HasApiVersion(1.0);

        api.MapPut("/cancel", CancelOrderAsync);
        api.MapGet("{orderId:int}", GetOrderAsync);
        api.MapGet("/", GetOrdersByUserAsync);

        // A group built on a group composes both prefixes.
        var admin = api.MapGroup("admin");
        admin.MapDelete("/{orderId:int}", DeleteOrderAsync);

        // A lambda has no symbol to bind, so the route carries no handler rather
        // than a fabricated one.
        api.MapGet("/health", () => Results.Ok());

        // Takes no path: excluded because MapControllers is not a verb, without
        // needing a list of method names to skip.
        app.MapControllers();

        return api;
    }

    public static void CancelOrderAsync() { }
    public static void GetOrderAsync() { }
    public static void GetOrdersByUserAsync() { }
    public static void DeleteOrderAsync() { }
}
