# Architecture

How enola works under the hood — the mental model first, the reference second.

If you just want to install it and point your agent at a repo, start with the [README](README.md). This document is for people who want to understand *why* enola is built the way it is, and for anyone extending it.

---

## The idea

enola models a codebase as a **graph of architectural types and the relations between them**.

The types are called **kinds** — modules, symbols, routes, storage, dependencies, services. The relations are the edges between them — *declares*, *imports*, *calls*, *implements*, and so on. Together they form a typed, directed graph: a structural map of what exists in your code and how it connects.

Two design choices make this graph useful in a way that "throw the repo at an LLM" is not:

1. **It is typed and structural, not textual.** The unit of knowledge is a fact (`AuthHandler is a struct in internal/auth/handler.go`) and an edge (`LoginController calls AuthHandler.Verify`), not a chunk of text or an embedding vector. A graph of typed nodes can be *traversed* and *queried* with exact answers — "what depends on this?", "what is the path from A to B?" — instead of *retrieved* with approximate similarity.

2. **Every fact is derived, never inferred.** This is the core invariant:

   > **Nothing in the graph is guessed by a language model.** Every node and edge comes from a real parser (Go's `go/ast`, a tree-sitter grammar, a language-specific scanner) or a deterministic algorithm (Tarjan's strongly-connected-components for cycles, pattern matching for layers). Run enola twice on the same commit and you get the same graph, byte for byte.

That determinism is the whole point. An AI agent reasoning over enola's graph is standing on ground truth it can trust, instead of re-deriving the structure of your code — imperfectly, and at the cost of tokens — on every single task.

---

## The fact model

Defined in [`internal/facts/model.go`](internal/facts/model.go). A `Fact` is a node in the graph:

```go
type Fact struct {
    Kind      string         // one of the kinds below
    Name      string         // canonical identifier
    File      string         // source path (repo-prefixed in multi-repo mode)
    Line      int            // source line
    Repo      string         // repo label (multi-repo mode)
    Props     map[string]any // kind-specific properties
    Relations []Relation     // outgoing edges
}
```

### Kinds — the architectural types

