namespace Acme.Api.Apis;

// A library that mounts its whole surface at a CALLER-SUPPLIED prefix — the shape
// the MCP C# SDK uses. The real paths are whatever the host application passes as
// `pattern`, so nothing here is resolvable.
//
// The absence of these two from the golden is the assertion. Publishing the
// registration paths alone would claim endpoints this library does not serve, and
// since the first one registers at "", it would land on "/" — the same phantom-root
// collapse conventional routing produced.
public static class McpEndpoints
{
    public static IEndpointConventionBuilder MapMcp(this IEndpointRouteBuilder endpoints, string pattern = "")
    {
        var mcpGroup = endpoints.MapGroup(pattern);
        var streamable = mcpGroup.MapGroup("");

        streamable.MapPost("", HandlePostAsync);
        streamable.MapGet("/sse", HandleSseAsync);

        return mcpGroup;
    }

    public static void HandlePostAsync() { }
    public static void HandleSseAsync() { }
}
