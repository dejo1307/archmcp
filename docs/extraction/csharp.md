# C# — what enola extracts

Parsed with tree-sitter. Detected by an MSBuild solution or project file (`.sln`,
`.slnx`, `.csproj`) or by any `.cs` source within four directory levels.

Fixture: [`csharp_sample`](../../internal/engine/testdata/repos/csharp_sample/) ·
Unit coverage in
[`csharpextractor/csharp_test.go`](../../internal/extractors/csharpextractor/csharp_test.go)

> **Check your `mcp-arch.yaml` before you conclude C# is unsupported.** A config file's
> `extractors:` list *replaces* the built-in default rather than merging with it, so a
> config written before an extractor existed silently disables it. A repository indexed
> with a stale list reports zero C# facts and says nothing about why. Either add `csharp`
> to the list or delete the key to inherit the default.

## At a glance

| You write | enola stores | Kind |
|---|---|---|
| a directory of `.cs` files | one module carrying its `project` (the owning `.csproj`) | `module` |
| `class` / `interface` / `struct` / `record` / `enum` / `delegate` | a symbol with `symbol_kind`, `namespace` and `fqn` | `symbol` |
| a method, constructor, operator | a symbol with `receiver` | `symbol` |
| a **public or protected** field or property | a symbol | `symbol` |
| a private field or property | *nothing* — see [What is deliberately not extracted](#what-is-deliberately-not-extracted) | — |
| `partial class Foo` in several files | **one** symbol carrying the union of every half's edges | `symbol` |
| `using Acme.Data;` | a dependency tagged `internal` / `external` / `stdlib` | `dependency` |
| `: BaseClass, IThing` | one `implements` relation per entry | relation |
| a sole constructor's parameters | `injects` relations | relation |
| `new Order()` | an `instantiates` relation | relation |
| `Lookup(id)` inside the declaring type | a `calls` relation | relation |
| `[Route("x")]` + `[HttpGet("y")]` | a server route at `/x/y`, `handled_by` its action | `route` |
| `app.MapGroup("api/x")` + `.MapGet("/y", H)` | a server route at `/api/x/y`, `handled_by` `H` | `route` |

## Names, namespaces and the two spellings

enola names a fact by its **directory**, as it does in every language — `<dir>.<Type>`,
`<dir>.<Outer>.<Inner>`, `<dir>.<Type>.<member>`. A C# namespace routinely disagrees with
the file system, so it travels as a prop rather than as part of the name:

```csharp
// src/Acme.Api/OrderService.cs
namespace Acme.Api;                 // file-scoped

public sealed class OrderService : IDisposable { … }
```

```
symbol  src/Acme.Api.OrderService   props: symbol_kind=class, exported=true, sealed=true,
                                          namespace=Acme.Api, fqn=Acme.Api.OrderService
                                    --implements--> IDisposable
```

Both spellings — the file-scoped `namespace A.B;` and the block `namespace A.B { … }` —
produce the same `namespace` prop. That matters because resolution keys on it.

## Partial types are one type

C# lets a type be split across any number of files, and real codebases lean on it hard:
7,740 files in the benchmark corpus declare one. Each half is its own parse tree, so
without a merge each becomes its own fact under the same name — inflating the symbol
count, scattering the type's edges across facts that each look thinly connected, and
handing every consumer an arbitrary one of the halves as the type's `file:line`.

```csharp
// src/Acme.Domain/Widget.Core.cs        // src/Acme.Domain/Widget.Extra.cs
public partial class Widget              public partial class Widget : IOrderRepository
{                                        {
    public void Reset() => Describe();       public void Describe() { }
}                                        }
```

```
symbol  src/Acme.Domain.Widget          props: partial=true, partial_declarations=2
                                        --implements--> src/Acme.Domain.IOrderRepository
symbol  src/Acme.Domain.Widget.Reset    --calls--> src/Acme.Domain.Widget.Describe
symbol  src/Acme.Domain.Widget.Describe
```

The surviving fact is the earliest by `(file, line)`, so the merge is a function of the
fact set rather than of the order the files happened to be walked.

`Reset --calls--> Describe` is the part a per-file index cannot produce: the two members
are in different trees. Inside a partial type a bare call is therefore offered
speculatively against **that type's own member namespace**, and dropped if no half
declares it. The candidate is scoped to one type rather than to the repository, so the
worst case is a base-class call that draws no edge — never an edge to an unrelated type
that happens to share a method name.

## Resolution — why it is a whole-repository pass

Java can resolve a bare type reference from the file's own imports, because a Java
`import` names a type. A C# `using` opens a **namespace**, so the file's directives say
which namespaces are in scope and nothing about which one declares `StringBuilder`.

Bare names are therefore bound against a project-wide index, in this order:

1. a declaration in the reference's **own namespace** — C#'s own rule, not a tie-break;
2. a **unique** simple name anywhere in the repository;
3. otherwise **nothing**, and the target is left as written.

Step 3 is the load-bearing one. On a corpus this size `Options`, `Message` and `Result`
each name dozens of unrelated types, and picking one would manufacture an edge between
subsystems that have never heard of each other.

Measured on [dotnet/eShop](https://github.com/dotnet/eShop), the unresolved remainder is
almost entirely BCL and framework surface — `Task.FromResult`, `Guid.NewGuid`,
`JsonSerializer.Deserialize`, `IRequestHandler`, `Migration` — which is what "left as
written" is supposed to look like.

An **alias** (`using Repo = Acme.Data.OrderRepository;`) and a `using static` are the two
forms that *do* name a type; both resolve through the type index, and the alias is
substituted at every reference in that file.

## Dependency injection

```csharp
public OrderService(IOrderRepository repo, HttpClient http) { … }
```

```
symbol  src/Acme.Api.OrderService  --injects--> src/Acme.Domain.IOrderRepository
                                   --injects--> HttpClient
```

Injection is read from a **sole** constructor, or from a C# 12 primary constructor
(`class Foo(IBar bar)`). A type with several constructors has convenience overloads and
no single injection point, so none of them produce edges — the same restriction the Java
extractor applies, for the same reason.

For a `record`, the primary constructor's parameters are additionally emitted as the
public properties the compiler generates from them.

## Routes — ASP.NET Core attribute routing

An action's URL is assembled from two attributes in two places, and frequently from
a third the file cannot see:

```csharp
// src/Acme.Api/BaseApiController.cs          // src/Acme.Api/Controllers/AudioController.cs
[ApiController]                                public class AudioController : BaseApiController
[Route("[controller]")]                        {
public class BaseApiController : ControllerBase    [HttpGet("{itemId}/stream", Name = "GetAudioStream")]
{ }                                                public IActionResult GetAudioStream(string itemId) => Ok();
                                               }
```

```
route  /Audio/{itemId}/stream   props: method=GET, framework=aspnetcore,
                                       handler=src/Acme.Api/Controllers.AudioController.GetAudioStream
                                --handled_by--> src/Acme.Api/Controllers.AudioController.GetAudioStream
```

Most controllers declare no `[Route]` of their own — 40 of jellyfin's 64 inherit
one from a shared base — so composition runs over the whole fact set rather than
per file, walking the resolved inheritance edges to find the nearest template.
`[controller]` then resolves to each subclass's own name minus the `Controller`
suffix, which is how one shared base attribute gives 40 controllers 40 distinct
paths. `[action]` resolves to the method name.

A method template beginning with `/` or `~/` is **absolute**: it replaces the
controller's template rather than extending it. `Name = "…"` is a route's display
name, not its path — only an attribute's first argument is a template, and only
when it is a string literal.

Measured on [jellyfin](https://github.com/jellyfin/jellyfin): 422 routes, exactly
one per `[HttpGet]`/`[HttpPost]`/`[HttpDelete]`/`[HttpHead]` attribute in the
source, with all 388 distinct handlers resolving to real symbol facts.

### Conventional routing produces nothing

A controller with verb attributes but **no `[Route]` anywhere in its hierarchy** is
using *conventional* routing, where the URL comes from a template registered in
`Program.cs`:

```csharp
public class AccountController : Controller     // really served at /Account/Login
{
    [HttpGet]  public IActionResult Login()  => View();
    [HttpPost] public IActionResult Logout() => View();
}
```

That template is not read here, so the path is genuinely unknown and **no route is
emitted**. Composing from what is visible is what this used to do, and it was wrong
twice over: a bare `[HttpGet]` carries no template, so every action came out as
`/` — the wrong path, and, because facts are name-keyed, several actions collapsing
onto a single root node. On eShop's Identity.API that turned 14 real endpoints into
a handful of phantom roots.

Guessing `/{controller}/{action}` instead would be inventing a template that lives
in a file the extractor did not parse and can be anything. The count of skipped
actions is logged, so the gap is visible rather than silent.

Note that `[Route("")]` is **not** the same as declaring no `[Route]`: an empty
template is a real one, and its actions are served at the method template alone.

## Routes — minimal APIs

A minimal-API endpoint is registered by a call rather than declared by an
attribute, and its path is split between a group builder held in a local variable
and the registrations that use it:

```csharp
public static RouteGroupBuilder MapOrdersApiV1(this IEndpointRouteBuilder app)
{
    var api = app.MapGroup("api/orders").HasApiVersion(1.0);

    api.MapPut("/cancel", CancelOrderAsync);
    api.MapGet("{orderId:int}", GetOrderAsync);

    var admin = api.MapGroup("admin");
    admin.MapDelete("/{orderId:int}", DeleteOrderAsync);
}
```

```
route  /api/orders/cancel                 props: method=PUT,    handler=…OrdersApi.CancelOrderAsync
route  /api/orders/{orderId:int}          props: method=GET,    handler=…OrdersApi.GetOrderAsync
route  /api/orders/admin/{orderId:int}    props: method=DELETE, handler=…OrdersApi.DeleteOrderAsync
```

Groups nest, and a fluent call after `MapGroup` does not hide the prefix. The
analysis is scoped to **one method body** — which is what makes it resolvable
without a whole-program pass, and equally what bounds it: a group passed across a
function boundary is not followed. A group variable in one method never resolves a
registration in another.

A **method-group** handler (`GetOrderAsync`) binds to the sibling method it names;
a **lambda** has no symbol to point at, so the route carries no `handler` at all
rather than a fabricated one.

A **string-literal first argument** is the discriminator. That is what keeps
`MapControllers()`, `MapRazorPages()` and `MapHub<T>()` — none of which take a
path — out of the route set, without a list of method names to exclude, and it
also excludes a computed path (`app.MapGet(BuildPath(), H)`) that cannot be read.

### An unresolvable group prefix drops its routes

A library may mount its whole surface at a prefix its caller supplies:

```csharp
public static IEndpointConventionBuilder MapMcp(this IEndpointRouteBuilder endpoints,
                                                string pattern = "")
{
    var mcpGroup = endpoints.MapGroup(pattern);   // ← not a literal
    var streamable = mcpGroup.MapGroup("");

    streamable.MapPost("", HandlePostAsync);
    streamable.MapGet("/sse", HandleSseAsync);
}
```

The real paths are whatever the host application passes, so the group is marked
**unknown** and its registrations emit nothing. Publishing the registration path
alone would claim endpoints the library does not serve — and since the first
registers at `""`, it would land on `/`, recreating the phantom-root collapse
[conventional routing](#conventional-routing-produces-nothing) produced.

This is the one place the C# extractor is stricter than the Go extractor, which
keeps a bare path when a mount is unresolved. A bare Go path is still a
recognisable suffix; a bare path here is frequently the root itself.

Measured on [eShop](https://github.com/dotnet/eShop): 30 routes, all with composed
prefixes; the MCP SDK's caller-mounted file contributes 0.

## Dead code, and why the bare edge matters

A DI-wired .NET application calls almost everything through an interface. With a
same-type-only call graph, every method reached that way has no inbound edge at
all — so it reads as dead, and so does the interface member it implements.

Measured on jellyfin, before and after the bare member-call edge:

| | methods | enums | constants | all symbols |
|---|---:|---:|---:|---:|
| same-type edges only | 2,478 | 105 | 802 | 7,485 / 15,665 |
| + bare member-call edges | 988 | 105 | 802 | 5,982 |
| + qualified references | **905** | **8** | **179** | **4,576** |

A second gap sat beside the first: only *invocations* emitted edges, so a plain
`VideoRange.HDR` produced nothing at all and 84 of 137 enums read as isolated
while that exact expression appeared at 25 call sites. A qualified `Type.Member`
reference now emits an edge to the member **and** to the type — the member edge
alone vouches for `HDR` and says nothing about `VideoRange`, which left every
class reached only through static calls unreferenced too.

The type edge is added once the receiver has *provably* resolved to a declared
type, not at the call site: a bare type name emitted there would be
indistinguishable from the bare method name a member call produces, and
`foo.Order()` would bind to a class named `Order`. Receivers are gated on
PascalCase, C#'s type-naming convention, so `order.Id` and `list.Count` emit
nothing.

2,909 symbols rescued in total, at +49% `calls` edges.

What remains flagged is largely **not** a false positive, and splits in two.
jellyfin discovers its providers by reflection (`GetExports<IImageProvider>()`),
so a class like `ComicImageProvider` genuinely has no static reference anywhere in
the tree. And a type used only in *type position* — `ViewType Kind { get; set; }`,
`List<ViewType>` — is not edge-tracked in any language enola supports, which is
what the last 8 enum findings are. Both are limitations the orphan detector's own
caveat already describes.

Note also that `find_orphans` reports **no** high-confidence findings for C#. Its
confidence model rates plain `function` calls as reliably tracked, and C# has
almost no free functions — everything is a method, which it rates `low`. Treat C#
orphan output as leads to verify, not a cleanup list.

## Complexity and I/O

Like the other AST extractors, each member body is walked once for `cyclomatic`,
`loop_depth`, `loop_count`, `calls_in_loop`, `recursive_self`, plus `scaling_loop_depth`
and `calls_in_scaling_loop`. `for`/`foreach`/`while`/`do` and LINQ iterator lambdas
(`Select`, `Where`, `ForEach`, `Aggregate`, …) count as loops; a literal-bounded `for
(var i = 0; i < 3; i++)` or a `foreach` over a collection literal raises `loop_depth` but
adds **no** scaling depth, so a fixed-size loop never inflates a genuine O(n) into a
false O(n²).

A member that calls a network, file or database primitive — `HttpClient`'s verbs,
`File.*`, ADO.NET `Execute*`, EF Core `SaveChangesAsync`/`ToListAsync` — is tagged
`io_direct`, and a post-pass propagates that transitively over the call graph into
`performs_io`. That is what lets a per-iteration call to a wrapper read as an N+1 rather
than as ordinary work.

`&&`, `||` and `??` each add a decision point. `?.` does not, matching the other
extractors' treatment of optional chaining.

## Test projects

Three directory patterns keep test code out of the production graph:

```
**/tests/**/*.cs      **/test/**/*.cs      **/*.Tests/**/*.cs
```

`*.Tests/` is not redundant with `tests/`: the dominant .NET solution layout puts a
test project in `MyApp.Tests/` *beside* `MyApp/` rather than under a `tests/`
directory, and 303 files in the benchmark corpus are reachable only that way.

**There is deliberately no filename pattern**, and that is measured rather than
cautious. Across 40,014 `.cs` files in the corpus, `**/*Tests.cs` would have added
exactly **one** file the directory rules miss — `emitUnitTests.cs`, a generator
that *emits* unit tests — while `**/*Test.cs` would have deleted
`XmlQualifiedNameTest` (a real XPath node-test type in `System.Private.Xml`) and
the Azure Load Testing tool's `LoadTest/Test.cs` model. That is the same hazard
Ruby's `_test.rb` suffix presents, and it resolves the same way: the directory is
the signal, the filename is not.

What this changes, measured:

| Repo | facts before | after | routes before | after |
|---|---:|---:|---:|---:|
| dotnet/runtime | 881,581 | **372,992** | 23 | 17 |
| csharp-sdk | 9,156 | 4,122 | **31** | **0** |
| mcp | 32,844 | 21,228 | 0 | 0 |
| jellyfin | 30,012 | 26,882 | 422 | 420 |
| eShop | 3,080 | 3,156 | 30 | 30 |

csharp-sdk is the case that motivated it: every one of the 31 endpoints enola
reported for it came from `tests/`, and the library serves no HTTP surface of its
own. jellyfin keeps its real 420-endpoint API and loses two test-only routes.

*Limits:* a test tree under a name these patterns do not know — dotnet/runtime's
`src/mono/wasm/testassets/` — is still indexed, and a test-support library that
happens to ship (`Microsoft.Extensions.DependencyInjection.Specification.Tests`) is
excluded along with real tests.

### …but what a test *references* is kept

Excluding a test project has a cost: a production symbol whose only caller is a
test then has no inbound edge at all, and reads as dead. So test files are parsed
once more, for the sole purpose of capturing their outbound references:

```csharp
// tests/Acme.Api.Tests/OrderServiceTests.cs
var svc = new OrderService(repo);
var money = svc.Summarise(orders);
Assert.Equal("EUR", money.Currency);
```

```
test_ref  tests/Acme.Api.Tests/OrderServiceTests.cs
            --calls--> OrderService     --calls--> Summarise
            --calls--> svc.Summarise    --calls--> money.Currency
```

One `test_ref` fact per file, carrying **only** `calls` relations — no symbols, no
modules, no routes. So a test class still never becomes a dead-code candidate, and
no symbol/module/route explainer sees test code at all.

Targets are emitted **as written** rather than resolved to canonical fact names.
The production symbol index is built inside `Extract` and is not available to this
pass, and the consumer does not need it: the orphan detector matches a target both
exactly and by its last dot-separated segment, which is why the Ruby extractor
emits the same `Const.method` shape.

Assertion and mocking receivers (`Assert`, `Mock`, `It`, `Times`, …) are dropped,
**including the bare method name**. Filtering only `Assert.Equal` let `Equal`
through — and production code really does declare `Equal`, so the harness would
have vouched for a symbol no test exercises, suppressing a genuine dead-code
finding.

| Repo | `test_ref` facts | distinct targets | matching a production symbol |
|---|---:|---:|---:|
| jellyfin | 210 | 2,796 | 1,974 (71%) |
| csharp-sdk | 261 | 4,032 | 2,191 (54%) |
| eShop | 34 | 330 | 170 (52%) |

The remainder are BCL and framework calls (`Activator.CreateInstance`,
`AddLogging`, `AddDays`) that match nothing and cost nothing.

*Cost:* the pass parses every test file. On dotnet/runtime — 16,974 of them — it
adds roughly 23s to a 20s snapshot.

## Generated code

`.g.cs`, `.generated.cs`, `.Designer.cs`, `AssemblyInfo.cs` and anything carrying an
`<auto-generated>` header produce **nothing** — not an empty symbol set, nothing at all,
so a directory holding only generated files emits no module either. The count of skipped
files is logged rather than silently absorbed.

Generated code is a projection of something else — a `.resx`, a `.xaml`, an interop
definition — and indexing it attributes thousands of symbols and a large complexity
surface to whichever directory the generator wrote into. Those findings are also
unactionable: the fix lives in the generator, and the generator is not in the graph.

`obj/` and `bin/Debug|Release/` are excluded by the default ignore globs for the same
reason.

## What is deliberately not extracted

- **Private fields and properties are not symbols.** They are a type's internal state,
  and on a BCL-scale repository emitting them would multiply the symbol count without
  adding a node anyone traverses. Their initializers and accessor bodies are still walked,
  so the call edges inside them survive, attributed to the enclosing type.
- **A call on an arbitrary receiver draws a BARE edge, never a bound one.** `foo.Handle()`
  emits `calls -> Handle` — the method name alone — because the receiver's static type is
  not tracked. It is kept bare even when exactly one type declares that name: jellyfin has
  a single `Match` method (`FileStackRule.Match`) while four of its five variable-receiver
  `.Match(` call sites are `Regex`, so binding on uniqueness would point them into the
  video-stack parser, and a wrong edge feeds `impact_analysis` and `find_path`. Nothing is
  lost — the dead-code detector matches by short name, and the rescue measures identical
  either way. A name no type in the repository declares is dropped, so `.ToString()` and
  `.Add()` do not become edges to symbols that do not exist. Same-type calls (`Foo()`,
  `this.Foo()`) and static calls (`Type.Method()`) still resolve to canonical targets.
- **Extension methods record their receiver but bind no call site.** A
  `static bool IsOpen(this Order o)` carries `extension_method` and `extends_type=Order`;
  an `order.IsOpen()` call site needs the receiver's static type to bind, so it does not.
- **Same-named types in different directories are never merged**, including partial ones.
  The name is directory-anchored, so they are different nodes by construction — the same
  limitation the C/C++ header/source merge has.
- **EF Core storage and outbound HTTP clients are not extracted yet.** There are no
  `storage` facts and no client-role routes for the cross-repo linker to match, so a C#
  service links to its neighbours only as a route *provider*.
- **A minimal-API group is not followed across a function boundary**, and a group whose
  prefix is not a string literal drops its routes entirely — see above.
- **Conventionally-routed controller actions mint no route** — see above.
- **A test's references are captured by name, not resolved.** A `test_ref` target
  matches a production symbol by its last segment, so an unrelated symbol sharing a
  method name is credited with test coverage it does not have. That biases toward a
  *missed* dead-code lead rather than a false accusation, which is the safe direction.
