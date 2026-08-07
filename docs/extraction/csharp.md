# C# — what enola extracts

Sources parsed with tree-sitter; MSBuild project and solution files parsed as XML.
Detected by a solution or project file of any .NET language (`.sln`, `.slnx`,
`.csproj`, `.fsproj`, `.vbproj`) or by any `.cs` source within four directory
levels.

> **"C#", not "all of .NET" — but the project system is the whole platform's.**
> Source reading is C#-only. The MSBuild layer beneath it is not: every project
> file is parsed whatever language it compiles, so the assembly graph — which
> project references which — is complete even where the sources are not.
>
> | Sources not extracted | Present in the benchmark corpus |
> |---|---|
> | F# (`.fs`) | 5,539 files |
> | VB.NET (`.vb`) | 3,784 files |
>
> Razor (`.razor`, `.cshtml`) and XAML (`.xaml`, `.axaml`) **are** read — see
> [Razor](#razor--blazor-components-and-mvc-views) and [XAML](#xaml--wpf-winui-maui-and-avalonia).
>
> A mixed solution therefore has all of its projects and all of its
> `ProjectReference` edges, and symbols for its C# and Razor halves.
>
> Reading `.fsproj` and `.vbproj` is also what keeps a claimed repository from
> being an empty one. `giraffe-fsharp/Giraffe` ships `Giraffe.slnx`, seven
> `.fsproj` and no `.cs` at all: detection matched the solution, this extractor
> claimed the repository, emitted **zero facts** and reported a successful
> snapshot — indistinguishable from an empty repo. It now emits the project graph,
> and logs that the sources went unread.

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
| a `.csproj` / `.fsproj` / `.vbproj` | that directory as a module carrying `project`, `target_framework`, `output_type`, `solution` | `module` |
| `<ProjectReference Include="..\B\B.csproj" />` | a `depends_on` relation to B's directory | relation |
| `<PackageReference Include="Serilog" />` | an external dependency tagged `package_manager=nuget` | `dependency` |
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

## The project system — where .NET's real dependency edges are

Everywhere else in this extractor a fact is named after the **directory** it lives
in. That is the right unit for a symbol and the wrong one for a dependency: .NET's
dependency unit is the **assembly**, it is declared in a project file, and a
`ProjectReference` between two of them is the one edge in a .NET solution that the
build system itself enforces.

```xml
<!-- src/Acme.Api/Acme.Api.csproj -->
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net9.0</TargetFramework>
    <OutputType>Exe</OutputType>
  </PropertyGroup>
  <ItemGroup>
    <ProjectReference Include="..\Acme.Domain\Acme.Domain.csproj" />
    <PackageReference Include="Serilog" Version="4.2.0" />
  </ItemGroup>
</Project>
```

```
module  src/Acme.Api   props: project=Acme.Api, msbuild=true, target_framework=net9.0,
                              output_type=Exe, language=csharp
                       --depends_on--> src/Acme.Domain

dependency  src/Acme.Api -> Serilog   props: package_manager=nuget, version=4.2.0,
                                             source=external
```

A project root that also holds sources produces **one** module fact carrying both
halves, not two. Facts are name-keyed so the graph would merge them anyway, but
both would reach `facts.jsonl` and be counted twice in `fact_count` — a published
benchmark number.

Project files are read with a **token walk over `Name.Local`** rather than
unmarshalled into a struct, because the two formats disagree about namespaces: an
SDK-style project has none, a legacy one puts every element in the 2003 MSBuild
namespace. `Include` paths use **backslashes on every host platform**, so they are
normalised before any path operation — a plain `filepath.Dir` on macOS leaves them
embedded and the edge points nowhere.

**Conditions are ignored.** A `ProjectReference` guarded by
`Condition="'$(TargetFramework)' == 'net48'"` is a real reference under some build
configuration, and enola has no configuration to evaluate against. Taking every
branch over-approximates the edge set, which is the safe direction: a missing edge
hides a real dependency, an extra one describes a build that genuinely exists. The
same target reached twice still yields one edge.

A **solution** contributes grouping only — a `solution` prop naming which
assemblies ship together — and never an edge. Two projects in one solution have no
dependency by virtue of that alone, and drawing one would connect every project in
a 148-project monorepo to every other. Both formats are read: `.slnx` as XML,
`.sln` through its bespoke text format.

`IsTestProject`, or a `Microsoft.NET.Test.Sdk` package reference, sets
`module_role=test` and **outranks the path heuristic** — a test project named
`Acme.Verification/` under `src/` is a test project whatever its path suggests.

A reference escaping the repository root is dropped: it names a project that
cannot be in this graph.

## Razor — Blazor components and MVC views

**This is not a parse.** A Razor file interleaves HTML, C# and a transition syntax
that has no standalone grammar; the real one lives in the Razor compiler, which
generates a C# class. What runs here instead finds the regions where C# appears
and harvests the **names referenced** there. That is deliberately less than a
parse, and it is enough for the job it exists to do.

The job, measured before it existed: MudBlazor reported **5,749 orphans out of
9,287 symbols — 62% of a maintained component library** — because
`MudAlert.razor.cs` declares `OnClickHandler` and only `MudAlert.razor` calls it.

### A component and its code-behind are one type

```razor
@* src/MudBlazor/Components/Alert/MudAlert.razor *@
@namespace MudBlazor
@inherits MudComponentBase
@inject InternalMudLocalizer Localizer

<div class="@Classname" @onclick="OnClickHandler">
    @if (ShowCloseIcon) { <MudIconButton @onclick="OnCloseIconClickAsync" /> }
</div>
```

```
symbol  src/MudBlazor/Components/Alert.MudAlert
          props: symbol_kind=class, razor_component=true, framework=blazor,
                 partial=true, namespace=MudBlazor
          --implements--> MudComponentBase      (@inherits)
          --injects-----> InternalMudLocalizer  (@inject)
          --instantiates-> MudIconButton        (a PascalCase tag)
          --calls-------> Classname, OnClickHandler, ShowCloseIcon,
                          OnCloseIconClickAsync
```

No special handling makes this converge with `MudAlert.razor.cs`: both name the
same directory-anchored symbol and both carry `partial`, so the ordinary
[partial-type merge](#partial-types-are-one-type) unifies them. The component is
marked partial **even when no code-behind exists**, so the merge never depends on
whether one happens to be present.

`symbol_kind` is `class`, not `component`, and that is load-bearing rather than
pedantic. `symbol_kind` is what puts a name into the type index that resolves bare
type references, and `component` is not a type kind. Emitting it cost a real edge:
the `.razor` half merged over the `.razor.cs` half, carried the unrecognised kind
with it, and `DialogService.ShowCoreAsync --calls--> MudDialogContainer`
disappeared — turning live code into apparent dead code. The component-ness
travels as `razor_component` instead.

### Tag helpers, which carry no `@` at all

```html
<input asp-for="DisplayMenuFilter" class="form-check-input" type="checkbox" />
```

Every one of OrchardCore's view-model false positives was reached this way. An
MVC or Razor Pages view binds to its model through the `asp-for` family —
`asp-for`, `asp-validation-for`, `asp-validation-class-for`, `asp-items` — which
is plain HTML attribute syntax with **no Razor transition**. A scanner that
follows only `@` finds none of them.

A `.cshtml` view emits a `file_ref`, not a symbol: MVC and Razor Pages generate a
class nothing references by name, so a view can never itself become a dead-code
candidate, while still vouching for the members it binds.

```
file_ref  …/Views/AdminSettings.Edit.cshtml
            --calls--> AdminSettingsViewModel   (@model, reduced to its last segment)
            --calls--> DisplayMenuFilter, DisplayNewMenu, DisplayThemeToggler
```

### `@code` blocks are real C#

An `@code` / `@functions` body is handed to the ordinary C# walker inside a
synthetic compilation unit. The wrapper occupies exactly **one line** and is
followed by enough blank lines to put the body back on the line it occupies in
the `.razor` file, so every symbol's reported line is the real one rather than an
offset into a synthetic buffer.

### String literals, and the holes in them

Literal contents are blanked before names are harvested — `@T["Enable Admin Menu
filter"]` otherwise contributed `Enable`, `Admin`, `Menu` and `filter`. A
fabricated reference is worse than a missing one: it vouches for a symbol nothing
uses and *suppresses* a genuine dead-code finding.

**Interpolation holes survive.** `$"{Section.ToStringFast(true)}/{Previous.Link}"`
is a string whose braces hold real code, and it is how Blazor builds most of its
hrefs; blanking it wholesale would lose exactly the references worth having.

### `@page` is a UI route, never a server route

```
route  /counter/{id:int}   props: method=GET, type=page, framework=blazor
                           --handled_by--> src/App/Pages.Counter
```

A Blazor or Razor Pages URL is one the **browser** navigates to, not an HTTP
contract served to other services. Typed as a server route it would become a
cross-repo match candidate and an "unused route no client calls" finding; `type=page`
is the existing marker the linker already excludes for that reason.

### What this changes, measured

| Repo | orphans before | after | rescued | regressed |
|---|---:|---:|---:|---:|
| OrchardCore | 8,382 | **6,497** | 1,885 | **0** |
| MudBlazor | 19,442 | 21,437 | 721 | 2 |

Counted over the symbols present in *both* snapshots, because the change also
**adds** symbols (`@code` members, component types) and every added symbol is a
new orphan candidate — which is why MudBlazor's total rises while the fix works.
Its rescue rate is the lower one because its orphan set is dominated by 10,629
constants and 4,010 variables that no markup references.

MudBlazor's two "regressions" are not regressions: both were a *test* file's
namespace import (`using MudBlazor.UnitTests.TestComponents.DropZone`) vouching
for an unrelated production nested class by short-name collision. Losing that
un-suppresses a genuine finding.

*Cost:* +100ms on MudBlazor's 1,987 `.razor`, +21ms on OrchardCore's 1,610
`.cshtml`.

*Limits:* `_Imports.razor`, `_ViewImports.cshtml` and `_ViewStart.cshtml` declare
no component and emit nothing. A Razor Pages route that comes from the `Pages/`
directory convention rather than an explicit `@page` template is not derived. A
reference is harvested by NAME, so an unrelated symbol sharing it is credited —
the same bias, in the same safe direction, as `test_ref`.

## XAML — WPF, WinUI, MAUI and Avalonia

Unlike Razor, XAML *is* XML, so this is a real token walk rather than a scan. It
looks for the three ways a view reaches into code:

```xml
<Page x:Class="Files.App.Views.SplashScreenPage"
      xmlns:conv="using:Files.App.Converters">
    <Image ImageFailed="Image_ImageFailed" />
    <TextBlock Text="{x:Bind BranchLabel, Mode=OneTime}" />
    <Button Click="OnGoClicked"
            IsEnabled="{Binding Path=CanNavigate,
                        Converter={StaticResource BoolNegationConverter}}" />
    <conv:StatusCenterItem />
</Page>
```

```
symbol  src/Files.App/Views.SplashScreenPage
          props: symbol_kind=class, xaml_view=true, framework=xaml, partial=true,
                 fqn=Files.App.Views.SplashScreenPage, namespace=Files.App.Views
          --instantiates-> StatusCenterItem
          --calls-------> Image_ImageFailed, OnGoClicked, BranchLabel,
                          CanNavigate, BoolNegationConverter
```

`x:Class` makes the document one half of a partial class, so it merges with
`SplashScreenPage.xaml.cs` through the same partial-type merge a `.razor`
component uses — and for the same reason `symbol_kind` is `class`, not a
XAML-specific kind: that prop is what puts a name into the type index.

A document with **no `x:Class`** — a ResourceDictionary, a style file — has no
class to attach to and emits a `file_ref` rather than inventing one.

### Namespaces are matched by URI, not by prefix

`encoding/xml` resolves a prefix to its URI before reporting a name, so
`<conv:StatusCenterItem>` arrives with `Space="using:Files.App.Converters"`. A
tag is an instantiation when its namespace URI is a `clr-namespace:` or `using:`
one — a type declared in this solution. Framework controls (`Grid`, `TextBlock`,
`Button`) live in the schema-URL namespace and are not repository symbols.

### Why handlers are gated rather than harvested

`Click="OnSave"` names a method; `Stretch="None"` and `FontWeight="SemiBold"` do
not, and they are the same syntax. A bare-identifier attribute value is far more
often an enum member than a handler, so a value is taken as a method only when
the attribute is a known event **or** the value follows one of the two dominant
handler conventions (`OnSave`, `Image_ImageFailed`). Harvesting every bare value
would vouch for symbols nothing uses, which *suppresses* genuine dead-code
findings — the direction worth guarding, and the same rule the Razor scanner
applies to string literals.

Inside a markup extension only the ARGUMENTS are read. `Binding`,
`StaticResource` and `x:Bind` are syntax, and so are the named arguments that
configure a binding rather than name code — `Mode`, `RelativeSource`,
`FallbackValue`, `ElementName`, `StringFormat`. Nested extensions are followed,
which is how `Converter={StaticResource BoolNegationConverter}` reaches the
converter class.

`x:Name` and `x:Key` **declare**; they are not references.

### What this changes, measured

| Repo | orphans before | after | rescued | regressed |
|---|---:|---:|---:|---:|
| Files (WinUI) | 4,818 | **4,399** | 419 | **0** |
| Avalonia | 14,827 | **14,213** | 615 | **0** |

No symbols are added — a view merges into the code-behind symbol that already
existed — so unlike Razor there is no orphan-candidate inflation and the totals
fall directly. What comes back is what the corpus predicted: `AdaptiveGridView`,
`BladeItem.CloseButtonForeground`, `BreadcrumbBar.EllipsisButtonToolTip`,
Avalonia's `GenericValueConverter` and `TestItemView`.

*Cost:* immeasurable at this scale — +3ms on Files' 145 documents, within noise
on Avalonia's 481.

*Limits:* a handler whose name follows neither convention and whose event this
file does not know is missed. `{Binding}` with no path names nothing. A reference
is harvested by NAME, so an unrelated symbol sharing it is credited — the same
bias, in the same safe direction, as `test_ref`.

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

A loop's **iterable is evaluated in the enclosing scope**, so it is not counted as
nested work. `foreach (var x in items.Where(p))` enumerates once — the lambda runs
per element of `items`, which is the same *n* the loop itself runs, not *n* per
iteration — so it is O(n), not O(n²). LINQ puts a `Where`/`Select`/`OrderBy` in
the iterable position of a great many `foreach` statements, and counting it inside
inflated the estimate for all of them. A `for` initializer runs once for the same
reason; its condition and update genuinely repeat and stay inside.

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
