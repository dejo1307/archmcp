using Microsoft.AspNetCore.Mvc;

namespace Acme.Api.Controllers;

// CONVENTIONAL ROUTING: derives from Controller, carries verb attributes, and has
// no [Route] anywhere in its hierarchy. ASP.NET resolves these from a template
// registered in Program.cs (`{controller}/{action}`), which this extractor does not
// read — so the real paths are /Account/Login and /Account/Logout, and neither is
// derivable from anything in this file.
//
// The absence of these two from the golden is the assertion. Composing from what IS
// visible gave every action the path "/", which is both wrong and — facts being
// name-keyed — collapsed them onto a single root node.
public class AccountController : Controller
{
    [HttpGet]
    public IActionResult Login() => View();

    [HttpPost]
    public IActionResult Logout() => View();
}
