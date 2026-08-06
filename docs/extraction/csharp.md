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
- **A call on an arbitrary receiver draws no edge.** `foo.Handle()` records its name for
  the in-loop metrics only. The receiver's static type is not tracked, and `Execute`,
  `Handle` and `Dispose` each name hundreds of unrelated methods in a large solution.
  Same-type calls (`Foo()`, `this.Foo()`) and static calls (`Type.Method()`, when `Type`
  is a declared type) do draw edges.
- **Extension methods record their receiver but bind no call site.** A
  `static bool IsOpen(this Order o)` carries `extension_method` and `extends_type=Order`;
  an `order.IsOpen()` call site needs the receiver's static type to bind, so it does not.
- **Same-named types in different directories are never merged**, including partial ones.
  The name is directory-anchored, so they are different nodes by construction — the same
  limitation the C/C++ header/source merge has.
- **Minimal APIs, EF Core storage and outbound HTTP clients are not extracted yet.**
  `app.MapGet("/x", …)` and `MapGroup` prefixes mint no routes, so a minimal-API service
  contributes symbols and call edges but no endpoints; there are no `storage` facts and no
  client-role routes for the cross-repo linker to match.
- **Conventionally-routed controller actions mint no route** — see above.
- **Test projects are indexed as production code.** The default test globs carry no C#
  patterns, so a `*Tests.cs` class becomes an ordinary symbol, and there is no
  `TestRefExtractor` for C# — meaning a production symbol exercised only by its tests
  reads as unreferenced.
