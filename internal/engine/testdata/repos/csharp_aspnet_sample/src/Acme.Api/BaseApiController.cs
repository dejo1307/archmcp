using Microsoft.AspNetCore.Mvc;

namespace Acme.Api;

// The shared base most controllers inherit from. Its [Route("[controller]")] is
// the template that governs AudioController and CatalogController below, neither
// of which declares one — the case a per-file composition cannot resolve, and the
// shape 40 of jellyfin's 64 controllers use.
[ApiController]
[Route("[controller]")]
public class BaseApiController : ControllerBase
{
}
