using Microsoft.AspNetCore.Mvc;

namespace Acme.Api.Controllers;

// No [Route] of its own: the template comes from BaseApiController, and
// [controller] resolves to this class's own name minus the suffix — so one shared
// base attribute gives each controller a distinct path.
public class AudioController : BaseApiController
{
    // The named argument is a route NAME, not a template. Reading it would serve
    // this endpoint at /Audio/GetAudioStream.
    [HttpGet("{itemId}/stream", Name = "GetAudioStream")]
    public IActionResult GetAudioStream(string itemId) => Ok();

    // A verb attribute with no template at all serves the controller path itself.
    [HttpHead(Name = "HeadAudio")]
    public IActionResult HeadAudio() => Ok();
}