| Kind         | What it represents |
|--------------|--------------------|
| `module`     | A package, directory, or logical grouping of code |
| `symbol`     | A function, method, struct, interface, type, class, variable, constant, or enum |
| `route`      | An HTTP/API endpoint (Next.js page, Rails route, FastAPI handler, OpenAPI operation, …) |
| `storage`    | A data store — a database table/model, cache, etc. |
| `dependency` | An import / require relationship |
| `service`    | A whole repository, used as a node in the cross-repo graph (see [Graph of graphs](#cross-repo-the-graph-of-graphs)) |

For `symbol` facts, the specific construct is carried in `Props["symbol_kind"]`, one of: `function`, `method`, `struct`, `interface`, `type`, `class`, `variable`, `constant`, `enum`.

### Relations — the edges

| Relation       | Meaning |
|----------------|---------|
| `declares`     | A module/file declares a symbol |
| `imports`      | A module imports another module |
| `calls`        | A symbol calls another symbol |
| `implements`   | A symbol implements an interface |
| `depends_on`   | A generic dependency (e.g. a package-boundary rule) |
| `instantiates` | A symbol constructs an instance of a type |
| `injects`      | A symbol takes a type as a dependency-injected constructor parameter |
| `has_method`   | A type owns a method (synthesized when the graph is built — see below) |

### A tiny example

A Go file `internal/auth/handler.go` with a `LoginHandler` struct, a `Verify` method on it, and a call to `tokens.Decode` produces roughly:

```
module    internal/auth
symbol    LoginHandler            (symbol_kind: struct)   declares-> LoginHandler.Verify
symbol    LoginHandler.Verify     (symbol_kind: method)   calls-> tokens.Decode
dependency internal/tokens        (imported by internal/auth)
```

Every one of those lines came from parsing the file — not from asking a model what the file "looks like."

---

## The graph

Once facts are extracted they're indexed into a **bidirectional graph** ([`internal/facts/graph.go`](internal/facts/graph.go)). Each node keeps both its outgoing edges (*what it depends on*) and its incoming edges (*what depends on it*). That single structure is what makes three kinds of question cheap to answer:

- **Reachability** — follow edges *forward* ("what does X depend on, transitively?") or *reverse* ("what depends on X, transitively?").
- **Shortest path** — the chain of edges connecting two nodes ("how does the HTTP handler reach the database layer?").
- **Impact / blast radius** — the full set of nodes that transitively depend on a target, grouped by distance.

The graph builder adds a few **synthetic edges** so that traversals are semantically complete rather than literally what each parser emitted:

- **`has_method`** links a type to its methods. Extractors emit methods as their own facts (named `Type.method`) with no back-link; without `has_method`, walking forward from a struct would miss everything it owns.
- **module → import bridging** connects a module straight through to the things it imports, so a forward walk from a module reaches its dependencies.
- **cross-repo target normalization** strips known module-path prefixes so a call in one repo resolves to the symbol in another.

These are why, when you ask "what's the blast radius of changing `AuthService`?", enola also finds callers that only ever touch `AuthService` through one of its methods or its constructor — not just the ones that name the type directly.

---

## The pipeline

A snapshot is produced by a fixed, deterministic pipeline ([`internal/engine/engine.go`](internal/engine/engine.go)):

```
Repository
   │
   ▼
File Walker ──▶ Extractors ──▶ Fact Store ──▶ Cross-Repo Linker ──▶ Graph Index
 (apply        (Go, Java,      (indexed by     (only with 2+         (bidirectional)
  ignore        Kotlin, Python, kind / file /    repos loaded)             │
  globs)        TS, Swift,      name / repo)                               ▼
                Ruby, C++, OpenAPI)                                   Explainers
                                                                  (cycles, layers,
                                                                     crossrepo)
                                                                          │
                                                                          ▼
                                                                      Renderer
                                                                  (llm_context.md)
                                                                          │
                                                                          ▼
                                                                      Artifacts
                                                                     (.enola/)
```

Stage by stage:

1. **File walker** — enumerates files under the repo, applying the `ignore` globs from config so build output, vendored code, tests, and generated files never reach the parsers.
2. **Extractors** — each enabled language extractor first *detects* whether it applies (e.g. Go runs when there's a `go.mod`), then *parses* the matching files and emits facts. This is pure parsing; see [Supported languages](#supported-languages) for what each one understands.
3. **Fact store** — facts land in an in-memory store ([`internal/facts/store.go`](internal/facts/store.go)) indexed by kind, file, name, and repo for fast queries. In append mode, facts are tagged with a repo label and file paths are repo-prefixed.
4. **Cross-repo linker** — only when two or more repos are loaded. It connects the per-repo graphs by matching HTTP client/server routes and shared-library imports, emitting `service` nodes and cross-repo dependency edges. The link set is recomputed from scratch on every append, so it always reflects exactly the repos currently loaded.
5. **Graph index** — builds the bidirectional graph (with the synthetic edges above) that powers `traverse`, `find_path`, and `impact_analysis`.
6. **Explainers** — run deterministic analyses over the facts and emit insights (next section).
7. **Renderer** — produces `llm_context.md`, a compact, token-budgeted architecture summary an agent can read directly.
8. **Artifacts** — everything is written to `.enola/` (see [Output artifacts](#output-artifacts)). Content hashes enable incremental re-extraction on the next run.

Three plugin roles drive the middle of the pipeline — **extractors** (source → facts), **explainers** (facts → insights), and **renderers** (snapshot → artifacts). Each is a small Go interface with a registry, so adding a language or an analysis is a self-contained addition rather than a change to the engine.

---

## Insights (explainers)

Explainers turn raw facts into architectural observations. Each insight carries a **confidence** score: `1.0` means it's a structural fact, below `1.0` means it's a heuristic.

- **Cycles** ([`internal/explainers/cycles`](internal/explainers/cycles/cycles.go)) — finds cyclic module dependencies using **Tarjan's strongly-connected-components algorithm**. A cycle either exists in the import graph or it doesn't, so these land at confidence `1.0`, with every module in the cycle listed as evidence.
- **Layers** ([`internal/explainers/layers`](internal/explainers/layers/layers.go)) — recognizes common architectural shapes by matching module paths against known patterns: **hexagonal** (application / port / adapter / domain / …), **Next.js** (pages / components / hooks / lib / api / …), and **Go-standard** (cmd / internal / pkg / api). Confidence is computed from how much of the codebase matches. It also flags **layer violations** — an inner layer importing an outer one — as lower-confidence heuristic warnings.
- **Cross-repo** ([`internal/explainers/crossrepo`](internal/explainers/crossrepo/crossrepo.go)) — summarizes the cross-repo edges found by the linker. Returns nothing for a single-repo snapshot.

---

## The tools

enola is a stdio [MCP](https://modelcontextprotocol.io/) server. It exposes **seven tools** and no MCP resources — everything flows through tool calls. The tools defined in [`internal/server/server.go`](internal/server/server.go) are listed below, each leading with the question it answers.

> Most read tools share a **token-cost ladder** via `output_mode`: `summary` (smallest, aggregated counts) → `compact` (markdown, grouped) → `full` (raw JSON, can be large). Start with `summary` and escalate only when you need node-level detail. Most also accept `max_tokens` to hard-cap a response.

### `generate_snapshot` — "index this repository"

Parses a repo and builds the fact graph. **Run this first**; re-run after code changes.

| Parameter | Description |
|-----------|-------------|
| `repo_path` | Path to the repository. Defaults to the configured repo. |
| `append` | If `true`, keep existing facts and add a new repo with repo-prefixed paths (for cross-repo analysis). enola auto-enables append when it detects you switched repos. Default `false`. |

### `explore` — "what's in here, and what touches it?"

The primary exploration tool — use it first after generating. Given a module, file, symbol, or directory prefix, returns a markdown summary: symbols (with kinds and line numbers), direct dependencies, and reverse dependents.

| Parameter | Description |
|-----------|-------------|
| `focus` *(required)* | Module name, file path, or symbol name to explore. |
| `depth` | `1` = direct relations only, `2` = include relations of relations. Default `1`, max `2`. |
| `output_mode` | At `depth=2`: `summary` (default) returns aggregated insights (hotspots, cycle/layer warnings, size metrics); `compact`/`full` list per-symbol relations. Ignored at `depth=1`. |
| `max_tokens` | Optional hard cap on output size. |

### `query_facts` — "give me exactly these facts"

Precision filtering over the fact store: every route, every interface, every external dependency, all symbols in a file. Filters AND across dimensions, OR within a dimension.

| Parameter | Description |
|-----------|-------------|
| `kind` | Filter by kind: `module`, `symbol`, `route`, `storage`, `dependency`, `service`. |
| `file` / `file_prefix` | Filter by exact file path or by path prefix (e.g. `internal/server`). |
| `name` | Substring match on name. |
| `relation` | Filter by relation kind (`declares`, `imports`, `calls`, `implements`, `depends_on`). |
| `prop` / `prop_value` | Filter by a property and its value (e.g. `prop=source prop_value=external`). |
| `names` / `files` / `kinds` | Batch (OR) variants for exact-name / multi-file / multi-kind lookups. |
| `repo` | Filter by repo label (multi-repo mode). |
| `offset` / `limit` | Pagination. Default limit 100, max 500. |
| `include_related` | Inline full fact data for each relation target. |
| `output_mode` | `full` (default) → `compact` → `names` → `summary`. |
| `max_tokens` | Optional hard cap. |

### `show_symbol` — "show me the actual code"

Returns the source for a named symbol. Prefers an exact name match; falls back to substring (up to 5 results). Works in single- and multi-repo mode.

| Parameter | Description |
|-----------|-------------|
| `name` *(required)* | Symbol name (substring match). |
| `context_lines` | Source lines around the symbol. Default 60 (≈15 before the declaration, ≈45 after). |

### `traverse` — "walk the graph from here"

Breadth-first walk of the dependency/call graph. `direction='forward'` answers *"what does X depend on?"*; `direction='reverse'` answers *"what depends on X?"*. Use it instead of many `explore` calls when you need transitive relationships.

| Parameter | Description |
|-----------|-------------|
| `start` *(required)* | Starting node. Substring match; supports scoped prefixes (`repo:`, `kind:`, `file:`) and package-qualified names (e.g. `domain/cart.CartService`) to disambiguate. |
| `direction` | `forward` (default) or `reverse`. |
| `relation_kinds` | Restrict to `imports`, `calls`, `declares`, `implements`, `depends_on`, `has_method`. Default: all. |
| `node_kinds` | Restrict *output* to specific kinds. Default: all. |
| `max_depth` | 1–20. Default 5. |
| `max_nodes` | 1–500. Default 100. |
| `output_mode` | `summary` (default) → `compact` → `full`. |
| `max_tokens` | Optional hard cap. |

Walking forward from a struct/interface follows `has_method` edges into its methods. Walking *reverse* from a type also seeds from its methods and constructor, so callers that only touch it through a method are still found — the same rollup that powers `impact_analysis`.

### `find_path` — "how does A reach B?"

Shortest path (by hop count) between two nodes — a call chain or dependency chain. When an endpoint is ambiguous, `find_path` tries the top candidates (and, for a type, its methods/constructor) and returns the first path it finds; the response reports the candidates it tried.

| Parameter | Description |
|-----------|-------------|
| `from` *(required)* | Source node. Substring match + scoped/package-qualified prefixes. |
| `to` *(required)* | Target node. Same matching rules. |
| `relation_kinds` | Restrict to specific relation types. Default: all. |
| `max_depth` | Max path length, 1–20. Default 10. |

### `impact_analysis` — "if I change this, what breaks?"

The change-impact tool. Computes the **blast radius** of a target: every node that transitively *depends on* it, grouped by hop depth. This is the determinism payoff for refactoring — instead of an agent guessing what a change might affect, it gets the exact dependent set, with an accurate total even when the displayed list is capped.

| Parameter | Description |
|-----------|-------------|
| `target` *(required)* | The node being changed. Substring match + scoped prefixes. |
| `max_depth` | Hops of impact to compute, 1–10. Default 3. |
| `max_nodes` | Max displayed nodes, 1–500. Default 200 (the *total* count stays accurate regardless). |
| `include_forward` | Also show what the target depends on — what could break *it*. Default `false`. |
| `output_mode` | `summary` (default) → `compact` → `full`. |
| `max_tokens` | Optional hard cap. |

What makes it precise:

- **Reverse closure.** It walks incoming edges, grouping dependents by distance — depth 1 is direct callers, depth 2 is their callers, and so on. Direct dependents are higher-risk than distant ones, and the grouping makes that visible.
- **Type rollup.** When the target is a type, the walk seeds from the type *plus its methods plus its constructor* (`NewType`), so it catches callers that reference the type only indirectly.
- **Accurate totals.** `max_nodes` caps what's *shown*, not what's *counted* — the reported total dependent count reflects the true reachable set within `max_depth`.
- **Cross-repo aware.** In multi-repo mode it reports which other repos contain a dependent.

---

## Cross-repo: the graph of graphs

enola can analyze multiple repositories together. Use `append` mode to incrementally build a combined fact store, then query across all of them.

1. **Generate the first snapshot** as usual.
2. **Append additional repos** by calling `generate_snapshot` with `append=true`. Each appended repo's facts are tagged with a **repo label** (the directory basename, e.g. `/path/to/go-service` → `go-service`) and its file paths are prefixed with that label (e.g. `go-service/lib/foo.go`).
3. **Query across repos** with the `repo` filter on `query_facts` to scope to one repo, or omit it to query all at once.

### Linking, not just co-locating

Appending several repos does more than pool their facts — a linking pass connects the per-repo graphs using three signals the extractors already capture:

- **HTTP route role matching** — a route a repo *calls* (`role:"client"`, e.g. from a generated OpenAPI client) is matched to a route another repo *serves* (`role:"server"`, or a framework route) by normalized path + method. The caller is recorded as depending on the servee.
- **Import / shared-lib references** — an import whose scope or leading segment names another loaded repo (e.g. `@app-web/lib-api`, `lib-core/money`) records a dependency on that repo.
- **Shared symbol surface** — when two repos declare enough of the same distinctive types (e.g. a vendored protocol header copied between them — the `onelab::*` / `GmshClient` / `GmshServer` classes shared by *gmsh* and *getdp*), they are coupled. The match is on each type's portable identity (the namespace-qualified name with the repo-specific directory prefix stripped), filtered to type-like symbols (class/struct/interface/enum) and to distinctive names — namespaced identities always count; bare names must be non-generic and reasonably long. A pair links only above a small shared-type threshold, so an incidental `Config`/`JsonParser` collision can't fabricate a dependency. This relationship is symmetric, so it is emitted as a **bidirectional** pair of edges marked `via:"shared_symbols"`.

These become real, queryable facts:

- A `service` node per repo (`query_facts kind=service`), named by its repo label.
- A cross-repo dependency edge per `consumer → provider` pair, carrying the matched endpoints, import samples, and shared-symbol samples.

Because they're ordinary graph nodes and edges, the traversal tools become cross-repo aware with no extra steps — `traverse`, `find_path`, and `impact_analysis` all reach across repo boundaries. The cross-repo dependencies also appear as a **Cross-Repo Dependencies** section in `llm_context.md`, so an agent reading the snapshot sees them without running a tool.

> **Config note:** the `crossrepo` explainer (which adds a cross-repo entry to `insights.json`) must be listed under `explainers:` in your config — the bundled configs already include it. The `service` nodes, graph edges, traversal, and the `llm_context.md` section work regardless of explainer config; only the `insights.json` entry depends on it.

---

## Configuration

Create an `mcp-arch.yaml` (or pass a custom path as the first CLI argument):

```yaml
repo: "."
ignore:
  - "vendor/**"
  - "node_modules/**"
  - ".git/**"
  - ".enola/**"
  # tests, build output, generated files, docs, config data, …
  - "**/*_test.go"
  - "**/*.test.ts"
  - ".next/**"
  - "build/**"
  - "**/*.md"
  - "**/*.yaml"
extractors:
  - go
  - java
  - kotlin
  - openapi
  - python
  - typescript
  - swift
  - ruby
explainers:
  - cycles
  - layers
  - crossrepo
renderers:
  - llm_context
output:
  dir: ".enola"
  max_context_tokens: 16000
```

The bundled [`mcp-arch.yaml`](mcp-arch.yaml) ships a much fuller `ignore` list (Android/Gradle, Xcode/SPM, Rails, CI, Docker, env files, …); see it and the per-language configs under [`examples/`](examples/) for ready-made starting points.

| Field | Description | Default |
|-------|-------------|---------|
| `repo` | Repository root path | `"."` |
| `ignore` | Glob patterns for files/dirs to skip | vendor, node_modules, .git, tests, build dirs, docs, config data, … |
| `extractors` | Enabled extractors | `["cpp", "go", "java", "kotlin", "openapi", "python", "typescript", "swift", "ruby"]` |
| `explainers` | Enabled explainers | `["cycles", "layers", "crossrepo"]` |
| `renderers` | Enabled renderers | `["llm_context"]` |
| `output.dir` | Output directory for artifacts | `".enola"` |
| `output.max_context_tokens` | Token budget for `llm_context.md` | `16000` |

---

## Supported languages

Each extractor is detected by characteristic project files and then parses what it finds. Detection walks into subdirectories for the monorepo cases noted below.

| Language   | Parser           | Detected by |
|------------|------------------|-------------|
| Go         | `go/ast`         | `go.mod` present |
| Java       | tree-sitter      | `pom.xml` (Maven) present, or any `.java` source file (a Gradle build file alone does **not** trigger it — Kotlin/Android use Gradle too) |
| Kotlin     | tree-sitter      | `build.gradle.kts` / `build.gradle` with Kotlin/Android |
| Python     | tree-sitter      | `pyproject.toml`, `setup.py`, `requirements.txt`, `Pipfile`, `pytest.ini`, `mypy.ini`, `tox.ini`, or `setup.cfg` (root or up to 3 levels deep) |
| TypeScript | tree-sitter      | `tsconfig.json`, `tsconfig.base.json`, or `package.json` with TypeScript (root or one level deep) |
| Swift      | tree-sitter      | `Package.swift`, `.xcodeproj`, or `.xcworkspace` present |
| Ruby       | regex scanner    | `Gemfile` present |
| C++        | tree-sitter      | a C++ source (`.cpp`/`.cc`/`.cxx`/`.hpp`/...) present, or a build file (`CMakeLists.txt`/`Makefile`/`meson.build`/`*.vcxproj`) plus any header |
| OpenAPI    | YAML/JSON scanner| any file containing `openapi:` or `swagger:` |

**Go** uses the standard-library parser directly, so symbols, methods, interfaces, imports, and call edges are exact.

**TypeScript** (tree-sitter) includes Next.js route detection (App Router and Pages Router), monorepo detection one level deep, and parsing of `openapi-typescript`-generated client files — each operation is emitted as a `route` fact with `role:"client"`. App Router route groups like `(standard)` are stripped from URLs.

**Python** is parsed with tree-sitter (the concrete syntax tree handles nested classes/methods and docstrings natively, replacing the older indentation scanner). It understands **FastAPI/Starlette** route decorators and **Django** routes — `@api_view([...])` and `urls.py` `path()`/`re_path()` — emitting a `route` fact per endpoint. It emits `storage` facts for **SQLAlchemy** `__tablename__` and **Django models** (table name inferred from the class name), and classifies Django views and serializers via a `django_component` prop. It captures `async def` (`async: true`), decorator props (`@property`, `@staticmethod`, `@classmethod`, `@abstractmethod`, and Celery `@task`/`@shared_task`), and return-type hints. Each class emits an `implements` edge per base class, with generic type parameters stripped (`CRUDBase[Model, Id]` → `CRUDBase`), and both `import` forms become `dependency` facts. Crucially, the Python extractor now walks function and method bodies for call sites, emitting `calls` and `instantiates` edges (filtering out builtins) — so Python code participates in the dependency/call graph and is reachable by `traverse`, `find_path`, and `impact_analysis`. Monorepo detection walks up to 3 levels.

**Java** (tree-sitter) is framework-aware for the JVM server ecosystem. It emits symbol facts for classes, interfaces, enums, records, and annotation types, plus their methods, constructors, and fields, named with enola's `<dir>.<Type>` / `<dir>.<Type>.<method>` convention (nested types are qualified through the enclosing type). `extends`/`implements` become `implements` edges, `new X()` becomes `instantiates`, same-class method calls become `calls`, and both import forms become `dependency` facts split into internal vs. external. Because Java imports are explicit, type-reference edges are resolved through a project-wide fully-qualified-name index built in a second pass — so `implements`/`instantiates`/`injects` targets point at the canonical declaring symbol in another file or module rather than a bare name. Framework specialization covers **Spring MVC** (a `@RestController`/`@Controller` class's `@RequestMapping` base path is combined with method-level `@GetMapping`/`@PostMapping`/`@PutMapping`/`@DeleteMapping`/`@PatchMapping`/`@RequestMapping(method=…)` into one `route` per endpoint, carrying the HTTP method and the handler symbol), **Spring stereotypes** (`@Service`/`@Component`/`@Repository`/`@Controller`/`@Configuration` classified via a `component` prop), **dependency injection** (`@Autowired` fields, constructor injection, and Lombok `@RequiredArgsConstructor` over `final` fields → `injects` edges), and **JPA / Spring Data storage** (`@Entity` → a `storage` fact with `storage_kind: entity`; `@Repository` and `JpaRepository`/`CrudRepository`-style interfaces → `storage_kind: repository`). A `@Table(name = …)` is captured, and when the name is given as a `static final String` constant it is resolved to its literal value — the original identifier is preserved in a `table_constant` prop. **Apache Dubbo** is recognized too: `@SPI`/`@Activate`/`@DubboService` tag the type with `framework: "dubbo"` (`dubbo_spi`, `dubbo_activate`). Detection requires Maven (`pom.xml`) or real `.java` sources, so a pure-Kotlin Gradle project is left to the Kotlin extractor.

**Kotlin** is Android-aware: it detects Jetpack Compose (`@Composable`), Hilt DI (`@HiltViewModel`, `@Module`, `@AndroidEntryPoint`), Room (`@Entity`, `@Dao`, `@Database`), ViewModels, Repositories, Use Cases, and Workers.

**Swift** is iOS-aware: SwiftUI views (`View`/`App`/`Scene`), UIKit (`UIViewController`/`UIView` subclasses), Combine view models (`ObservableObject`, `@Observable`), architectural roles (Repositories, Use Cases, Coordinators, Services, DI containers), and `@MainActor`.

**OpenAPI** scans for spec files independently of the main walker (so it finds them even when `*.yaml`/`*.json` are globally ignored), confirming candidates by an `openapi:`/`swagger:` key. It emits one `route` per operation enriched with method, `operationId`, summary, tags, and a spec back-reference; specs under an `openapi/client/` directory are marked `role:"client"`. Gateway extensions (`x-gateway-config`, `x-gateway-capabilities`) are parsed into props.

**Ruby** is Rails-aware: ActiveRecord models (`has_many`, `belongs_to`, scopes, table inference), the route DSL in `config/routes.rb` (resources, namespaces, member/collection blocks), and Packwerk package boundaries (`package.yml` dependency enforcement). It also tracks modules, classes, methods with visibility, mixins (`include`/`extend`/`prepend`), `ActiveSupport::Concern`, constants, and attributes.

**C++** is parsed with tree-sitter and handles the header/source split that defines the language. Classes, structs, unions, enums (incl. `enum class`), namespaces, free functions and methods, data members, and `typedef`/`using` aliases become symbol facts named `<dir>.<ns1::ns2::Class::member>` — enola's `<dir>.` module convention on the outside, native C++ `::` scope inside. Because an out-of-line definition `Class::method` (parsed from a `qualified_identifier`) yields the same canonical name as its in-class declaration, a dedup pass **merges a header's method prototype with its `.cpp` definition** into a single symbol (the definition wins for file/line and carries the call-graph edges). Base classes become `implements` edges; method bodies are walked for `calls`/`instantiates` edges; quoted `#include "x.h"` becomes a `dependency` resolved to the declaring module, while system `<...>` includes are skipped. Templates are unwrapped to their inner declaration and flagged `templated`, and the walker descends through `#if`/`#ifdef` preprocessor guards (so code wrapped in `#if defined(HAVE_*)` and headers behind include guards are still extracted). *Limitation:* header/source merging relies on the `.h` and `.cpp` living in the same directory (the common layout); split `include/` + `src/` trees are not merged.

---

## Output artifacts

After `generate_snapshot`, these are written to the output directory (default `.enola/`):

| File | Description |
|------|-------------|
| `llm_context.md` | Compact, token-budgeted architecture summary for an agent to read directly |
| `facts.jsonl` | Every extracted fact, one JSON object per line |
| `insights.json` | Architectural insights with confidence scores |
| `snapshot.meta.json` | Metadata including per-file content hashes for incremental updates |

`llm_context.md` is the human- and agent-readable digest. It's prioritized and truncated to the configured token budget, and includes (as space allows): a repository map of modules, the detected architecture pattern, cross-repo dependencies, entry points, routes, storage, dependency rules, the most critical modules (by fan-in/fan-out), risk zones (cycles and layer violations), and an architecture-aware "how to add a feature" guide.

---

## Determinism & incremental updates

Two properties hold the whole design together:

- **No model in the loop.** Extraction and analysis never call an LLM. The graph is a function of your source code and the configured plugins — reproducible across runs and machines. The LLM enters only *downstream*, as the consumer of the snapshot.
- **Incremental by content hash.** `snapshot.meta.json` records a SHA-256 for every file. On a re-run, only files whose hash changed are re-parsed, so refreshing a snapshot on a large repo is fast.

Together these mean the architectural map an agent relies on is both *trustworthy* (it reflects the code, not a guess) and *cheap to keep current* (regenerate after changes without re-scanning everything).

---

## License & acknowledgements

Apache License 2.0 — see [`LICENSE`](LICENSE). Third-party components are listed in [`NOTICE`](NOTICE). Swift parsing uses the [tree-sitter-swift](https://github.com/alex-pinkus/tree-sitter-swift) grammar by Alex Pinkus (MIT), vendored under [`internal/extractors/swiftextractor/grammar/`](internal/extractors/swiftextractor/grammar/).
