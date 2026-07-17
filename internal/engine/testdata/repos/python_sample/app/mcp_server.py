"""MCP server handlers: registered by decorator, dispatched by the framework.

Pins v118: @mcp.tool / @mcp.resource / @mcp.prompt / @mcp.custom_route (and bare
re-exported wrappers) get the registration self-edge so the handlers are not
flagged dead; a function with only a wrapper decorator (@log_usage) stays
unreferenced — the preserved true positive.
"""

from app.db import get_user


@mcp.tool()
async def list_users_tool():
    """Tool handler — dispatched by the MCP server, no in-code caller."""
    return get_user(1)


@mcp.resource("instance://metadata")
def instance_metadata_resource():
    """Resource handler — dispatched by URI."""
    return "{}"


@mcp.custom_route("/health", methods=["GET"])
async def health_check(request):
    """Health route registered on the MCP server."""
    return "ok"


@prompt("quickstart")
async def quickstart_prompt(user_type="analyst"):
    """Bare re-exported wrapper form (superset_core-style)."""
    return "hi"


@log_usage(function_name="MCP legacy_tool", log_type="mcp_tool")
async def legacy_unregistered_tool(data):
    """Wrapper decorator only — NOT registered; must stay an orphan."""
    return data
