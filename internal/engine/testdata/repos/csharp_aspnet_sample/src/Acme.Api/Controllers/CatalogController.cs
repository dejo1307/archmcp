using Microsoft.AspNetCore.Mvc;

namespace Acme.Api.Controllers;

[Route("api/catalog")]
public class CatalogController : BaseApiController
{
    [HttpGet("items")]
    public IActionResult ListItems() => Ok();

    [HttpPost]
    [Route("items")]
    public IActionResult CreateItem() => Ok();

    // Absolute: a leading "/" replaces the controller template rather than
    // extending it, so this is served at the root and not at /api/catalog/health.
    [HttpGet("/health")]
    public IActionResult Health() => Ok();
}
