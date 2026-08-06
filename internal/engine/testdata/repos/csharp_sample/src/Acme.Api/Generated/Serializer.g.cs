// A source-generator artifact checked in beside real sources. Its `.g.cs` suffix
// keeps it out of the graph entirely, and it is the only file in this directory —
// so the absence of a src/Acme.Api/Generated module from the golden is what proves
// the file produced nothing at all, rather than merely producing no symbols.
namespace Acme.Api.Generated
{
    public class GeneratedSerializer
    {
        public string Write(object value) => value.ToString();
    }
}
