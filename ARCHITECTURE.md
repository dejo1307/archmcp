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

   > **Nothing in the graph is guessed by a language model.** Every node and edge comes from a real parser (Go's `go/ast`, a tree-sitter grammar, a YAML/JSON scanner) or a deterministic algorithm (Tarjan's strongly-connected-components for cycles, pattern matching for layers). Run enola twice on the same commit and you get the same graph, byte for byte.

That determinism is the whole point. An AI agent reasoning over enola's graph is standing on ground truth it can trust, instead of re-deriving the structure of your code — imperfectly, and at the cost of tokens — on every single task.

Those two choices describe what is *in* the graph. A third describes what happens to it: the graph is computed when asked and kept as a value rather than maintained in place, which is what makes one snapshot comparable to another — see [Why a snapshot, not a store](#why-a-snapshot-not-a-store), and [docs/SNAPSHOTS.md](docs/SNAPSHOTS.md) for the full argument.

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
| `storage`    | A data store — a database table/model, cache, or a messaging topic |
| `dependency` | An import / require relationship |
| `service`    | A whole repository, used as a node in the cross-repo graph (see [Graph of graphs](#cross-repo-the-graph-of-graphs)) |

For `symbol` facts, the specific construct is carried in `Props["symbol_kind"]`, one of: `function`, `method`, `struct`, `interface`, `type`, `class`, `variable`, `constant`, `enum`.

`storage` facts carry the analogous discriminator in `Props["storage_kind"]` — `table`, `model`, `entity`, `repository`, `s3`, `topic`, … — because a Kafka topic and a Postgres table are the same *kind* of node (a data store the code names) but not the same thing. A `topic` fact additionally carries `messaging` (the transport, e.g. `kafka`) and `source` (which syntactic form the topic name was read from), and is what the async cross-repo signal binds on (see [Linking, not just co-locating](#linking-not-just-co-locating)).

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
| `handled_by`   | A route/endpoint is served by a symbol — e.g. a gRPC RPC route bound to its Go handler method (added post-extraction) |

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
┌──────────────────┐  apply ignore globs (a few extractors also scan
│   File Walker    │  config-format files directly — see Supported languages)
└──────────────────┘
   │
   ▼
┌──────────────────┐  Go · Java · Kotlin · JS/TS · Vue · Svelte · Python ·
│   Extractors     │  Swift · Ruby · Rust · C/C++ · PHP · OpenAPI (source → facts)
└──────────────────┘
   │
   ▼
┌──────────────────┐
│   Fact Store     │  indexed by kind / file / name / repo
└──────────────────┘
   │
   ▼
┌──────────────────┐  binders (pre-link) · cross-repo signals ·
│   Linking        │  binders (post-link)   — signals only with 2+ repos
└──────────────────┘
   │
   ▼
┌──────────────────┐
│   Graph Index    │  bidirectional (+ synthetic edges)
└──────────────────┘
   │
   ▼
┌──────────────────┐  cycles · layers · crossrepo · coverage · unused-routes ·
│   Explainers     │  god-class · hotspots · dependency-depth ·
└──────────────────┘  exported-surface · complexity-outliers   (facts → insights)
   │
   ▼
┌──────────────────┐
│    Renderer      │  llm_context.md   (snapshot → artifacts)
└──────────────────┘
   │
   ▼
┌──────────────────┐
│    Artifacts     │  .enola/
└──────────────────┘
```

Stage by stage:

1. **File walker** — enumerates files under the repo, applying the `ignore` globs from config so build output, vendored code, tests, and generated files never reach the parsers. Beyond the path globs, the TypeScript extractor additionally detects **minified/bundled** files by content (any line longer than ~2000 chars) and skips them, so a hash-named vendor bundle checked in outside a build directory (e.g. a minified third-party bundle served from a static assets dir) does not pollute the fact graph with obfuscated symbols and spurious complexity/hotspot findings. A few extractors (OpenAPI, and PHP's Symfony route config) deliberately scan specific config-format files (YAML/JSON) directly from disk, bypassing these globs — because the globs exist to suppress config/data noise, not to hide those architecturally meaningful files (see [Supported languages](#supported-languages)).
2. **Extractors** — each enabled language extractor first *detects* whether it applies (e.g. Go runs when there's a `go.mod`), then *parses* the matching files and emits facts. This is pure parsing; see [Supported languages](#supported-languages) for what each one understands.
3. **Fact store** — facts land in an in-memory store ([`internal/facts/store.go`](internal/facts/store.go)) indexed by kind, file, name, and repo for fast queries. In append mode, facts are tagged with a repo label and file paths are repo-prefixed.
4. **Linking** — resolves the edges no single extractor could see, in three ordered steps.
   **Pre-link binders** run first: a binder resolves references *within* the assembled fact set, and this stage is for the ones that change what the linker will match on (the Python gRPC client's short service name is rewritten to its fully qualified wire path here — after linking it would be too late, and the dependency would silently vanish).
   **Cross-repo signals** run next, and only when two or more repos are loaded. Each is an independent plugin reporting evidence — HTTP route role matching, imports, Kafka topic ownership, shared code — which the linker materializes into `service` nodes and cross-repo dependency facts. Directional signals run before symmetric ones, because the only honest way to orient a symmetric finding is to defer to a direction something else established.
   **Post-link binders** run last: route-to-handler binding for gRPC and HTTP, and the pass that records which routes went unmatched.
   The whole stage is recomputed from scratch on every append, so it always reflects exactly the repos currently loaded.
5. **Graph index** — builds the bidirectional graph (with the synthetic edges above) that powers `traverse`, `find_path`, and `impact_analysis`.
6. **Explainers** — run deterministic analyses over the facts and emit insights (next section).
7. **Renderer** — produces `llm_context.md`, a compact, token-budgeted architecture summary an agent can read directly.
8. **Artifacts** — everything is written to `.enola/` (see [Output artifacts](#output-artifacts)). Content hashes enable incremental re-extraction on the next run.

Five plugin roles drive the middle of the pipeline — **extractors** (source → facts), **binders** (resolve references across an assembled fact set), **cross-repo signals** (evidence that one repo depends on another), **explainers** (facts → insights), and **renderers** (snapshot → artifacts). Each is a small Go interface in [`pkg/plugin`](pkg/plugin/) with a registry, so adding a language, a connection, or an analysis is a self-contained addition rather than a change to the engine. [docs/EXTENDING.md](docs/EXTENDING.md) is the guide to the middle three, including which one a given problem calls for.

**One-shot explain mode.** `enola --explain [repo_path]` is an alternative exit path through the pipeline: stages 1–6 run normally, but instead of proceeding to stage 7 (Renderer) and stage 8 (Artifacts), `pkg/explain.Compute()` reads the fact store, produces a `Report` struct, and `report.Render()` prints a human-readable statistical summary to stdout. No artifacts are written; `.enola/` is not touched. See [The explain package (`pkg/explain`)](#the-explain-package-pkgexplain) below.

---

## Insights (explainers)

*Why these exist and what a finding is worth once you have it: **[docs/EXPLAINERS.md](docs/EXPLAINERS.md)**. This section is the reference.*

Explainers turn raw facts into architectural observations. Each insight carries a **confidence** score: `1.0` means it's a structural fact, below `1.0` means it's a heuristic. Every insight is also tagged with the explainer that produced it (`Insight.Source`), and the whole set is retrievable through the **`query_insights`** tool — filter by `explainer`, `repo`, or `min_confidence` — so an agent fetches a finding directly instead of re-deriving it from raw facts or scraping it out of `explore depth=2` / `.enola/insights.json`.

**Annotating facts (`plugin.Annotator`).** An explainer may optionally implement `Annotate(ctx, *facts.Store) error` to write **derived values back onto the facts it analysed**, in addition to emitting insights.

This exists because insights and measurements are different things. An insight is a *finding* — discrete, evidenced, worth attention — and an analyzer that computes a continuous value for every module has no honest way to publish one: a finding per module is noise, and publishing only the outliers throws the rest away. A prop on the fact is the right home, and it brings a property insights do not have — **the snapshot diff already reports prop movement**, so an annotated metric becomes comparable across two snapshots for free, attributed to the entity it belongs to.

The contract is narrow, and each clause pays for itself:

- It runs **after linking**, so whole-graph derivations (afferent/efferent coupling) are computable here and not in an extractor.
- It runs **before every `Explain`**, not interleaved, so no explainer's insights depend on whether another was registered ahead of it — which would make the snapshot depend on registration order.
- It may only **add or update props**. Adding, removing or renaming facts would make the graph depend on which explainers were enabled, and two snapshots taken with different sets would no longer be comparable.
- Values must be **deterministic and stable in their rendered form**. They are written into `facts.jsonl` and hashed into the snapshot ID, so an unrounded float — whose last bits can move with summation order — would make an unchanged tree produce a different snapshot on every run. Round before storing.

Gated exactly like `Explain`: an explainer excluded by config annotates nothing.

- **Cycles** ([`internal/explainers/cycles`](internal/explainers/cycles/cycles.go)) — finds cyclic module dependencies using **Tarjan's strongly-connected-components algorithm**. A cycle either exists in the import graph or it doesn't, so these land at confidence `1.0`, with every module in the cycle listed as evidence.
- **Layers** ([`internal/explainers/layers`](internal/explainers/layers/layers.go)) — recognizes common architectural shapes by matching module paths against eight known taxonomies: **hexagonal** (application / port / adapter / domain / …), **Go-standard** (cmd / internal / pkg / api), **Next.js** (pages / components / hooks / lib / api / …), **rails-mvc**, **django**, **spring-layered**, **android-clean** and **ios-clean**. Most are gated on a detected framework or language, so only one is ever reported: the most specific match wins, and ties break on confidence. Confidence is computed from how much of the codebase matches — capped below `1.0`, because a directory-name match is a well-supported guess and never a proof. Test modules are excluded from that measurement (a build file's `module_role` outranks what a path looks like), so a test source set cannot vote on the architecture. It also flags **layer violations** — an inner layer importing an outer one — as lower-confidence heuristic warnings.
- **Cross-repo** ([`internal/explainers/crossrepo`](internal/explainers/crossrepo/crossrepo.go)) — summarizes the cross-repo edges found by the linker. Returns nothing for a single-repo snapshot.
- **Coverage** ([`internal/explainers/coverage`](internal/explainers/coverage/coverage.go)) — turns the per-service `edge_coverage` counts the linker records into **coverage-gap** insights: a service with no resolved outbound edges but unresolved outbound call sites is flagged as a blind spot ("appears isolated but…"), distinct from one that is genuinely a leaf. Distinguishes absence of edges from a gap in coverage. Returns nothing for a single-repo snapshot. Surfaced programmatically by the `coverage_report` tool.
- **Unused-routes** ([`internal/explainers/unusedroutes`](internal/explainers/unusedroutes/unusedroutes.go)) — the **server-side inverse** of the cross-repo HTTP linker: it rolls up the `route` facts that *no loaded client calls* (tagged `unmatched_by_clients` during linking — see [Finding unused endpoints](#finding-unused-endpoints)) into one candidate-cleanup insight per service. Deliberately conservative: it only considers repos that actually serve a cross-repo client (an HTTP *provider* — never a frontend's own page routes), skips low-signal generic paths (`/health`, single-segment), and biases toward false negatives. Each insight carries the mandatory caveat that candidates are unused *by the loaded clients only* — consumers outside the snapshot (admin scripts, cron, webhooks, third-party clients, deep links) don't appear, so verify before deleting. Confidence `0.6` (a candidate to review, not a verdict). Returns nothing for a single-repo snapshot.
- **God-class** ([`internal/explainers/godclass`](internal/explainers/godclass/godclass.go)) — flags symbols with an outlier **fan-in** (depended upon by far more symbols than average), computed from the graph's reverse adjacency list. High fan-in concentrates change risk.
- **Hotspots** ([`internal/explainers/hotspots`](internal/explainers/hotspots/hotspots.go)) — flags call-graph **pinch points** (symbols with both high fan-in and high fan-out, scored `fanIn × fanOut`). A cheap degree-centrality proxy for betweenness — chokepoints most call chains pass through.
- **Dependency-depth** ([`internal/explainers/depth`](internal/explainers/depth/depth.go)) — flags modules whose **longest transitive import chain** is unusually long (cycle-safe longest-path over the module graph). Deep modules are slow to grasp and widen rebuild/retest impact.
- **Exported-surface** ([`internal/explainers/surface`](internal/explainers/surface/surface.go)) — flags **large public surfaces**: sizeable modules that export almost all their symbols, so they encapsulate little. Because "public is the default" in Go and Ruby (so a raw ratio test floods), it skips mock/test/generated packages, requires a meaningful size and a near-total export ratio, and reports only the **top N worst offenders** (largest public surface first) rather than every match — a digestible shortlist for a visibility review, not a list of definite defects.
- **Complexity-outliers** ([`internal/explainers/complexity`](internal/explainers/complexity/complexity.go)) — flags functions/methods whose **cyclomatic complexity** is a statistical outlier, using the language-agnostic `cyclomatic` prop every extractor records.

The shared module-graph construction and statistical-outlier helpers used by several of these live in [`internal/explainers/common`](internal/explainers/common/common.go).

**Determinism has two halves, and both are load-bearing.** `StronglyConnectedComponents` documents the first: visit nodes in sorted order with sorted neighbour lists, so Go's randomized map iteration cannot reach the output. `MeanStdDev` is the second: it sorts a copy of its values before reducing, so `mean + kσ` is a function of the *multiset* and not of the order the caller iterated.

The second was learned the hard way. Callers build that slice by walking the fact store, whose order reflects concurrent extraction and varies between runs; float addition is not associative; and god-class is the only explainer that puts a threshold into its **output** (`confidence`) rather than using it solely to select. So across 90 runs of 30 repositories, `facts.jsonl` was byte-identical every time — it is sorted before it is written — while `insights.json` moved in the last two digits of a `float64` on one repository. Nothing a human reads changed, and `snapshot_id` does not cover `insights.json`, but `output_hashes["insights.json"]` stopped being reproducible. Any new statistic belongs behind a reduction with this property.

---

## The explain package (`pkg/explain`)

`pkg/explain` ([`pkg/explain/explain.go`](pkg/explain/explain.go)) is a **public** package rather than `internal/` for one reason: `enola-enterprise` imports it to append its own license-gated sections (dead code, package metrics) to the base `Report` before rendering. It is one of several `pkg/` packages that exist for that cross-module consumer — see [The CLI surface](#the-cli-surface-pkgcli-pkgstatus-and-pkgdashboard) for the two that back `--help`, `--list` and `--status`.

### `Report` and `Compute()`

`Compute(eng *bootstrap.Engine) *Report` reads the engine's current fact store and snapshot — it does not generate a snapshot; callers do that first. The fields it populates map directly to the eight output sections:

| Report field(s) | Output section |
|---|---|
| `RepoPath`, `GeneratedAt`, `Duration`, `Extractors`, `TotalFacts` | Overview |
| `KindCounts` | Architectural kinds |
| `SymbolKinds` | Symbol breakdown |
| `Routes`, `RoutesByMethod`, `Storage` | API & data surface |
| `DepSources` | Dependencies |
| `Architecture`, `ArchConfidence`, `Cycles`, `LayerViolations`, `CrossRepoEdges` | Architecture |
| `Modules`, `HighCriticality`, `MediumCriticality`, `Hotspots`, `CouplingUnresolved` | Impact analysis (hotspots) |
| `CodeHealth` | Code health |

`CouplingUnresolved` is a special flag: it is set when dependency facts exist but no import edge resolved to a module, meaning coupling analysis is unavailable rather than genuinely zero. The renderer surfaces this as an explanatory note instead of implying the codebase has no coupling.

`CodeHealth` is a slice of `FindingGroup` (label + count + top offenders), one per symbol/module-level explainer (god-class, hotspots, dependency-depth, exported-surface, complexity-outliers). `Compute` builds it by parsing those explainers' insight titles — the title formats are the contract, noted at each explainer's `Title:` site. Groups with a zero count are omitted, so the section disappears for snapshots without symbols (e.g. OpenAPI-only). Because enola-enterprise renders this same base `Report`, the Code health section appears in the enterprise `--explain` too, before its license-gated sections.

### Extensibility via `ExtraSections`

`Report.ExtraSections []Section` is the extension point for enterprise code. After calling `Compute()`, enterprise calls `report.AddSection(title, body)` (or directly appends `explain.Section{...}`) and then calls `report.Render()`. The renderer appends the extra sections after the seven base sections, using the same plain-text format. This design means `enola-enterprise` only depends on the exported `pkg/explain` surface and never needs to import `internal/facts` or `internal/engine` directly.

### Output format

`Render()` produces plain aligned text (not Markdown), designed to read well in a terminal without paging. Sections are separated by `═` rule lines (60 characters). Key-value pairs use `fmt.Fprintf` with a 20-character label width; tables (hotspots) use fixed-width column formats. No color codes — output is safe to pipe or capture in CI.

---

## The CLI surface (`pkg/cli`, `pkg/command`, `pkg/status` and `pkg/dashboard`)

`--help`, `--list` and `--status` are served from two public packages, for the same reason as `pkg/explain`: a wrapper binary must be able to print *its* version of each without restating the shared text. Both extension points are plain data, so nothing a wrapper adds is known here.

**Subcommands vs. flags.** Subcommands are dispatched *before* the flag loop, and each owns a `flag.FlagSet`. The loop itself is an exact-match switch over `os.Args` and cannot parse `--flag=value`, so anything taking its own flags has to be a subcommand. The loop's `default:` case **validates** rather than absorbs: a directory is a repository, an existing file is a config, and anything else is rejected. Passing an unrecognized token through as a config path would inherit the leniency of `config.Load`, where an unreadable config is only a *warning* — by design, so a missing `mcp-arch.yaml` falls back to defaults — and every typo would become a silent wrong action rather than an error. So the default-path fallback stays lenient; an *explicitly named* path that does not exist is fatal.

**[`pkg/command`](pkg/command)** implements the subcommands themselves — the gate (`check`, `baseline`), the reports (`coverage`, `doctor`), the installer (`install`/`uninstall`) and the session hooks (`hook`).

They live in a package rather than in `cmd/enola` because a wrapper binary cannot reach them otherwise: they are built on `internal/` packages — baseline resolution, the hook heartbeat, the file lock — which do not cross a module boundary. A separate module could neither import them nor reimplement them without carrying a second copy of the gate's exit-code contract and the hook's silence rules. Keeping them here lets them go on using those internals while a wrapper reaches them through one exported surface. Deliberately *not* folded into `pkg/cli`, which is pure text: anything wanting only `--help` would otherwise pull in the whole engine.

`Runner` holds the `cli.Binary`, so usage lines, error prefixes and — the part that matters — the commands suggested in remedies name the binary that is running. `New(bin, ownSubcommands...)` also records commands the binary dispatches *for itself*: `upgrade` is OSS-only and handled in [`cmd/enola/main.go`](cmd/enola/main.go), but is still offered as a typo suggestion, so `enola upgrad` is not told that upgrade is not a command of any kind. `Dispatch` returns false when the argument was not a subcommand, which is what lets the caller continue into its own flag parsing.

`WithEngine` configures every engine these commands construct. They build their own — a gate has to snapshot the tree it is grading — so a wrapper that registered plugins only on its server's engine would get a plain one here, and the same binary would produce different snapshots depending on which command was invoked. The callback takes only `*bootstrap.Engine` because a caller outside this module cannot name `*config.Config`, but reaches the same value through `Engine.Config()`.

**[`pkg/cli`](pkg/cli)** renders what a binary prints about itself.

- `OSSTools()` is the `--list` catalogue: name plus a one-line summary. It is hand-written on purpose — the descriptions registered with `mcp.AddTool` are multi-paragraph agent prompts, unusable in a terminal. `TestE2E_ToolCatalogueMatchesRegisteredTools` ([`internal/server/e2e_test.go`](internal/server/e2e_test.go)) asserts set equality against the tools the running server actually registers, so the two cannot drift. `RenderToolList(ToolListSpec)` renders it; a wrapper passes its own tools in `Extra` (or an unlock note via `ExtraLocked`/`LockedNote`), and with a zero spec the output never mentions that a wrapper exists.
- `DefaultHelp(Binary)` returns the `HelpSpec` shared by every enola binary — usage, flags, config path, examples, MCP configuration, build — with the binary's name, command package and version ldflag substituted in. A wrapper appends to `Commands`/`Flags`/`Sections`, qualifies a shared flag with `AppendFlagNote`, and places its own blocks precisely with `InsertSectionsBefore`. `RenderHelp` does the layout (a 22-column description gutter, with continuation lines aligned to it).

**[`pkg/status`](pkg/status)** is the usage tracker behind `--status`. `Tracker.OnToolCall` is registered as the server's tool callback (`bootstrap.Server.SetToolCallback`) and attributes each call to the repo it actually operated on, not to a fixed one. Counters live in `~/.enola/usage/<repo-base>-<hash8>.json` — outside the repo, so they survive both a restart and deleting `.enola/`. `AggregateServer` collapses every file into the server view `PrintStatus` renders; `AggregateUsage` produces the per-repo breakdown for `--status --all`.

### Many servers, one home directory

Agent tooling starts one MCP server per session, so several run concurrently — different repos, sometimes the same repo, both binaries — and they all share `~/.enola`. Two mechanisms keep that from turning into cross-talk.

**Shared files are merged, not overwritten.** A repo's usage file has many writers. `Tracker.flush` therefore holds a cross-process lock ([`internal/filelock`](internal/filelock)), re-reads the file, and adds only the increments this process has not yet flushed. Writing `baseline + ownSession` instead — as it once did — silently discarded whatever a sibling had recorded in between.

**Per-process state lives in files with one writer.** Everything that describes a *process* rather than a repo — PID, uptime, dashboard port, the repos it holds, its own call counts — is published to `~/.enola/instances/<pid>-<startNano>.json` ([`instance.go`](pkg/status/instance.go)), refreshed on every tool call and by a 30s heartbeat, and removed by `Tracker.Close`. `LiveInstances` reads them, and reaps records whose process is gone (or whose PID is live but long unrefreshed — a PID-reuse guard), so a hard-killed server disappears at the next read. The start-time suffix means a recycled PID cannot resurrect a dead record.

That registry is what lets `--status` list every running server with its own URL, and what lets a dashboard name the process serving it instead of guessing from the newest usage file — a guess that, with several terminals open, was usually a different process.

The value estimate `--status` and the dashboard render lives in [`pkg/status/value.go`](pkg/status/value.go) and has its own section below — see [The value model](#the-value-model).

**[`pkg/dashboard`](pkg/dashboard)** serves the read-only page described in the README, on a loopback port bound at startup (`--no-dashboard` skips it). It is **strictly a viewer**: `buildPage` runs per request and every source is read through an accessor the MCP tools already use — `Options.Tracker`, the engine's published store, and the receipt/insight artifacts (preferring the in-memory copy, falling back to the last-written file on disk, since `AutoLoadSnapshot` restores facts without full receipt metadata). Nothing it does mutates server state, and every source degrades to an explanatory note rather than an error page.

**Whose data is on the page.** Each figure has exactly one legitimate source, and mixing them is what made the page misleading when several servers ran at once:

- *This process* — PID, uptime, binary, workdir, repos loaded, calls served — comes from `Options.Tracker.Self()`. Never from `status.ServerSnapshot()`, whose scalar fields describe only one arbitrary instance.
- *This process's graph* — the repo list and counts — comes from `bootstrap.Engine.GraphReceipt()`, assembled in memory. Reading the machine-wide `~/.enola/receipt.json` here (as it once did) let the repo list describe one server's graph while the services, insights and coverage panels below it described another's.
- *Genuinely cross-process* figures — the lifetime tool totals and the tracked-repo count — still come from `ServerSnapshot()`, and the page labels them as such.
- *The other servers* come from `status.LiveInstances()`, rendered as a table of links so any dashboard is an entry point to all of them.

**The shared URL.** Each server binds its own ephemeral port, then competes for a fixed one (`DefaultStablePort`, overridable via `ENOLA_DASHBOARD_PORT` or `dashboard.port`; see `ResolveStablePort`). The winner serves the same mux from both and flags itself `FrontDoor` in the registry; the losers retry every few seconds, so when the holder exits another picks the port up and the bookmark survives. There is no coordination beyond the OS refusing the second bind.

The page reads receipts into the engine's own `facts.Receipt` / `facts.GraphReceipt` types, re-exported from `pkg/facts` precisely so a consumer never hand-writes a JSON mirror that drifts.

**The insight allowlist.** `insightLabels` in [`pkg/dashboard/insights.go`](pkg/dashboard/insights.go) maps explainer id → display label, one entry per explainer `bootstrap.NewEngine` registers, and it doubles as an admission list: `insightDetails` drops any insight whose `Source` is absent from it, and excludes it from the structural/candidate counts. This matters because both binaries share a repo's `.enola/insights.json` — without the filter, a file written by a build with extra explainers would surface findings this engine cannot produce. For the same reason the clickable insight counters render the *filtered* total rather than the receipt's raw `insight_count`, so the number you click always matches the list you get.

**The overlay.** A wrapper adds panels through `Options` rather than by forking the page. `Overlay` is a template fragment redefining any of the blocks `OverlayBlocks()` publishes — `extra-styles`, `extra-cards`, `extra-modals`, `extra-scripts` — each of which is passed the page root, so the fragment reaches its own data as `{{.Extra}}`, computed per request by `Options.Extra(store)`. `InsightLabels` widens the allowlist above (a wrapper that registers explainers *must* use it, or its own findings are filtered out of its own dashboard) and `Title` names the product. Two tests hold the contract from both ends: `TestOverlayBlocksExistInPage` here, and a matching check in the wrapper that its fragment defines only published names. Note that every server gets its own `Clone` of the base template even without an overlay — `html/template` refuses to clone a template that has already executed, so rendering straight from the package-level base would break the *next* dashboard's construction.

---

## The value model

`--status`, `--status --all` and the dashboard all report an estimate of what enola saved you, in two currencies: **tokens** and **time**. The model lives in [`pkg/status/value.go`](pkg/status/value.go) and is computed from real per-tool call counts recorded under `~/.enola/usage/`. This section says exactly what those numbers mean, because a savings figure that nobody can explain is worth less than no figure at all.

### What is being counted

The estimate answers one question:

> **What would an agent have had to ingest to arrive at the same answer using ordinary tools — grep, glob, open a file, read it, infer?**

That counterfactual, not the size of enola's own response, is the baseline. The distinction matters. A `query_facts` call that returns 12 KB of JSON has not "cost" you 3,000 tokens — it has *replaced* an exploration loop that would have grepped, opened a dozen files, and re-derived the same edges imperfectly. Pricing the response instead of what it displaced would measure the wrong thing entirely, and would perversely reward tools that answer less.

The counterfactual is also why the model is anchored to **corpus size**, not to call count. Reconstructing a graph of the Linux kernel (218M tokens of parsed source, 1.9M facts, 55,399 files) and reconstructing one of a small service (17.9K tokens) are not the same act of work. Any flat per-call price is wrong at both ends by four orders of magnitude, so there isn't one.

### The corpus anchor

Every figure derives from one measurement: **the token size of the source enola actually parsed**, computed by `ScanFootprint` in [`pkg/status/footprint.go`](pkg/status/footprint.go) and cached per repo in the usage file.

Two properties of that measurement are load-bearing:

- It counts **parsed source only**, not `files_seen`. A snapshot's `file_hashes` list covers everything the walker touched, including whatever else lives in the tree. On one real repository the two differ by 39× — 3.1M tokens of source against 121M once JPEGs, an MP4 or two and a 63 MB GeoIP database are folded in. An agent was never going to read those, so they are not corpus.
- It is a **measurement, not a guess**. Nothing else in this model is allowed to invent a corpus size.

Measured across the workloads enola is actually pointed at, the spread the model has to cover is over **12,000×** — from a 17.9K-token service to a 218M-token kernel:

| Workload | Parsed source | Facts | Wall clock |
|---|---:|---:|---:|
| Mid-size project (Python + TS) | 1.77M tokens | 16,909 | 0.8s |
| Backend service (Go + Python) | 3.12M | 23,716 | 1.5s |
| Apache Airflow (Python + TS + Java + gRPC) | 8.54M | 67,702 | 4.9s |
| **8-repo product ecosystem** (5 languages, backend + web + iOS + Android) | **8.45M** | 155,796 | 8.2s |
| **GitLab** (Ruby + TS + Python + Java) | **32.77M** | 435,033 | 22.3s |
| **Linux kernel** (C + Rust) | **218.15M** | 1,892,343 | 2m20s |

Every figure there is the engine's own measurement, taken on a fresh snapshot; the ecosystem row sums its eight repos.

The rendered digest (`llm_context.md`) is capped at ~16K tokens regardless of any of it. For GitLab that is a **2,048:1** compression of the corpus into the artifact an agent actually reads; for the kernel, **13,600:1**.

### Tokens

```
first snapshot     = corpus_tokens × rediscoveryFactor
additional repo    = above + priorCorpusTokens × crossRepoPremiumFactor
refresh (unchanged)= refreshConfirmCredit
refresh (changed)  = changed_fraction × corpus_tokens × rediscoveryFactor + refreshConfirmCredit
query tools        = weight × tokensPerManualOp × queryCorpusScale − response_tokens
```

Credit is never negative, and a failed call earns nothing at all.

**A repo is never credited more than its own corpus.** Reading every line of it is the most an agent could possibly have ingested on its account, so that is the ceiling — the cross-repo premium tops a snapshot up toward it but never past it. Without that bound, a 4K-token service joining a 50M-token graph would earn hundreds of times its own source. The bound buys a property worth stating plainly: **cumulative snapshot credit never exceeds the size of the code itself.** If a total ever reads higher than the corpus it was computed over, the model is wrong, not the codebase.

The premium is also scoped to the graph actually loaded. A non-append snapshot resets the store, and the corpus pool resets with it — a repo joining a small cluster is credited for edges against that cluster, not against everything indexed earlier in the session.

`rediscoveryFactor` (< 1) encodes that an agent doing this by hand does **not** read every file. It reads a lot of them, greps the rest, and stops when it thinks it understands enough — usually before it actually does. Crediting the full corpus would assume an exhaustiveness no agent exhibits.

**The cross-repo premium** is not a bonus for doing more work; it prices a different *kind* of result. A cross-repo edge — an iOS client calling a route served by its backend — can only be derived with both corpora resident at once. That is why `append` is priced above a second independent snapshot, and it leads directly to the threshold below.

**The infeasibility threshold.** When the corpus that must be simultaneously resident exceeds `agentContextWindow`, the counterfactual is not *expensive* — it is **impossible**. The 8-repo ecosystem above resolves its cross-repo edges across 8.45M tokens held at once; GitLab is 32.77M, and the Linux kernel 218.15M — 218× a single window. No amount of patience or budget gets an agent there by re-reading files, because it cannot hold the sides of the comparison in the same context.

Such entries keep their numeric credit (so totals stay arithmetic) but are **flagged in the output** rather than quietly rendered as a large number. A footnote saying *"exceeds a single context window — not reproducible by re-reading"* is a stronger and more honest claim than any figure, and it is the case both large workloads in the table above fall into.

**Refreshes are not waste.** Re-running `generate_snapshot` on an unchanged repo looks redundant and isn't: it answers *"is my understanding of this system still valid?"*, which is the question [`internal/engine/freshness.go`](internal/engine/freshness.go) exists to answer and which is genuinely expensive to settle by hand — you must re-derive the graph to know whether it moved. So an unchanged refresh earns a small fixed credit (real, but far below a first build), and a changed one is scaled by the fraction of files whose hashes actually moved. The `snapshot_id`, `git.commit` and `config_hash` recorded in `snapshot.meta.json` are what make that distinction exact rather than heuristic.

**The ledger has the final say on "first".** Whether a repo has been built before is answered by the usage history, not by the repo's `.enola` directory. Those are different questions with different lifetimes: repo-local metadata says *"is this graph on disk current?"* and travels with the working copy — it survives clearing `~/.enola`, and it arrives with a fresh clone. The value model asks *"has this installation ever been credited for building this graph?"*, which only the ledger can answer. So a snapshot the server reports as unchanged is still priced as a first build when no snapshot credit exists for that repo yet. Without that rule a leftover meta file suppresses the credit permanently, and resetting your statistics silently makes every subsequent build look free.

Note that "unchanged" is decided on **file hashes**, not on the snapshot id alone. An id can move without any source moving — a version bump, a config change — and that earns confirmation credit rather than a full rebuild's worth. Two distinct cases are therefore carried explicitly: a changed-fraction of exactly zero means *the files are identical*, while a **negative** fraction means *no previous snapshot to compare against*, which is a genuine first build. They must not share a default — one of them is worth a whole corpus and the other is worth a few thousand tokens.

For the **query tools**, `weight` remains an ordinal judgement — how much exploration one call displaces, relative to the others — while `tokensPerManualOp` converts it into tokens. That constant is calibrated to the **median** parsed source file across the corpora above (~800 tokens), not the mean, which outliers inflate by roughly 2.5×. Response tokens are subtracted, which is what makes the `output_mode` ladder visible in the estimate: `summary` mode genuinely saves more than `full` mode, and the model should say so.

**Queries scale with the graph they searched.** The work a query replaces is driven by the size of the haystack, not the number of needles: asking *"where are the dependency cycles"* over Linux (1.9M facts across 55,399 files) displaces far more grepping than the same question over a small service, even though it returns fewer findings. So the ordinal weight is multiplied by `queryCorpusScale`, taken over the whole loaded graph rather than one repo — a query is not scoped to a single repo, so neither is its pricing.

The growth is **logarithmic and capped**. A haystack 100× larger does not take 100× the greps, just a few more rounds of them, and linear scaling would hand a single query millions of tokens on a kernel-sized graph:

| Graph | Corpus | Scale | One `query_insights` |
|---|---:|---:|---:|
| Small service | 17.9K | 1.00 | 10,743 *(corpus-capped)* |
| Mid-size project | 1.77M | 1.00 | 24,000 |
| 8-repo ecosystem | 8.45M | 3.23 | 77,532 |
| GitLab | 32.77M | 5.19 | 124,472 |
| Linux kernel | 218.15M | 7.92 | 190,107 |

Below `queryScaleReferenceCorpus` the weight stands unscaled; `maxQueryCorpusScale` binds above ~230M tokens. That cap is the most arbitrary number in the model — it exists to stay conservative at the top end, not because 8 is special.

**The graph's size survives a restart.** A server coming back up restores its facts from disk without re-snapshotting — that is what `AutoLoadSnapshot` is for, and with one server per agent terminal it is the normal path, not an edge case. So the measurement those facts were extracted from has to come back with them, or every query on a restored graph is priced as though the graph were tiny. Each repo's parsed-source size is therefore carried in the graph receipt (`GraphRepoEntry.SourceBytes`), and `AutoLoadSnapshot` returns it from the *same* receipt the facts came from — resolving one independently could size the graph by a repo set the server is not actually holding.

The field is additive and `omitempty`: a receipt written before it existed reads as zero, which is treated as *unknown* rather than as an empty corpus, and heals on the next snapshot. It is not an input to `computeSnapshotID`, so snapshot fingerprints are unaffected. And because a repo's size is read from its own `snapshot.meta.json`, a momentarily unreadable file yields zero — which the receipt merge treats as "no new reading" and carries the last known size forward, rather than writing the gap through and silently un-pricing that repo.

**And a query is capped by its graph, like a snapshot is by its repo.** No call can displace more work than reading outright everything it searched, so query credit is bounded by `corpus × rediscoveryFactor`. That bound is what the top row above hits: a single `query_insights` priced at its ordinal weight would otherwise be credited 24,000 tokens against a repo whose entire source is 17,906.

### Time

Time saved is **your** time — a human waiting on an agent — not CPU time and not model throughput:

```
time_saved = (tokens_avoided / agentTokensPerSecond) × reworkFactor
```

`agentTokensPerSecond` is end-to-end discovery throughput: tool round trips and reasoning included, not raw generation speed.

`reworkFactor` is the **non-determinism premium**, and it is the honest core of enola's pitch. An agent re-deriving your architecture from grep does not merely take longer — it gets things subtly wrong often enough that some fraction of the work is done twice: the migration attempted a second time, the caller missed until code review, the refactor undone. enola returns the same graph every time. Pricing that as a named, tunable multiplier is better than burying it inside a per-tool weight where nobody can find or argue with it.

### The constants

All seven live at the top of [`pkg/status/value.go`](pkg/status/value.go) so tuning is a one-line change. Where a range was defensible, the lower end was chosen:

| Constant | Meaning |
|---|---|
| `rediscoveryFactor` | Fraction of a corpus an agent actually ingests before it stops. |
| `crossRepoPremiumFactor` | Share of the already-loaded corpus credited for resolving a new repo's edges against it. |
| `refreshConfirmCredit` | Per-repo credit for establishing that nothing changed. |
| `tokensPerManualOp` | Median parsed source file, in tokens. |
| `queryScaleReferenceCorpus` | Graph size at which a query's ordinal weight stands unscaled. |
| `maxQueryCorpusScale` | Ceiling on that multiplier, so huge graphs cannot run away. |
| `agentTokensPerSecond` | End-to-end agent discovery throughput. |
| `reworkFactor` | Non-determinism premium — work done twice. |
| `agentContextWindow` | Threshold past which the counterfactual is infeasible. |

### A worked example: the Linux kernel

The extreme end of the table — one `generate_snapshot` over the kernel's 55,399 parsed files, then three reads:

```
Value Estimate (approximate):
  tool                calls   ~time saved   ~tokens saved
  explore                 1            6s           11.2K
  generate_snapshot       1  21h 48m 52s           130.9M
  query_facts             1            3s            6.3K
  query_insights          1           14s           23.4K
  TOTAL                   4  21h 49m 16s           130.9M†
  running now             4  21h 49m 16s           130.9M

  † corpus exceeds a single context window — not reproducible by re-reading files.
```

Four things in that output are worth reading closely:

- **130.9M is exactly `218.15M × 0.6`** — the corpus times `rediscoveryFactor`, with no premium because it is a single repo. Nothing about the call count produced that number.
- **The ratio of snapshot to reads is 3,200:1.** On a codebase this size, building the graph *is* the work; the queries are rounding error against it. That is the model agreeing with intuition rather than being tuned to.
- **21.8 hours is a floor, not an estimate of the real cost.** It is the agent-throughput conversion of tokens avoided. Nobody was going to ingest 130.9M tokens — people spend months learning a kernel — so the figure understates the true difficulty by a wide margin. It is bounded by what the model can defend, not by what the work is worth.
- **The dagger is the honest claim.** At 218.15M tokens the corpus exceeds a single context window by **218×**. The token figure describes a counterfactual that cannot be performed at all, which is why a flagged row says more than its number does.

Two operational notes follow from the same run, and they matter before pointing enola at a tree this size: the snapshot writes an **830 MB `facts.jsonl`**, and it produces **37,425 insights** — far past what anyone triages by hand, so `min_confidence` and an explainer filter are the sensible default posture rather than an afterthought.

At the other end of the scale, re-running an identical session over an already-built graph collapses to confirmation credit — roughly 1.7K per repo instead of its corpus. That gap, for byte-identical commands, is the model distinguishing building an understanding from confirming one still holds.

### What it deliberately does not model

- **Reasoning tokens.** Only ingestion is priced. The thinking an agent does *about* what it read is not counted, on either side of the comparison.
- **Cache warmth.** A repeat read inside one session is cheaper than a cold one; the model ignores this in enola's disfavour.
- **Downstream consequence.** The missed caller found in code review, the afternoon spent reconstructing how two services talk, the design decision made on a wrong mental model. `reworkFactor` prices a slice of this; the rest is not attempted.
- **Value not yet consumed.** A graph built and never queried is credited for the build. It is a durable, reusable artifact and the credit is for producing it — not a forecast that it will be used.

### Extending it

`RegisterToolWeights` lets a wrapper binary price the tools it adds instead of letting them fall through to `defaultWeight` — a licensed wrapper registers its own at startup, before the server begins serving. The map is mutex-guarded because registration happens once at startup while reads come from the tool callback and, in wrapper binaries, from concurrent HTTP handlers.

One rule matters here: **the credit is persisted alongside the count**, not repriced at render time. Every binary shares `~/.enola/usage/`, and a build that has never heard of a tool would price it at `defaultWeight` — so the same usage file would report a materially different figure depending on which binary you ran `--status` from. Recording the token figure when the call happens is what makes the number a property of the data rather than of the reader.

---

## The tools

enola is a stdio [MCP](https://modelcontextprotocol.io/) server. It exposes **thirteen tools** and no MCP resources — everything flows through tool calls. The tools defined in [`internal/server/server.go`](internal/server/server.go) are listed below, each leading with the question it answers.

> Most read tools share a **token-cost ladder** via `output_mode`: `summary` (smallest, aggregated counts) → `compact` (markdown, grouped) → `full` (raw JSON, can be large). Start with `summary` and escalate only when you need node-level detail. Most also accept `max_tokens` to hard-cap a response.

### `generate_snapshot` — "index this repository"

Parses a repo and builds the fact graph. **Run this first**; re-run after code changes.

| Parameter | Description |
|-----------|-------------|
| `repo_path` | Path to the repository. Defaults to the configured repo. |
| `append` | If `true`, keep existing facts and add a new repo with repo-prefixed paths (for cross-repo analysis). enola auto-enables append when it detects you switched repos. Default `false`. |
| `fresh` | If `true`, force a clean **single-repo** snapshot: reset the store (discard any loaded repos) and index only `repo_path`, bypassing the auto-append heuristic. Use it when you've moved to a *different* project and don't want it merged into the current multi-repo store. Mutually exclusive with `append`. Default `false`. |

> **The auto-append escape hatch.** enola auto-enables append when a plain `generate_snapshot` targets a different repo than the one currently loaded — convenient when you forgot `append=true` on repo #2 of a multi-repo set, but a footgun if you actually switched projects (it silently merges the new repo into the existing store). The auto-append is announced loudly in the response with the remedy; pass `fresh=true` to force a single-repo reset instead.

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

The `summary` mode also surfaces a `## Flags` section tallying notable boolean props present in the result set (currently `unmatched_by_clients`), so sizing a route query reveals the dead-route signal — and the exact follow-up query — without already knowing the prop name.

### `query_insights` — "what did the analysis find?"

Returns the architectural findings the explainers computed during `generate_snapshot` — the first-class way to ask "which routes are unused?", "where are the cycles?", "which modules are god-classes?" instead of re-deriving them from raw facts. Each insight carries a title, the explainer that produced it ([`Insight.Source`](internal/facts/model.go)), a description, a confidence (`1.0` = structural fact, below = heuristic candidate), evidence (files/symbols/routes), and suggested actions. All explainers populate insights, but route/cross-repo findings (`unused-routes`, `crossrepo`, `coverage`) only appear for multi-repo (append-mode) snapshots.

| Parameter | Description |
|-----------|-------------|
| `explainer` | Filter to one explainer: `unused-routes`, `cycles`, `layers`, `crossrepo`, `coverage`, `god-class`, `hotspots`, `dependency-depth`, `exported-surface`, `complexity-outliers`. Empty = all. |
| `repo` | Best-effort filter to insights about one repo label (substring match over title + evidence files). |
| `min_confidence` | Only return insights at or above this confidence (0.0–1.0). |
| `output_mode` | `summary` (default, one row per insight) → `compact` (adds description, evidence sample, actions) → `full` (complete JSON). |
| `max_tokens` | Optional hard cap. |

`query_insights(explainer="unused-routes")` returns the per-service dead-route candidates directly, with the out-of-snapshot caveat and suggested actions attached — see [Finding unused endpoints](#finding-unused-endpoints).

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

### `coverage_report` — "which cross-repo edges did enola resolve, and which did it miss?"

Per-service edge-coverage report, so you can tell a genuinely isolated service from one whose outbound edges enola simply couldn't resolve. For each `service` node it shows resolved outbound dependencies and, per edge type (currently `http_client`), how many call sites were detected, resolved to a loaded service, and left unresolved — then classifies the service as `connected`, `coverage_gap` (no resolved edges but unresolved call sites detected — likely *not* isolated), or `isolated` (a genuine leaf). Use it before concluding a service stands alone. Multi-repo only; single-repo snapshots have no service nodes. Surfaces the `coverage` explainer's underlying `edge_coverage` counts.

| Parameter | Description |
|-----------|-------------|
| `repo` | Optional: limit the report to one service (repo label). Default: all services. |
| `output_mode` | `summary` (default) returns a markdown table; `full` returns JSON. |

---

### `set_baseline` — "remember the architecture as it is now"

Pins the current snapshot as a diff baseline by copying the snapshot artifacts (`facts.jsonl`, `insights.json`, `snapshot.meta.json`, `receipt.json`) into `.enola/baseline/`. The receipt rides along so the pinned baseline carries its own provenance, which is what makes the comparability check below possible from either side. Call it once at the start of a task, after the first `generate_snapshot`. The pinned baseline **survives subsequent `generate_snapshot` runs**, so it stays valid across several rounds of edits — unlike the auto-rotated `.enola/previous/`, which only ever holds the immediately-preceding run. Takes no parameters.

> The CLI equivalent, `enola baseline pin [repo|config]`, **snapshots first and then pins** — there is no separate `--generate` step. The MCP tool cannot do that (the agent has just generated, and re-generating inside the tool would be surprising), but from a shell "pin a baseline of this repo" is one intent, and pinning whatever happened to be on disk risks freezing a days-old snapshot as "the state before my change" — precisely the staleness the diff then warns about.

**Publishing a baseline is atomic.** The artifacts are staged in a sibling temp directory and renamed into place, never copied file-by-file into `baseline/`. Two properties follow:
>
> - **A partially-written baseline is never observable.** `LoadSnapshotDir` accepts any directory containing `facts.jsonl`, so a half-copied baseline would read as a real one and be diffed against — silently producing a wrong delta rather than an error. An *absent* baseline is handled everywhere; a partial one is not.
> - **A failed pin leaves the previous baseline intact.** Copying in place would overwrite the old artifacts before discovering it will fail, leaving a baseline that is neither the old one nor the new one.
>
> Replacing rather than overlaying also means the directory holds exactly the current artifact set, so a file written by an older enola does not survive indefinitely because nothing overwrites it. The swap is remove-then-rename, leaving a brief window where the directory does not exist — deliberate, since renaming a directory onto a non-empty one is not portable, and absent is the safe state.

### `diff_snapshot` — "what did my change actually do?"

Computes the architectural **delta** between a baseline snapshot and the current one. This is the verification counterpart to `impact_analysis`: where impact analysis *plans* a change, diff_snapshot *confirms* it, replacing "re-read the files to check what got built" with a deterministic answer.

It is a **delta, not a linter**: it judges the current snapshot against the codebase's own prior state, not an external ideal, and reports only what *changed* —

- **findings that newly appeared** (regressions introduced — a new cycle, layer violation, god-class, unused route, …) and **findings that were resolved**, each carrying through its original confidence and caveats untouched;
- **new and removed coupling edges** (the architecturally interesting structural change);
- **added / removed** modules, symbols, routes, storage; and props-level **changed** facts (line-only shifts are ignored, so an edit above a symbol doesn't churn the diff).

Because the baseline is the project's own prior snapshot, a pattern that was present *before and after* (e.g. an API-first route with no loaded consumer) produces no delta — the diff is structurally immune to that false-signal class. `Compute` is pure and deterministic: identical inputs render byte-identically.

Two refinements keep the finding delta honest, both learned from dogfooding on a real backend:

- **Stable finding identity.** A finding is keyed by its explainer plus its number-normalized title (the stable subject), so a god-class whose fan-in merely ticked up — or a whole-codebase summary finding like the `layers` pattern, whose evidence enumerates every module — does not churn as resolve+introduce on an unrelated edit. Cycles are the exception: their title carries only a member count, so they are keyed on their sorted member modules.
- **Structural-cause classification.** A new/resolved finding is reported as a real **regression introduced** / **improvement** only when the change actually touched one of the entities it cites (a fact added/removed/changed, or an edge endpoint — so a finding that flips because a *new caller* changed a symbol's fan-in still counts). Findings that appear or clear with no structural cause — a moving `mean+2σ` threshold, or a top-N list re-ranking after a worse offender left the window — are routed to a separate **incidental finding shifts** section so they never masquerade as something the change caused.

**Working-tree drift (`internal/drift`).** A delta computed from a snapshot that no longer matches the tree describes neither the snapshot's state nor the tree's, and that is the one caveat which invalidates it outright. `drift.AddWarning` re-walks and re-hashes the repository and attaches a comparability warning when it has moved; a snapshot with no recorded hashes reports "cannot verify" rather than staying silent, because an unanswerable check must not read as a clean bill of health.

It is its own package because both sides of the module boundary need it and neither can reach the other: `internal/server` cannot import `pkg/bootstrap` (which imports the server), and an out-of-module consumer computing its own delta cannot import `internal/server` at all. A leaf depending on `engine` and `diff`, depended on by both, is the only shape with no cycle — and one implementation is the point, since two tools disagreeing about what "stale" means is itself a kind of drift. Exposed to wrappers as `bootstrap.AddDriftWarning`. It re-hashes the repo, so it belongs at a deliberate decision point rather than on every tool call.

**Conformance (`target` / `expected_packages`, `internal/conformance`).** Optional, and the one question the delta itself cannot answer. `diff.Compute` is a function of two snapshots, so it can report what changed and nothing about what the caller *meant* to change. Declaring a target adds that third input: reverse-dependency impact analysis runs over the **pre-change** graph, and any package the change reached outside the predicted radius is reported as **spillover** — a package altered by something the declaration did not describe.

It lives beside the delta rather than inside it, for the same reason: folding a third input into `Compute` would make every diff depend on something most callers never supply. Two behaviours are load-bearing. With nothing declared, the packages the author edited are taken as the statement of intent, or every undeclared multi-package change would report a false conformance failure. And a package counts as *reached* when it gains an outbound dependency even if none of its own facts moved — the coupling case, which is the one most worth catching.

`enola check` exposes the same thing as `--target` / `--expected`, reported as a policy **measurement** rather than graded in place, so whether spillover fails a build is decided by `pkg/check`'s policy alongside everything else rather than by a second grader.

The typical loop is `generate_snapshot → set_baseline → edit → generate_snapshot → diff_snapshot`.

| Parameter | Description |
|-----------|-------------|
| `baseline` | What to compare against: `pinned` (default — the `set_baseline` snapshot), `previous` (the immediately-preceding run, auto-rotated into `.enola/previous/`), or an explicit path to a directory holding `facts.jsonl`. |
| `focus` | Optional: narrow the report to entries referencing a module/file/symbol (substring), to verify just what you touched. |
| `output_mode` | `summary` (default — headline regressions/improvements + structural tally) → `compact` (adds finding descriptions, evidence, and the changed edges/facts) → `full` (complete JSON). |
| `max_tokens` | Optional hard cap on output size. |

The engine lives in [`internal/diff`](internal/diff/diff.go) (pure `Compute` + deterministic renderers) and is re-exported for out-of-module use via [`pkg/diff`](pkg/diff/diff.go); baseline persistence, the on-disk loader and the shared selector resolution (`ResolveBaselineDir`) live in [`internal/engine/baseline.go`](internal/engine/baseline.go). `diff_snapshot` also runs the **comparability guard** (`diff.CompareMeta`): it reads the receipt fields on both snapshots' metadata and warns, above the delta, when they were not generated over equivalent inputs.

#### Comparability is a spectrum, not a boolean

`Comparability` carries `Comparable bool` (invariant: `Comparable == (len(Warnings) == 0)`), free-text `Warnings`, and a **set of `Kinds`** categorizing them. The kinds exist because the boolean spans everything from *"these are different repositories, the delta is meaningless"* to *"the baseline is four days old, the delta is real but also contains the repo's own drift"*. A human reader can weigh that from the prose; a **gate cannot** — and consuming `Comparable` would turn every stale baseline into a hard refusal, contradicting the deliberate design of `staleBaselineDays`.

| Kind | Raised when | Treated as |
|------|-------------|-----------|
| `different_repo` | the two snapshots are of different repositories (see [Repository identity](#repository-identity-portable-baselines) — *not* merely a different path) | blocking |
| `version_mismatch` | different enola versions (extractor changes read as churn) | blocking |
| `extractor_set` | a language present on one side only | blocking |
| `ignore_globs` | the set of files parsed changed | blocking |
| `unclassified` | contributed via `AddWarning` by a caller that knows something this package cannot (notably `engine.Drift`) | blocking — a gate must **fail closed** on a caveat it cannot categorize |
| `inverted_pair` | the baseline is *newer* than the current snapshot | usage error (concrete remedy: re-generate) |
| `stale_baseline` | the baseline is ≥ 3 days older | **advisory** — warn and still grade |
| `pre_receipt` | the baseline predates snapshot receipts | advisory |

`Kinds` is a **set**, not a per-message list, so `Warnings` keeps its type and JSON shape for every existing consumer (the dashboard, `output_mode='full'`, and out-of-module readers via `pkg/diff`). Callers that know their category should use `AddWarningKind` rather than `AddWarning`.

> Note on timestamps: `GeneratedAt` is RFC3339, i.e. **second** resolution, so a baseline pinned and then diffed inside the same second yields a zero gap. Zero is *simultaneous*, not inverted — `inverted_pair` requires a strictly negative gap. Treating zero as inverted made a no-op check on an untouched repository report "the current snapshot does not contain your change".

#### Repository identity (portable baselines)

A baseline is only useful in CI if it can be **pinned on one machine and graded on another**. That makes "are these the same repository?" a question about *identity*, not *location* — and comparing absolute `RepoPath` answered it with location. A baseline pinned at `/home/runner/work/app/app` and restored against `/Users/dev/src/app` always tripped `different_repo`, which the gate treats as blocking, so a downloaded baseline artifact could never grade. (The delta underneath was correct the whole time; only the verdict was wrong.)

`facts.SameRepo` ([`internal/facts/repoidentity.go`](internal/facts/repoidentity.go)) decides it from two signals, strongest first:

1. **Normalized git remotes**, when both snapshots have one. `facts.NormalizeRemote` reduces a remote URL to `host/path` — scheme, credentials, port and a trailing `.git` removed, lowercased — so every way of cloning one repository collapses to one identity: `git@github.com:org/app.git`, `https://github.com/org/app`, and a CI URL carrying an injected token all normalize to `github.com/org/app`. Without that, a runner cloning over HTTPS and a developer over SSH would read as two repositories.
2. **The checkout directory name**, otherwise. Weaker — two unrelated repositories both checked out as `api/` look alike — but it is what exists for a repo with no remote, and it is strictly better than the absolute path it replaces. It compares the last segment across *either* separator, because a baseline written on a Linux runner and read on Windows carries the separator of the machine that **wrote** it.

`GitInfo.Remote` is populated by one more read-only `git remote get-url origin` alongside the three calls already in `gitInfo()`, and degrades to empty exactly as they do (no git, no repo, no origin). It is normalized again on read, so a baseline written by an older build — carrying a raw URL, or no remote at all — still compares correctly. **A remote rather than the root commit**: `git rev-list --max-parents=0` looks like the purer identity, but it is unreachable in a shallow clone, which is what `actions/checkout` does by default.

Both absolute paths stay in the warning text, and it names which signal decided, because the remedies differ: differing remotes mean the wrong baseline was fetched; differing directory names on one repository mean a checkout was renamed.

---

### The gate (`pkg/check`) — `diff_snapshot` as an exit code

[`pkg/check`](pkg/check/check.go) is a thin, **pure** policy layer over `internal/diff`: `Compute` decides *what changed*, `Evaluate(*diff.SnapshotDiff, Policy, ...Measurement) Verdict` decides whether that is allowed to break a build. Same delta plus same policy always yields the same verdict, so a gate is as reproducible as the snapshot underneath it. It backs the `enola check` CLI, and exists as a public package so a wrapper can build a graded verdict on top of it rather than re-deriving one.

**Measurements** are the seam for anything the delta itself does not carry. A caller that ran its own analysis over the two snapshots — a conformance check, or an analyzer this build does not ship — reports a `Measurement` (a named count with a human label); the policy's `Threshold`s decide what that count *means*. Grading stays in one place while measuring stays with whoever can measure, which is the property that keeps two surfaces from disagreeing about the same change: neither gets to pick its own severity.

`Evaluate` is variadic and `Policy.Thresholds` is empty by default, so a verdict with no thresholds is byte-identical to one computed before measurements existed — anything that would newly fail a build has to be asked for. Two details earn their keep: measurements are carried in the `Verdict` even when no threshold gates them (otherwise a caller cannot tell "under the bound" from "nobody looked"), and the FAIL headline counts threshold breaches alongside failing findings, so a change failing only on a measurement does not report zero regressions.

`Status.ExitCode()` is the contract with CI: `0` clean · `1` regression · `2` usage error · `3` incomparable. Precedence is **blocking → usage error → regression**; blocking comes first because when the snapshots were built over different inputs, the inverted-pair remedy ("re-generate") would send the caller down the wrong path. Nothing is hidden by the ordering — every warning is reported regardless of which decided the status.

**Why the policy keys on the explainer rather than on confidence.** Confidence alone is not enough to name what should break a build, even though [Insights](#insights-explainers) guarantees that `1.0` is a structural fact: it says how strong a claim is, not what kind of claim it is. A statistical outlier and a proven cycle are different objects, and only the second is a defect by construction. So the **explainer is the primary filter** (`DefaultFailExplainers = ["cycles"]`) and confidence is a floor applied within it (`DefaultMinConfidence = 1.0`). The floor does real work: `cycles` emits both a true cycle at `1.0` and a "highly coupled module cluster" at `0.4` whose own description calls it "a coupling-density signal, not a defect to break".

> **The two-part filter is what keeps `1.0` meaningful in both directions.** Every explainer that computes rather than proves its confidence clamps at `common.MaxHeuristicConfidence`, strictly below `1.0` — `god-class`, whose score is a fan-in ratio against a statistical threshold, and `layers`, whose pattern confidence is a coverage share. Both saturate on real repositories, and letting them reach `1.0` would have published a statistical outlier as a structural fact to everything downstream that reads the number: the receipt's heuristic-insight count, the dashboard's structural/candidate split, and `query_insights(min_confidence=…)`. A cycle is the only claim enola computes with certainty, and it is the only one that reaches `1.0`.

**New coupling is reported, never failed.** `diff.Edge` is name-level, so `EdgesAdded` is populated by virtually any change — adding a function that calls another adds edges. A gate firing on that would be switched off within a day. Only module-level and cross-repo coupling deltas are worth escalating, and that needs an edge filter that does not exist yet.

---

### Getting the loop used (`pkg/install`, `enola hook`)

A gate nobody runs is not a gate. [`pkg/install`](pkg/install/install.go) writes enola's instructions into the files coding agents actually read, and optionally a hook that closes the loop without anyone remembering to.

**Everything here is built on one constraint: these are the user's files.** Two failure modes drove the design, both observed in comparable tools rather than imagined:

- *Destroying hand-written content.* A section updater that anchors on a heading and takes everything to the next one will, sooner or later, anchor on an inline mention and delete what lies between. enola's block is delimited by explicit `<!-- enola:begin -->` / `<!-- enola:end -->` sentinels, the boundary is never inferred from document structure, and **unbalanced markers are refused rather than guessed at** — the correct response to not understanding a user's file is to stop.
- *Writing where nothing reads.* Config placed at a path the target ignores parses cleanly, looks installed, and silently does nothing. Every target path here was verified against that tool's own documentation before being written to.

That verification changed the design twice.

**Owned files wherever the tool allows one.** Claude Code loads `.claude/rules/*.md` at launch with the same priority as a project `CLAUDE.md`; Cursor has `.cursor/rules/`; Copilot has `.github/instructions/*.instructions.md`. In each case enola writes **a file it owns outright** rather than performing surgery on a document the user hand-maintains — so for three of the six targets the destructive-edit risk cannot arise at all. Only `AGENTS.md` (and the user-level `AGENTS.md` files) are genuinely shared, so only they need the sentinel machinery — and the repo-root one is never *created*, since a tool that drops a new instruction file into a repository uninvited is one that gets removed.

Copilot's frontmatter is load-bearing rather than decorative: `applyTo` is **required** for a file under `.github/instructions/`, and one written without it exists, looks installed, and governs nothing.

**Codex, Copilot and Pi all read the repository's `AGENTS.md`.** So locally they need no file of their own, and enola deliberately writes none — a second repo-local file would put the same instruction into the same context window twice. What they do need is their *user-level* files (`~/.codex/AGENTS.md`, `~/.pi/agent/AGENTS.md`), which reach projects where nobody has run `enola install`. Those are written only when the tool's own config directory already exists: that directory is the evidence the tool is installed, and creating `~/.codex` for someone who does not use Codex would be littering in a home directory to no purpose.

The write primitives are deliberately boring: atomic write via temp-and-rename, deep-equal before writing so a re-run reports `unchanged` rather than churning the file, unparseable JSON backed up and skipped rather than overwritten, and JSON configs mutated in place so every key enola does not know about survives. `install` previews and asks before it writes; `uninstall` restores shared files byte-for-byte and prunes the directories it created.

**`enola hook stop`** ([`cmd/enola/hook.go`](cmd/enola/hook.go)) is what the installed hook invokes. It grades the session's change and emits the verdict as `additionalContext` only when a baseline was pinned *and* the change regressed. Two rules govern it:

1. **It never exits non-zero.** Exit 2 would block the agent, which is not advisory mode's job. Exit 1 is a *non-blocking error* to the harness — so forwarding `enola check`'s regression code would surface as a hook failure rather than as the verdict it actually is.
2. **Every failure is silent.** No baseline, no snapshot, unreadable payload, missing `cwd`, a directory that is not a repository, an unknown event: all exit 0 with no output. enola is a guest in someone else's session, and a broken guest must never look like a broken session.

**`enola hook session-start`** is the other half: it freezes the architecture as a baseline when a session begins, so the loop needs no participation at all.

**The snapshot never runs in the hook.** It costs 0.2 s on a small repository and over ten seconds on a large one, and a session start that stalls for ten seconds is a broken tool however good the report at the other end is. The hook spawns a detached copy of itself and returns immediately, so session-start latency is the cost of one process spawn — measured at the binary's own floor (~17 ms) on a repository that takes ~10 s to index. That is the only mitigation constant in the size of the repo; a `timeout` still pays the timeout.

Four properties make automatic pinning safe to enable at all:

| Guarantee | Mechanism |
|---|---|
| Session start is never delayed | spawn detached, never wait, exit 0 |
| A timeout kill does not kill the snapshot | new session (`setsid`) / detached process group on Windows, so a group-wide kill of the hook does not reach the child |
| Several terminals do one snapshot, not N | non-blocking [`internal/filelock`](internal/filelock) `TryAcquire`; a session that finds the lock held does nothing. Blocking would have turned one redundant snapshot into a queue of them |
| A deliberate baseline is never destroyed | an auto-pinned baseline carries a marker file; one without it was pinned by a person or an agent and is left alone |

That last rule matters more than it looks. A baseline pinned at the start of a multi-day refactor is the "before" of that whole effort — replacing it at the next session start would destroy exactly what it was recording, and the diff would silently start reporting nothing. An auto-pinned baseline is refreshed only when the tree has actually moved, and a **dirty tree is never treated as current**: "dirty" says the content is not identified by the commit, so two dirty trees at one commit may differ arbitrarily.

Every failure path — no git, not a repository, the lock held, a snapshot error, a platform that cannot detach — does nothing and says nothing.

---

### `snapshot_receipt` — "what was this graph generated over, and how complete is it?"

Returns the receipt for the current snapshot (see [The snapshot receipt](#the-snapshot-receipt)): provenance (enola version, git ref + dirty status, content-fingerprint snapshot ID, extractor/explainer sets, ignore-glob hash, output-artifact hashes) and extraction-quality metrics (files seen/parsed/skipped, parse errors, coverage gaps). Read it before trusting an `impact_analysis` or a `diff`, and to spot thin extraction.

| Parameter | Description |
|-----------|-------------|
| `output_mode` | `summary` (default — headline provenance + quality metrics) → `full` (complete JSON receipt). |
| `max_tokens` | Optional hard cap. |

### `compare_receipts` — "are these two snapshots even comparable?"

Compares the current snapshot's receipt against a baseline's *before* you trust a diff between them. Returns a **comparability verdict** (same repo / enola version / extractor set / ignore globs?) and the **metric deltas** (files parsed, parse errors, coverage gaps, unresolved edges, fact/insight counts), flagging extraction-quality **regressions** — the signal that enola's own extraction got thinner. Use it as the gate before `diff_snapshot`, or poll it to drive improvements to enola's coverage.

| Parameter | Description |
|-----------|-------------|
| `baseline` | What to compare against: `pinned` (default), `previous`, or an explicit path to a directory holding `receipt.json` / `snapshot.meta.json`. |
| `output_mode` | `summary` (default — markdown) → `full` (complete JSON). |
| `max_tokens` | Optional hard cap. |

---

## Cross-repo: the graph of graphs

enola can analyze multiple repositories together. Use `append` mode to incrementally build a combined fact store, then query across all of them.

1. **Generate the first snapshot** as usual.
2. **Append additional repos** by calling `generate_snapshot` with `append=true`. Each appended repo's facts are tagged with a **repo label** (the directory basename, e.g. `/path/to/go-service` → `go-service`) and its file paths are prefixed with that label (e.g. `go-service/lib/foo.go`).
3. **Query across repos** with the `repo` filter on `query_facts` to scope to one repo, or omit it to query all at once.

### Linking, not just co-locating

Appending several repos does more than pool their facts — a linking pass connects the per-repo graphs using four signals the extractors already capture. Each is an independent plugin ([`internal/linkers/crossrepo/signals/`](internal/linkers/crossrepo/signals/)) that reports *evidence* rather than building facts; the linker materializes the accumulated evidence into service nodes and dependency facts. Adding a fifth is a new package and a registration line — see [docs/EXTENDING.md](docs/EXTENDING.md).

- **HTTP route role matching** — a route a repo *calls* (`role:"client"`, e.g. from a generated OpenAPI client) is matched to a route another repo *serves* (`role:"server"`, or a framework route) by normalized path + method. The caller is recorded as depending on the servee.
- **Kafka topic producer/consumer** — a repo that references a topic *owned by another loaded repo* consumes that repo's events, so it depends on it. The edge is drawn **consumer → producer**, mirroring HTTP client → server. Direction comes from the topic name rather than from call-site analysis: topics are conventionally named `<owning-service>.<event>` (`svc-orders.order_placed`, `core.items.item_uploaded`), so the leading segment identifies the producer, resolved to a repo by the same normalized leading-segment lookup the import signal uses. That makes the edge resolvable **from the consumer side alone** — it holds even when the producer's own publish code isn't parsed, which is the common case for a service whose events you consume but whose repo is loaded only shallowly. Two cases deliberately draw nothing: a repo referencing its *own* topic (it is the producer — intra-repo, not a dependency), and a topic owned by no loaded repo (an export sink, a third-party bus), which is left unlinked rather than guessed at. Edges are tagged `via:"kafka"` and carry `topic_count` / `topic_samples`.
- **Import / shared-lib references** — an import whose scope or leading segment names another loaded repo (e.g. `@app-web/lib-api`, `lib-core/money`) records a dependency on that repo.
- **Shared symbol surface** — when two repos declare enough of the same distinctive types (e.g. a vendored protocol header copied between them — the `onelab::*` / `GmshClient` / `GmshServer` classes shared by *gmsh* and *getdp*), they are coupled. The match is on each type's portable identity (the namespace-qualified name with the repo-specific directory prefix stripped), filtered to type-like symbols (class/struct/interface/enum) and to distinctive names — namespaced identities always count; bare names must be non-generic and reasonably long. A pair links only above a small shared-type threshold, so an incidental `Config`/`JsonParser` collision can't fabricate a dependency. This relationship is symmetric, which is why it never invents a direction: it annotates an edge one of the directional signals already drew, and when none exists it records a **coupling** (`type:"cross_repo_shared_code"`) that carries no relation and stays out of the traversable graph. That distinction is load-bearing — `depends_on` composes across hops and shared code does not, so a repo calling one side of a copy-paste pair does not thereby reach the other. Names are also verified against the files behind them where source is available, so the count of shared *code* is reported separately from the count that merely matched by *name*.

These become real, queryable facts:

- A `service` node per repo (`query_facts kind=service`), named by its repo label.
- A cross-repo dependency edge per `consumer → provider` pair, carrying the matched endpoints, import samples, shared topics, and shared-symbol samples.

Because they're ordinary graph nodes and edges, the traversal tools become cross-repo aware with no extra steps — `traverse`, `find_path`, and `impact_analysis` all reach across repo boundaries. The cross-repo dependencies also appear as a **Cross-Repo Dependencies** section in `llm_context.md`, so an agent reading the snapshot sees them without running a tool.

### Tuning what links

Every one of those signals rests on judgements about names — which single-segment paths are
too generic to match on, which type names are framework boilerplate rather than shared code,
which file paths hold generated stubs instead of contract surface. Those judgements are data,
not code: they live in [`internal/linkers/vocab`](internal/linkers/vocab/) and can be
overlaid from a `linking:` block in `enola.yaml`, so a false edge from a framework enola has
never seen is a config change rather than a patch and a release.

The lists are additive (`add:` / `remove:`), never replacing, so fixing one thing cannot
silently discard the rest. Thresholds are validated rather than clamped. And because the
vocabulary decides which edges get drawn, it is folded into the snapshot's config hash — two
snapshots taken under different vocabularies are not comparable, and the receipt says so.

What the config deliberately *cannot* express is a matching rule. A config language able to
describe how to match would let a user manufacture an edge, and every fact in the graph is
supposed to be derived rather than asserted. Widening what counts as "too generic to link on"
can only ever remove edges — the safe direction. See
[docs/EXTENDING.md](docs/EXTENDING.md#tuning-without-code).

### What is deliberately not linked

Cross-repo linking is the part of enola hardest to verify from the outside, so its limits belong beside the claim rather than being discovered later. In each case the choice is the same: **report the gap instead of inventing an edge.** A missing edge is visible — it appears in `enola coverage`'s unresolved count, and a reader can go look. A *wrong* edge is invisible and gets acted on: an `impact_analysis` that silently includes a dependency that does not exist is worse than one that admits it does not know.

- **Dynamic or interpolated paths.** A URL assembled at runtime (`base + "/" + tenant + "/orders"`) has no path to match, so the call site is detected, counted as unresolved, and never guessed at.
- **Cross-file mount resolution.** Prefix composition follows a subrouter across function and package boundaries within a module-wide fixpoint. A mount whose prefix cannot be traced back to a literal is left at its bare path rather than composed to a plausible one.
- **Bare catch-alls.** `app.get('*')` and equivalents are skipped: an SPA fallback is not an endpoint, and treating it as one would match any client path in any repository.
- **Absolute URLs to unloaded hosts.** Reported as *external* rather than unresolved — a third-party API is a known boundary, not a blind spot.

[`examples/cross-repo/`](examples/cross-repo/) is a runnable demonstration of both halves: two services whose link depends on a prefix composed across a function boundary, plus one deliberately dynamic call that stays unresolved. A test asserts it keeps demonstrating both, so the example cannot rot into describing an outcome that no longer happens.

### Finding unused endpoints

The same client/server route matching that draws cross-repo edges also answers its inverse: **which server routes does no loaded client call?** After linking, every server `route` a client matched is left untouched; every one that *no* client resolved to — by the identical normalized path + method join — is tagged `unmatched_by_clients: true` on the route fact.

The direct way to get the candidate list is the `unused-routes` finding:

```
query_insights(explainer="unused-routes")
```

which returns the per-service rollup with the out-of-snapshot caveat and suggested actions attached. To work with the raw facts instead — to filter, page, or post-process them — query the flag yourself:

```
query_facts(kind=route, prop=unmatched_by_clients, prop_value=true, repo="<service>")
```

(`query_facts(kind=route, output_mode=summary)` also reports the `unmatched_by_clients` count under a `## Flags` heading, so the signal is visible while sizing a route query.)

This is the candidate set for dead-endpoint cleanup, computed deterministically rather than grepped-and-guessed — the matching reuses the linker's exact path normalization (so a backend's `/api/settings/x` correctly counts as called by a client's base-relative `settings/x`), and it discriminates by method, so a read endpoint that clients hit stays clean while the `POST`/`PUT`/`DELETE` on the same path can still be flagged. Two guards keep it honest: only repos that actually serve a cross-repo client are considered (a frontend's own page routes are never flagged), and matching errs toward *use* — any path+method hit, at any confidence, counts — so the set is biased toward false negatives.

The flag means "unused by the clients in **this snapshot**" — not "dead." A consumer you didn't load (an admin script, a cron job, a webhook, a third-party caller, a mobile deep link) won't appear, so treat the list as candidates to verify, not delete on sight. The `unmatched_by_clients` flag is computed by the engine during linking and is **always** present on the facts; the `unused-routes` explainer (above) additionally rolls it up into one insight per service, with that caveat attached, and only that rolled-up insight depends on the explainer being enabled.

> **Config note:** the `crossrepo` explainer (which adds a cross-repo entry to `insights.json`) is enabled by default, and stays enabled as long as your config does not declare an `explainers:` list — the shipped configs deliberately declare none, since such a list replaces the defaults rather than extending them. If you do narrow it, `crossrepo` has to be in it. The `service` nodes, graph edges, traversal, and the `llm_context.md` section work regardless of explainer config; only the `insights.json` entry depends on it.

---

## Configuration

The config file is **optional**. Every field has a built-in default (see `config.Default()` in [`internal/config/config.go`](internal/config/config.go)), so with no `mcp-arch.yaml` enola runs entirely on those defaults — the file only *overrides* the keys you set. Create one (or pass a custom path as the first CLI argument) when you want to depart from the defaults below:

```yaml
repo: "."
# …or name a whole cluster instead, resolved relative to THIS file:
# repos:
#   - ../api
#   - ../web
#   - ../sdk
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
  - cpp
  - go
  - grpc
  - java
  - kotlin
  - openapi
  - php
  - python
  - typescript
  - swift
  - ruby
  - rust
explainers:
  - cycles
  - layers
  - crossrepo
  - coverage
  - unused-routes
  - god-class
  - hotspots
  - dependency-depth
  - exported-surface
  - complexity-outliers
renderers:
  - llm_context
output:
  dir: ".enola"
  max_context_tokens: 16000
```

The bundled [`mcp-arch.yaml`](mcp-arch.yaml) ships a much fuller `ignore` list (Android/Gradle, Xcode/SPM, Rails, CI, Docker, env files, …); see it and the per-language configs under [`examples/`](examples/) for ready-made starting points. It deliberately declares **no** `extractors:`, `explainers:` or `renderers:` — see the override rule below.

**Resolution, and why it is announced.** `bootstrap.ResolveConfig` looks for the named path (default `mcp-arch.yaml`) relative to the working directory, then — only when the running binary is *not* on `PATH`, i.e. an unpacked bundle rather than an installed one — beside the executable. Whatever it settles on is printed to stderr by every command (`enola: using config <path>`, or `no mcp-arch.yaml in <cwd>, using built-in defaults`), and the executable-adjacent case says so explicitly.

That line exists because the failure it prevents is silent. A config decides which extractors run and which paths are ignored, so loading the wrong one does not error — it analyses something other than what was asked for. Without the restriction and the announcement, a config sitting beside a `go build` output would govern every repository that binary is ever pointed at, from any directory without one of its own — and since an `extractors:` list replaces the default rather than merging with it, one written before a language's extractor existed silently yields `0 facts` for that language, with no error and no mention of it in the log.

A config that is **missing** falls back to the built-in defaults. A config that is **present and unusable** — unparseable, or naming an `output.dir` that cannot be honoured — is a fatal error instead, because the fallback's `repo: "."` is the working directory: a typo would otherwise make enola analyse whichever repository you were standing in and present it as an answer about the one the config named. Same rule the CLI already applies to an explicitly-named path that does not exist.

**The output directory ignores itself, wherever it is.** `config.Normalize` — run by both `config.Load` and `engine.New`, so a config assembled in code is treated identically — appends `<output.dir>/**` to the ignore list. `.enola/**` remains in the defaults as a literal as well, so a repository that used the default before changing it does not start indexing its own history.

That derivation is load-bearing rather than tidy. A literal `.enola/**` agrees with `Output.Dir` only by coincidence, so with `output.dir` set to anything else each snapshot would walk the previous one's artifacts — `facts.jsonl`, `insights.json`, `llm_context.md`, plus the `previous/` rotation — and an unchanged tree would produce a different snapshot every run. Reproducibility is the property the baseline diff rests on, and comparability checking cannot catch this: the config is identical on both sides.

**A list-valued key REPLACES its default; it does not merge.** `yaml.Unmarshal` overwrites the slice, so `extractors:` names the complete set — a config written before an extractor existed disables it permanently, and a disabled extractor is never tried and so never appears in the log. Two things make that visible: a bundled config that names no plugin lists at all, and a warning naming any *excluded* extractor that would have detected the repository (also recorded as `shadowed_extractors` in the snapshot receipt). The semantics are unchanged on purpose — an explicit list is the only way to disable an extractor.

| Field | Description | Default |
|-------|-------------|---------|
| `repo` | Repository root path, relative to the **working directory** | `"."` |
| `repos` | Ordered list of repository roots forming a multi-repo cluster; supersedes `repo`. One `--generate` run indexes them all (the first fresh, the rest appended), producing the service nodes and cross-repo edges a single-repo snapshot cannot have. Entries resolve relative to the **config file's own directory**, so a checked-in cluster config means the same thing wherever it is run from. Order is semantic; duplicates are dropped | *(unset)* |
| `ignore` | Glob patterns for files/dirs to skip | vendor, node_modules, .git, tests, build dirs, minified JS (`*.min.js`/`*.bundle.js`), docs, config data, … |
| `extractors` | Enabled extractors | `["cpp", "go", "grpc", "java", "kotlin", "openapi", "php", "python", "typescript", "swift", "ruby", "rust"]` |
| `explainers` | Enabled explainers | `["cycles", "layers", "crossrepo", "coverage", "unused-routes", "god-class", "hotspots", "dependency-depth", "exported-surface", "complexity-outliers"]` |
| `renderers` | Enabled renderers | `["llm_context"]` |
| `output.dir` | Output directory for artifacts. Must name a **subdirectory of the repository** — it is joined to the repository path, so an absolute value would nest that whole path inside the repo rather than write where it says. An ignore glob is derived from it automatically (see below) | `".enola"` |
| `output.max_context_tokens` | Token budget for `llm_context.md` | `16000` |
| `dashboard.port` | The fixed **shared URL** port every server competes for, in addition to its own ephemeral one. A negative value serves only the ephemeral port. `ENOLA_DASHBOARD_PORT` overrides it (`off` disables). | `7171` |
| `incremental` | Reuse each extractor's cached facts across snapshots when its files are unchanged; set `false` to force full re-extraction every run | `true` |

---

## Supported languages

Each extractor is detected by characteristic project files and then parses what it finds. Detection walks into subdirectories for the monorepo cases noted below.

| Language   | Parser           | Detected by |
|------------|------------------|-------------|
| Go         | `go/ast`         | `go.mod` present |
| Java       | tree-sitter      | `pom.xml` (Maven) present, or any `.java` source file (a Gradle build file alone does **not** trigger it — Kotlin/Android use Gradle too) |
| Kotlin     | tree-sitter      | `build.gradle.kts` / `build.gradle` with Kotlin/Android |
| JavaScript | tree-sitter      | `.js`/`.jsx` files are parsed by the TypeScript extractor (tree-sitter's TypeScript parser handles JavaScript natively) |
| Python     | tree-sitter      | `pyproject.toml`, `setup.py`, `requirements.txt`, `Pipfile`, `pytest.ini`, `mypy.ini`, `tox.ini`, or `setup.cfg` (root or up to 3 levels deep) |
| TypeScript | tree-sitter      | `tsconfig.json`, `tsconfig.base.json`, or `package.json` with TypeScript (root or one level deep) |
| Vue        | tree-sitter      | `package.json` with `vue` dependency, or `nuxt.config.js/ts/mjs` for Nuxt (root or one level deep) |
| Svelte     | tree-sitter      | `package.json` with `svelte` dependency, or `svelte.config.js/ts/mjs` / `@sveltejs/kit` for SvelteKit |
| Swift      | tree-sitter      | `Package.swift`, `.xcodeproj`, or `.xcworkspace` present |
| Ruby       | tree-sitter      | `Gemfile` present |
| Rust       | tree-sitter      | `Cargo.toml` present (root or up to 3 levels deep) |
| C/C++      | tree-sitter      | a C source (`.c`) or C++ source (`.cpp`/`.cc`/`.cxx`/`.hpp`/...) present, or a build file (`CMakeLists.txt`/`Makefile`/`meson.build`/`*.vcxproj`) plus any header |
| PHP        | tree-sitter      | `composer.json`, a WordPress bootstrap file (`wp-load.php`/`wp-settings.php`/`wp-config.php`), or any `.php` source within 3 directory levels |
| OpenAPI    | YAML/JSON scanner| any file containing `openapi:` or `swagger:` |
| gRPC       | proto3 scanner   | any `.proto` file present |

**Go** uses the standard-library parser (`go/ast`) directly rather than tree-sitter, so symbols, methods, interfaces, imports, and call edges are exact rather than best-effort. Extraction runs in **three global passes** ([`go.go`](internal/extractors/goextractor/go.go)): every package is parsed first, then module-wide indexes are built (struct-field types, package names, gRPC stub paths, route mount prefixes), then facts are emitted per file. The middle pass is what makes cross-package struct field types visible, so a multi-hop chain like `h.authLib.Service.Register` resolves to the declaring symbol instead of dangling. Functions, methods, types, and constants become symbol facts named `<pkgDir>.<Name>` (`<pkgDir>.<Receiver>.<Method>` for methods) carrying `exported`; embedded and implemented interfaces become `implements` edges, composite literals and struct field types become `instantiates` (so a struct that is only ever constructed, never called, isn't a dead-code false positive), and imports become `dependency` facts classified `stdlib` / `internal` / `external`. Go's predeclared identifiers (`len`, `make`, `string(b)`, …) are filtered out of call resolution, since resolving them would produce phantom edges to symbols that don't exist. Like the other AST extractors it walks each body once for the complexity metrics `cyclomatic`, `loop_depth`, `loop_count`, `calls_in_loop`, and `recursive_self`, plus `scaling_loop_depth` / `calls_in_scaling_loop` with two discounts: a constant-trip loop (a fixed-size composite literal, a `for { }` driven by break/return) adds no factor of *n*, and a **hierarchical** nested loop — a range over a collection reached *through* an enclosing loop's variable, i.e. a tree or AST walk — is linear rather than quadratic. *Unlike* Swift/TypeScript/Python/Rust/PHP/C++, the Go extractor emits no `io_direct`/`performs_io` flags — the loop metrics are there, but the I/O signal that turns an in-loop wrapper call into a detectable N+1 is not.

**Go route and client extraction** covers the server side, the client side, and the async side. **Routes** are recognized for `net/http`, `gorilla/mux`, and `go-chi/chi` (picked per file from the imports, specific routers winning over `net/http`), including chi's `r.Group(func(r chi.Router){ … })` nesting, and each emits a `route` fact with `method`, `framework`, and the registration-site `handler` expression. Mount prefixes are composed both *within* a function (`apiRouter := router.PathPrefix("/api").Subrouter()`) and **interprocedurally** ([`routeprefix.go`](internal/extractors/goextractor/routeprefix.go)): real backends split registration across files and packages, so a module-wide fixpoint propagates the prefix a router argument carries at each call site into the callee's router parameter, and a route registered on a subrouter passed into a per-package registration function is stored at its true runtime path (`/api/settings/courses`, not the bare `/courses`). A function mounted at several points emits its routes once per prefix; an unresolved mount keeps the bare path, so composition never *drops* a route. The analysis is name-based like the rest of the extractor, which means it can miss a prefix but cannot fabricate one. **Handler binding** connects a route back to the code that serves it: a function whose signature is `func(http.ResponseWriter, *http.Request)` is tagged `http_handler`, and a post-extraction pass binds each route to its declaring symbol via a `handled_by` edge. Binding is discriminated by *signature*, not by name — a route's `handler` prop names the receiver **variable** while the symbol is named by its receiver **type**, so the two key spaces barely intersect and a name-only rule would happily bind a route to a same-named stub in a wiring package; ambiguous method names are left unbound, because a wrong `handled_by` edge feeds `impact_analysis` and `find_path` and is worse than no edge. **Outbound HTTP calls** (`http.NewRequest`, `http.Get`, or a wrapper client's verb method) emit client-role `route` facts so the cross-repo linker can match them to the service that serves them; the target service is hinted from base-URL env-var naming (`CORE_HTTP_CLIENT_BASE_URL` → `core`), and a call whose base URL resolves — through file-local constants or struct fields — to an absolute `http(s)://` literal is tagged `external` with its `host`, so a third-party call is not mistaken for an unresolved internal edge. A config-injected or relative base stays internal and linkable. **Storage** facts come from SQL string literals (`CREATE TABLE` / `ALTER TABLE` → `storage_kind: table`; `INSERT INTO` / `UPDATE … SET` / `DELETE FROM` / `FROM` → `table_reference`, each with an `operation` prop) and from structs holding an `s3.Client`/`s3.S3` field when the AWS S3 SDK is imported. **gRPC clients** are detected too — see the gRPC entry below, which covers both the generated-stub path index and the call-site route facts. Finally, `*_test.go` files are parsed for the sole purpose of capturing their outbound references ([`testrefs.go`](internal/extractors/goextractor/testrefs.go)), emitting a reference-only `test_ref` fact and no symbols, so a production function exercised only by its test is not mis-reported as dead while the test functions themselves never become dead-code candidates.

**Go Kafka topics** make asynchronous coupling visible, which is the one dependency a call graph structurally cannot see: a service consuming another's events shares no import, no call, and no HTTP route with it. The extractor ([`kafka.go`](internal/extractors/goextractor/kafka.go)) emits a `storage` fact per topic name (`storage_kind: topic`, `messaging: kafka`, `source`), which the cross-repo linker binds into a consumer → producer edge by topic-name ownership (see [Linking, not just co-locating](#linking-not-just-co-locating)). It resolves the two ways a topic string reaches Go code *without* a literal at the call site: a config struct field whose **name ends in `Topic`** carrying an envconfig `default:"…"` tag, and an `env.Get("<KEY>_TOPIC", "<default>")` lookup — in both cases the default is the topic the service uses unless the environment overrides it. Both forms anchor on an explicit topic marker (the field-name suffix, the `TOPIC` substring in the env key), which is deliberate: an **in-process event bus**, whose `Subscribe` takes a Go event *symbol* rather than a topic string, therefore produces no fact **by construction** rather than by a filter that could drift. The extractor does not attempt to classify producer vs consumer — that direction is derived by the linker from the topic name, which is more reliable than a call-site guess. *Scope:* gin, echo, and fiber routers are not recognized; interface-dispatch call targets are left unresolved rather than guessed; and the Go extractor deliberately does not participate in the incremental extractor cache (it is not a `plugin.FileOwner`), so Go re-extracts on every snapshot.

**TypeScript** (tree-sitter) includes Next.js route detection (App Router and Pages Router), monorepo detection one level deep, and parsing of `openapi-typescript`-generated client files — each operation is emitted as a `route` fact with `role:"client"`. App Router route groups like `(standard)` are stripped from URLs. Like the other AST extractors it walks function/method bodies for the complexity metrics (`cyclomatic`, `loop_depth`, `loop_count`, `calls_in_loop`, `recursive_self`) that the built-in `complexity-outliers` explainer reads, and that the enterprise `analyze_performance` tool consumes in more depth, and — mirroring Swift — it tags a body `io_direct` when it directly invokes a network/file primitive (`fetch`, `axios`, `fs.readFile`, `navigator.sendBeacon`, `new WebSocket`/`XMLHttpRequest`/`EventSource`) **or** calls a binding imported from a network module (a known HTTP-client package, or any path with a `network` segment — e.g. a `request` helper from a `.../lib/network/request` module). A serial post-pass (`computeTSPerformsIO`) then propagates that flag transitively over the `calls` graph into a `performs_io` prop via a cycle-safe monotone fixpoint, so a function reaching the network only through wrapper helpers is still flagged — letting the analyzer catch a per-iteration network call (an N+1) hidden behind a wrapper. *Limitation:* default-imported internal wrappers aren't resolved by the call-edge pass, so the fixpoint does not cross that hop; in practice most wrappers call their I/O sink directly (so they are seeded `io_direct` without needing the edge), and the analyzer's short-name I/O index still matches the in-loop call.

**Vue** support is integrated within the TypeScript extractor. `.vue` Single File Components are handled natively — the extractor parses each SFC's `<script>` and `<script setup>` blocks (case-insensitive, with `lang` attribute detection for TypeScript vs JavaScript) and feeds them through the existing tree-sitter TypeScript pipeline. Detection checks for a `"vue"` dependency in `package.json` (TypeScript root first, then repo root fallback); **Nuxt** is additionally detected by `nuxt.config.js/ts/mjs` or a `"nuxt"` package dependency. Each `.vue` file emits a `symbol` fact for the component, named `<dir>.<ComponentName>` (kebab-case converted to PascalCase), carrying `web_component: "component"`, `framework: "vue"` or `"nuxt"`, and `vue_setup: true` when using `<script setup>`. Functions named `use*` anywhere in the project are automatically classified as composables (`web_component: "composable"`). **Nuxt file-based routing** emits one `route` fact per file under `pages/`, with the URL derived from the file path — index files resolve to `/`, dynamic segments like `[id].vue` are preserved — each with `method: "GET"` and `router: "pages"`. Files containing a `createRouter()` call are emitted as a route fact with `type: "router_config"`. Import statements in all script blocks become `dependency` facts, and call edges from method bodies participate in `traverse`, `find_path`, and `impact_analysis`. Vue detection runs automatically inside the `typescript` extractor — no separate entry is needed under `extractors:` in config.

**JavaScript** (`.js`/`.jsx`) is handled by the TypeScript extractor. Tree-sitter's TypeScript parser natively parses JavaScript (JS is a subset of TS), so all extraction features — imports, declarations, call graphs, JSX component detection — work identically for `.js` and `.jsx` files. No separate configuration is needed; any project detected by the TypeScript extractor will have its JavaScript files processed automatically alongside TypeScript files. **Minified/bundled JS is skipped**: before parsing, any file with a line longer than ~2000 characters is treated as a generated artifact and produces no facts (a directory containing only such files emits no module either), so checked-in vendor bundles do not distort the graph or the enterprise complexity/performance analyses.

**Svelte** support is integrated within the TypeScript extractor, following the same pattern as Vue. `.svelte` Single File Components are parsed by extracting `<script>` and `<script module>` blocks (Svelte 5 syntax; the older `<script context="module">` form from Svelte 4 is also supported) and feeding them through tree-sitter. Detection checks for a `"svelte"` dependency in `package.json`; **SvelteKit** is additionally detected by `svelte.config.js/ts/mjs` or a `"@sveltejs/kit"` package dependency. Each `.svelte` file emits a component fact with `web_component: "component"`, `framework: "svelte"` or `"sveltekit"`. **SvelteKit file-based routing** emits route facts for `+page.svelte`, `+layout.svelte`, `+error.svelte`, and `+server.ts` files under `src/routes/` — route groups in parentheses like `(groupName)` are stripped from URLs, dynamic segments like `[slug]` and catch-all `[...rest]` are preserved. Server-side load files (`+page.server.ts`, `+layout.server.ts`) are not emitted as routes. The SvelteKit `$lib` path alias is automatically resolved to `src/lib/`, ensuring imports like `$lib/utils` appear as internal dependency edges rather than unresolved externals.

**Python** is parsed with tree-sitter (the concrete syntax tree handles nested classes/methods and docstrings natively, replacing the older indentation scanner). It understands **FastAPI/Starlette** route decorators and **Django** routes — `@api_view([...])` and `urls.py` `path()`/`re_path()` — emitting a `route` fact per endpoint. It emits `storage` facts for **SQLAlchemy** `__tablename__` and **Django models** (table name inferred from the class name), and classifies Django views and serializers via a `django_component` prop. It captures `async def` (`async: true`), decorator props (`@property`, `@staticmethod`, `@classmethod`, `@abstractmethod`, and Celery `@task`/`@shared_task`), and return-type hints. Each class emits an `implements` edge per base class, with generic type parameters stripped (`CRUDBase[Model, Id]` → `CRUDBase`), and both `import` forms become `dependency` facts. Crucially, the Python extractor now walks function and method bodies for call sites, emitting `calls` and `instantiates` edges (filtering out builtins) — so Python code participates in the dependency/call graph and is reachable by `traverse`, `find_path`, and `impact_analysis`. Monorepo detection walks up to 3 levels.

**Java** (tree-sitter) is framework-aware for the JVM server ecosystem. It emits symbol facts for classes, interfaces, enums, records, and annotation types, plus their methods, constructors, and fields, named with enola's `<dir>.<Type>` / `<dir>.<Type>.<method>` convention (nested types are qualified through the enclosing type). `extends`/`implements` become `implements` edges, `new X()` becomes `instantiates`, same-class method calls become `calls`, and both import forms become `dependency` facts split into internal vs. external. Because Java imports are explicit, type-reference edges are resolved through a project-wide fully-qualified-name index built in a second pass — so `implements`/`instantiates`/`injects` targets point at the canonical declaring symbol in another file or module rather than a bare name. Framework specialization covers **Spring MVC** (a `@RestController`/`@Controller` class's `@RequestMapping` base path is combined with method-level `@GetMapping`/`@PostMapping`/`@PutMapping`/`@DeleteMapping`/`@PatchMapping`/`@RequestMapping(method=…)` into one `route` per endpoint, carrying the HTTP method and the handler symbol), **Spring stereotypes** (`@Service`/`@Component`/`@Repository`/`@Controller`/`@Configuration` classified via a `component` prop), **dependency injection** (`@Autowired` fields, constructor injection, and Lombok `@RequiredArgsConstructor` over `final` fields → `injects` edges), and **JPA / Spring Data storage** (`@Entity` → a `storage` fact with `storage_kind: entity`; `@Repository` and `JpaRepository`/`CrudRepository`-style interfaces → `storage_kind: repository`). A `@Table(name = …)` is captured, and when the name is given as a `static final String` constant it is resolved to its literal value — the original identifier is preserved in a `table_constant` prop. **Apache Dubbo** is recognized too: `@SPI`/`@Activate`/`@DubboService` tag the type with `framework: "dubbo"` (`dubbo_spi`, `dubbo_activate`). Detection requires Maven (`pom.xml`) or real `.java` sources, so a pure-Kotlin Gradle project is left to the Kotlin extractor.

**Kotlin** is Android-aware: it detects Jetpack Compose (`@Composable`), Hilt DI (`@HiltViewModel`, `@Module`, `@AndroidEntryPoint`), Room (`@Entity`, `@Dao`, `@Database`), ViewModels, Repositories, Use Cases, and Workers.

**Swift** (tree-sitter) emits symbol facts for classes, structs, enums, protocols, and extensions plus their methods, initializers, and properties, named `<targetDir>.<Type>.<member>` — where `<targetDir>` is the file's resolved SPM/XcodeGen *target* module (parsed from `Package.swift` and `project.yml`), not its leaf directory. Members declared inside a type are classified `symbol_kind: method`; free functions stay `function`. It walks bodies for the call graph: same-type `self.`/`self?.` dispatch, member calls on any receiver (`coordinator?.show()`, `delegate?.tap()`), and cross-`extension` calls all become `calls` edges — emitted as bare short names at walk time (extraction is parallel-per-file) and bound in a serial post-pass against a project-wide method index (unique name → the qualified `dir.Type.method`, ambiguous → the bare name still matched by short name, unmatched → dropped so stdlib/framework calls don't create phantom edges). A further post-pass resolves **inherited-method calls** — a subclass or protocol conformer calling a base-class / protocol-extension method — by walking the caller type's supertype chain (from the `implements` edges) nearest-first and rewriting the otherwise-dangling call target to the declaring ancestor's method fact (`dir.DataModel.runRequest`), so class/protocol hierarchies are traversable for impact analysis, dead-code, and the performs_io closure. `Foo()` → `instantiates`, constructor/property DI → `injects`, SwiftUI `View`→`ViewModel` → `depends_on`, and custom-operator usage (`a <- b`, but not stdlib operators like `+`/`<=`) → a `calls` edge to the operator. Top-level calls in `#!/usr/bin/swift` scripts are captured via a file-scope reference fact. Like the other AST extractors, it walks function/method bodies — and also computed-property getters and `willSet`/`didSet` observers — for the standard complexity metrics `cyclomatic`, `loop_depth`, `loop_count`, `calls_in_loop`, and `recursive_self`, which the built-in `complexity-outliers` explainer reads and the enterprise `analyze_performance` tool consumes in more depth; syntactic `for`/`while`/`repeat-while` and iterator closures (`map`/`forEach`/`filter`/…) count as loops, but **constant-bounded loops do not add scaling depth** — a literal integer range (`for i in 0..<10`), a literal-bound `stride(...)`, or an iterator over an array/dictionary literal or ALL-CAPS constant (`STOP_CHARS.forEach`) runs a fixed number of times, so it never inflates a genuine O(n) into a false O(n²)/O(n³). A method whose body invokes a network/file I/O primitive (`URLSession`/`dataTask`/`.data(for:)`, Alamofire `request`/`download`/`upload`, `Data(contentsOf:)`) is tagged `io_direct`; a serial post-pass then propagates that up the call graph into a transitive `performs_io` prop — crossing ambiguous kept-bare member-call edges by expanding them through the method-name index (bounded) rather than mutating the graph — so the enterprise `analyze_performance` tool can flag a per-iteration network N+1 (a loop calling a method that transitively hits the network) even when the I/O sits behind wrapper layers. It is **iOS-aware**: SwiftUI views (`View`/`App`/`Scene`), UIKit (`UIViewController`/`UIView` subclasses), Combine view models (`ObservableObject`, `@Observable`), architectural roles (Repositories, Use Cases, Coordinators, Services, DI containers), and `@MainActor`. *Limitation:* the vendored tree-sitter-swift grammar cannot parse a few advanced constructs — notably a tuple-type metatype `(A, B).self` (e.g. `withTaskGroup(of: (UUID, Result<T, Error>).self)`) — and its error recovery then flattens the whole enclosing type to file scope, so that file's type node is lost and its methods surface as top-level `function` symbols (~3% of files in a large iOS codebase). Dead-code detection stays accurate on these — a member call whose method was flattened falls back to resolving against the top-level function of that name (a rare same-name collision biases toward a missed lead, never a false accusation) — but the type's coupling/impact edges are degraded for the affected file until the construct is removed or the grammar gains support.

**Rust** (tree-sitter) targets a Cargo workspace or single crate. Every `Cargo.toml` in the repo is scanned up front (a minimal line-based `[package]`/`name` scan, not a full TOML parser) into a crate-name → crate-directory index, so cross-crate `use` paths resolve without a second parsing pass. It emits symbol facts for `fn`/`struct`/`enum`/`trait`/`type`/`const`/`static` items, qualifying nested `mod { }` blocks and `impl`/`trait` bodies into the name (`<dir>.<mod>.<Type>.<method>`); a function/method inside an `impl` or `trait` block is a `method`, everything else a `function`, and an impl-block method with no `self` parameter is tagged `static`. Because `impl Trait for Type` is a separate top-level item from `Type`'s own declaration — frequently in a different file — the resulting `implements` edge is attached to `Type`'s existing symbol fact by a small post-pass over the merged fact set (`applyImplements`) rather than emitted as a second, otherwise-empty fact that would double-count `Type` in symbol-kind stats. `use` declarations (including brace-expanded lists like `use std::{fmt, collections::HashMap}`) become `dependency` facts classified `internal`/`external`/`stdlib`: `self::`/`super::`/`crate::` paths and other in-workspace crate names resolve to the real submodule directory when one is known (trying progressively shorter suffixes against the known module-directory set, mirroring Python's import resolution) and fall back to the crate/module root otherwise; `std`/`core`/`alloc` are `stdlib`. Call resolution is deliberately conservative: a bare call resolves against a sibling method in the enclosing `impl`/`trait` block or a same-directory top-level function; `self.method()`/`Self::method()` resolve to the enclosing type's sibling method; any other receiver or path form (`recv.method()`, `Type::method()` for a different type) falls back to a bare short-name `calls` edge so dead-code matching still sees it used, without guessing a canonical target it can't verify. It also computes `cyclomatic` per function/method body (`if`, `match` arm, `while`/`for`/`loop`, the `?` operator, and `&&`/`||` each add one). Both construction forms emit `instantiates`: a tuple-call (`Foo()`) as part of ordinary call handling, and a named struct literal (`Foo { field: value }`, including functional-update syntax `Foo { field: value, ..base }`) via its own `struct_expression` handler, since it isn't a `call_expression` in the grammar. It also detects Axum routes: a `.route("/path", get(handler))` builder call (including chained verbs like `get(a).post(b)`) emits one `route` fact per (path, method) pair, matched structurally rather than gated on an `axum` dependency check. *Scope:* `.route_service(...)` and `.nest(...)` sub-router prefixes aren't handled (see [`axum.go`](internal/extractors/rustextractor/axum.go)), Actix/Rocket have no route awareness yet, and macros are treated as opaque (no expansion, no edges through them).

**OpenAPI** scans for spec files independently of the main walker (so it finds them even when `*.yaml`/`*.json` are globally ignored), confirming candidates by an `openapi:`/`swagger:` key. It emits one `route` per operation enriched with method, `operationId`, summary, tags, and a spec back-reference; specs under an `openapi/client/` directory are marked `role:"client"`. Gateway extensions (`x-gateway-config`, `x-gateway-capabilities`) are parsed into props.

**gRPC** models Protocol Buffers services the same way HTTP endpoints are modeled, so a gRPC surface answers the same cross-repo and unused-endpoint questions as a REST one. A small dependency-free proto3 scanner (comment-stripped, brace-depth aware — the same class of parser as OpenAPI's) reads each `.proto` and emits, for every `rpc`, a **server-role `route`** whose `Name` is the gRPC wire path `/pkg.Service/Method` (e.g. `/users.v1.UserService/GetUser`) with `method:"POST"`, `framework:"grpc"`, `source:"grpc-proto"`, `type:"grpc"`, and `rpc_service`/`rpc_method`/`streaming` props — the exact path+method a gRPC-web client hits over HTTP, so these flow through the cross-repo linker's normalized path+method matching and the `unused-routes` explainer with no linker special-casing. Each service also emits an `interface` symbol, each RPC a `method` symbol (`has_method`-linked to its service), and each message a `struct`/`enum` symbol, and proto `import`s become `dependency` facts — so proto participates in `traverse`, `find_path`, and `impact_analysis`. **Client-side detection** covers both TypeScript and Go. In the **TypeScript** extractor a repo-wide pre-pass resolves generated stubs — `@protobuf-ts` (`new ServiceType(...)`), connect-es (`typeName`), and **classic grpc-web** (where it derives the service and methods from the `MethodDescriptor`/`rpcCall` `/pkg.Service/Method` path literals) — into a service→method map, then per-file it binds `new XxxServiceClient(...)` variables (including typed constructor-injected fields) and emits a **client-role `route`** (`source:"ts-grpc-client"`) for each `client.method(...)` **call site** — only for methods actually called, so an RPC the frontend never invokes correctly surfaces as unmatched by clients. The **Go** extractor does the same for grpc-go consumers: because a Go call site (`client.GetUser(ctx, req)`) carries no wire path, a repo-wide pre-pass reads the authoritative `/pkg.Service/Method` from the *generated* code — the concrete client's `Invoke`/`NewStream` string literal (grpc-go, unary + streaming) or the `…Procedure` const (connect-go) — and builds a client-interface→method→path index. Per-file, it reuses the Go extractor's own receiver/field/local-variable type resolution (`resolveChain`) so a client is recognized whether it's a **local variable**, an **inline construction**, a **struct field** (`s.users.GetUser(...)` — dependency injection), or a **package-level var**, emitting a **client-role `route`** (`source:"go-grpc-client"`) per call site. Both **grpc-go** and **connect-go** consumers are covered. On the TypeScript side, **connect-es** consumers using `createClient(Service, transport)` / `createPromiseClient(...)` are detected alongside the `new XxxClient(...)` form. Cross-repo gRPC edges are tagged `via:"grpc"`. **Go handler binding:** a post-extraction pass connects each gRPC server route to the Go method that serves it via a `handled_by` edge (route → `pkg.Type.Method`) and a `handler` prop, so `impact_analysis`/`find_path` traverse from the RPC to its implementation (and, through the cross-repo edges, on to its clients). The bridge is the `protoc-gen-go-grpc` forward-compatibility convention — a server impl embeds `Unimplemented<Service>Server`, which the Go extractor already records as an `implements` edge, so the service short name matches the route's `rpc_service` with no new Go parsing; ambiguous or non-embedding impls are left unbound. *Scope:* client detection targets protoc-gen-go-grpc, connect-go, `@protobuf-ts`, connect-es, and classic grpc-web generated stubs; hand-rolled clients that bypass the generated stubs are not recognized (a namespaced commonjs grpc-web constructor, `new proto.pkg.XxxClient(...)`, binds only best-effort).

**Ruby** is parsed with tree-sitter, replacing the former line-based regex scanner — the grammar handles heredocs, endless methods (`def x = expr`), multi-line expressions, and the nested scopes that tripped up the line scanner. It is Rails-aware: ActiveRecord models (`has_many`/`has_one`/`belongs_to`/`has_and_belongs_to_many`, scopes, table inference, explicit `self.table_name`) emit `storage` facts; the route DSL in `config/routes.rb` (plus `config/routes/*.rb` and packwerk `draw`) is walked from the real block structure, so nested `namespace`/`scope`/`resources`/`member`/`collection` blocks produce one `route` per RESTful action (honoring `only:`/`except:`); and Packwerk package boundaries (`package.yml` dependency enforcement, `app/public/` privacy) are parsed. It tracks modules, classes, methods with `public`/`private`/`protected` visibility, `class << self` eigenclass and `module_function` methods — now correctly typed as class methods rather than instance methods — mixins (`include`/`extend`/`prepend` → `implements` edges), `ActiveSupport::Concern` (flagged `concern: true`), constants, and `attr_*` accessors. Like the other AST extractors, it walks method bodies for call sites, emitting `calls` edges (qualified `Const.method`/`Ns::Class.method` and receiver `var.method`, deduplicated) and `implements` edges for superclasses — so Ruby participates in `traverse`, `find_path`, and `impact_analysis`.

**C/C++** is parsed with tree-sitter and handles the header/source split that defines the language. The extractor owns both languages: `.c` files (and bare `.h` headers in a C context) are parsed with the **tree-sitter-c** grammar, while `.cpp`/`.cc`/`.cxx`/`.hpp`/... (and `.h` headers in a C++ context) use **tree-sitter-cpp**; every fact carries a `language` prop (`"c"` or `"cpp"`). A real C grammar is required rather than reusing the C++ one, because C code routinely uses C++ keywords as ordinary identifiers (`new`, `try`, `class`, `delete`, `private`, ...) plus C-only constructs (`_Generic`, `restrict`, GCC range designators) that the C++ grammar rejects. Bare `.h` headers are attributed **per directory subtree**: a header is treated as C++ only when its own subtree contains unambiguous C++ sources — so a handful of stray `.cpp` files (e.g. `tools/` in an otherwise pure-C kernel tree) do not flip every `.h` in the repo to C++; absent that signal, `.h` defaults to C. In C, a `static` function has internal linkage and is emitted with `exported=false` (file-private); C++ keeps `exported=true`. Classes, structs, unions, enums (incl. `enum class`), namespaces, free functions and methods, data members, and `typedef`/`using` aliases become symbol facts named `<dir>.<ns1::ns2::Class::member>` — enola's `<dir>.` module convention on the outside, native C++ `::` scope inside. Because an out-of-line definition `Class::method` (parsed from a `qualified_identifier`) yields the same canonical name as its in-class declaration, a dedup pass **merges a header's method prototype with its `.cpp` definition** into a single symbol (the definition wins for file/line and carries the call-graph edges). Base classes become `implements` edges; method bodies are walked for `calls`/`instantiates` edges; quoted `#include "x.h"` becomes a `dependency` resolved to the declaring module, while system `<...>` includes are skipped. Templates are unwrapped to their inner declaration and flagged `templated`, and the walker descends through `#if`/`#ifdef` preprocessor guards (so code wrapped in `#if defined(HAVE_*)` and headers behind include guards are still extracted). Like the other AST extractors, it walks each function/method body for complexity metrics — `cyclomatic`, `loop_depth`, `loop_count`, `calls_in_loop`, and `recursive_self` (counting `for`/`while`/`do-while`/range-`for` and STL-algorithm lambdas like `for_each`/`transform` as loops) — which the built-in `complexity-outliers` explainer reads and the enterprise `analyze_performance` tool consumes in more depth. *Limitation:* header/source merging relies on the `.h` and `.cpp` living in the same directory (the common layout); split `include/` + `src/` trees are not merged.

**PHP** is parsed with tree-sitter (the `LanguagePHP` grammar, which tolerates the HTML/`<?php` interleaving of WordPress templates). Classes, interfaces, traits, enums (and their cases), top-level functions, methods, class constants, and properties become symbol facts. Type names are fully qualified with the file's `namespace` (`App\Models\Order`), members use PHP's native `Class::method` / `Class::$prop` notation, and global symbols keep their bare name (the common WordPress case). `extends`/`implements` and in-class `use SomeTrait;` become `implements` edges; method/function bodies are walked for `calls` (global-function, static `Class::method`, and bare instance-method targets), `instantiates` (`new X`), and the standard complexity metrics (`cyclomatic`, `loop_depth`, `loop_count`, `calls_in_loop`, `recursive_self`). `use Foo\Bar;` imports become `dependency` facts; a resolve pass builds a project-wide FQN→module index and turns namespaced references (inheritance, trait use, static calls, instantiations) and resolvable imports into internal module-coupling edges, classifying the rest as external — so PHP participates in `traverse`, `find_path`, and `impact_analysis`. **Outbound HTTP-client detection** emits a client-role `route` fact for each call made through Guzzle (`$client->get('/x')`, `$client->request('POST', '/x')`), the Laravel `Http` facade (`Http::get(...)`, including `Http::withToken(...)->get(...)` chains), Symfony's `HttpClient` (`$httpClient->request(...)`), raw cURL (`curl_setopt($ch, CURLOPT_URL, …)`, `curl_init`), and `file_get_contents` — relative paths are kept (with a `target_hint` inferred from any nearby base-URL env var), while absolute `http(s)://` URLs and interpolated/concatenated paths are skipped. **Framework route DSLs** add server-role `route` facts. **WordPress awareness** (enabled when a `wp-load.php`/`wp-settings.php`/`wp-config.php` marker is present) emits a `route` fact per hook call: `add_action`/`add_filter` registrations (carrying the `callback` target), the `do_action`/`apply_filters` hook points, and `register_rest_route` endpoints — dynamic (interpolated/variable) hook names are skipped. **Laravel** (detected via `laravel/framework` in `composer.json`, an `artisan` file, or a `routes/web.php`|`api.php`) parses the `Route::` DSL in `routes/*.php`: verb registrations (`Route::get/post/…`), `Route::match`/`any`, `Route::resource`/`apiResource` (expanded into their REST actions), nested group prefixes (both `Route::group(['prefix' => …], …)` and the fluent `Route::prefix('x')->…->group(…)`), and the `->name(…)` modifier — handlers are normalized to `Controller::method`. **Symfony** (detected via `symfony/framework-bundle`, or `bin/console` + `config/`) reads routes from PHP 8 `#[Route('/path', methods: ['GET'], name: '…')]` attributes (a class-level attribute prefixes its methods; `methods` accepts string literals or `Request::METHOD_*` constants) and legacy `@Route(…)` docblock annotations, plus YAML (`config/routes.yaml`, `config/routes/*.yaml`) and XML route configuration — these config files are scanned directly from disk, independently of the main walker (the same approach the OpenAPI extractor uses), so they are found even when `*.yaml` is globally ignored, as the bundled configs do. Once emitted, these client/server routes flow through the cross-repo linker and the `unused-routes` explainer like every other language. *Scope:* Symfony route imports/`resource` includes and bundle-local route configs (`**/Resources/config/routes.*`) are not expanded/discovered.

---

## Output artifacts

After `generate_snapshot`, these are written to the output directory (default `.enola/`):

| File | Description |
|------|-------------|
| `llm_context.md` | Compact, token-budgeted architecture summary for an agent to read directly |
| `facts.jsonl` | Every extracted fact, one JSON object per line |
| `insights.json` | Architectural insights with confidence scores |
| `snapshot.meta.json` | Metadata including per-file content hashes for incremental updates, plus the full receipt fields |
| `receipt.json` | The **snapshot receipt** — a compact manifest of what the graph was generated over (enola version, git ref + dirty status, a content-fingerprint snapshot ID, the extractor/explainer sets, ignore-glob hash, output-artifact hashes) and extraction-quality metrics (files seen/parsed/skipped, directory trees pruned, parse errors, coverage gaps). Read it via the `snapshot_receipt` tool. |
| `previous/` | The immediately-preceding snapshot, auto-rotated on each write — the `baseline='previous'` source for `diff_snapshot` |
| `baseline/` | A snapshot pinned by `set_baseline`, preserved across re-snapshots — the default `diff_snapshot` baseline |

Outside the repository, `~/.enola/graphs/<workspace>/history/` holds the **architecture history**: one append-only line per snapshot, plus each revision's graph stored as a patch against the previous one. See [docs/HISTORY.md](docs/HISTORY.md).

It sits outside `.enola/` for a reason that is not convenience. Every artifact in the table above is derivable from the tree — delete them all and the next run reproduces the same `snapshot_id` — and a history is the first thing enola keeps that the working tree has forgotten. So it is bounded: every revision is replayable (`enola log --backfill` reproduces it from the commit), **nothing that judges the present reads it** (`check`, `diff_snapshot`, freshness and drift consult only the current snapshot and the pinned baseline), and deleting it changes no verdict and no `snapshot_id`. Both halves are regression tests, not intentions.

That rule is also why `previous/` and `baseline/` above are still full copies rather than references into the history. Making them references would save the duplication and would make removing the history able to change what the gate says — precisely the dependency the rule forbids.

### The snapshot receipt

`receipt.json` (and the same fields inside `snapshot.meta.json`) exists to answer *"what was this graph deterministic over, and how complete is it?"* — the trust question before an agent relies on an `impact_analysis` or a `diff_snapshot`. It serves two consumers:

- **Provenance / audit.** enola version, git ref + dirty-tree status, the extractor/explainer sets actually used, a **config hash** (over the effective extractors/explainers/renderers/globs/output settings) and its narrower `ignore_glob_hash`, per-artifact output hashes, and a **snapshot ID** that is a *content fingerprint* (SHA-256 over the byte-stable fact serialization plus the version and config hash), not a random UUID — so re-running on identical inputs yields the same ID and it can key equivalence. Every hash value carries a `sha256:` prefix.
- **The improvement loop.** Extraction-quality metrics — files seen vs. parsed vs. skipped, the number of directory trees pruned, a parse-error count and sample, the count of heuristic (confidence < 1.0) insights, and the cross-repo coverage-gap / unresolved-edge rollup — give a machine-readable signal a consumer (a human, a `diff_snapshot`, or an agent improving enola itself) can poll to detect *thin extraction* (a missing detection, a bad ignore glob, a failing extractor) and turn it into targeted work. The same metrics appear as an **Extraction Quality** section in `llm_context.md`, so an agent reading the snapshot sees thin extraction without a tool call.

  The two skip counters mean different things, and a bad ignore glob is usually a *directory* glob. `files_skipped` counts ignored files the walker **visited** and dropped — those matched by a file glob like `**/*.test.ts`. An ignored directory is pruned whole (`filepath.SkipDir`), so its contents are never visited and appear in no count: it is tallied once, as one entry in `dirs_skipped`. Each `skipped_sample` entry names the glob that matched it, so *"why is this file missing from the graph?"* is a lookup rather than an investigation; directories appear there with a trailing slash.

Because the receipt fields live in `snapshot.meta.json`, they ride into every pinned/`previous` baseline, and `diff_snapshot` reads them to add a **comparability guard**: it warns (above the delta) when the baseline and current snapshots were *not* generated over equivalent inputs — a different repo, enola version, extractor set, or ignore-glob set — since a diff across a mismatched extractor set would report every one of that language's facts as spurious churn. `compare_receipts` surfaces the same verdict plus the metric deltas directly.

#### The graph receipt, and why each workspace keeps its own

Alongside the per-snapshot receipt in `.enola/`, a snapshot writes a **graph receipt** describing the whole graph the server holds — every repo in it, where each lives, its git state and fact count. That is what a restart reads (`bootstrap.AutoLoadSnapshot`) to reload a *multi-repo* graph without re-running any extractor: the per-repo `.enola/` dirs alone cannot say which repos were appended.

It is written to two places ([`internal/engine/global_receipt.go`](internal/engine/global_receipt.go)):

- `~/.enola/graphs/<repo-base>-<hash8>.json` — keyed by the repo the server was *launched for* (`cfg.Repo`), stable across appends.
- `~/.enola/receipt.json` — the machine-wide copy, kept for tooling that already reads it.

The machine-wide file necessarily describes only whichever server generated last, so with one server per agent terminal it is the wrong thing to restore from: a server launched in repo A would come back holding the repos another terminal had snapshotted, then answer every query about the wrong codebase. `AutoLoadSnapshot` therefore prefers this workspace's own receipt, falls back to the machine-wide one **only when it actually covers `cfg.Repo`** (`receiptCovers`), and otherwise restores just `cfg.Repo` — or nothing, leaving the agent to run `generate_snapshot`. Starting empty is recoverable and visible; starting with someone else's graph is neither.

#### Relation to the issue #60 proposal

The receipt is a **functional superset** of the manifest proposed in [issue #60](https://github.com/enola-labs/enola/issues/60) — every data point the proposal asked for is present — but it keeps a flatter, richer shape rather than the proposal's exact grouping. The mapping:

| Issue #60 field | enola field | Note |
|---|---|---|
| `repo.path_label` | `repo_path` | |
| `repo.git_ref` / `repo.dirty` | `git.ref` + `git.commit` / `git.dirty` | ref and commit kept separate |
| `enola.version` | `enola_version` | |
| `enola.config_hash` | `config_hash` | superset of `ignore_glob_hash`, also present |
| `enola.enabled_extractors` | `extractors` | the *used* set (a subset of enabled) |
| `input_scope.*` | `quality.files_seen/parsed`, `quality.skipped_sample`, `ignore_glob_hash` | grouped under `quality`, not a separate `input_scope` |
| `quality.parse_errors` / `heuristic_insights` | `quality.parse_errors` / `quality.heuristic_insights` | |
| `quality.unresolved_edges` / `coverage_gaps` | `quality.coverage.*` | multi-repo only |
| `outputs.facts_hash` / `llm_context_hash` | `output_hashes["facts.jsonl"]` / `["llm_context.md"]` | a map, so new artifacts extend it |
| `outputs.graph_hash` | — (≡ `facts.jsonl`) | enola has no separate persisted graph artifact; `facts.jsonl` *is* the graph (facts + edges), so its hash covers it |

`llm_context.md` is the human- and agent-readable digest. It's prioritized and truncated to the configured token budget, and includes (as space allows): a repository map of modules, the detected architecture pattern, cross-repo dependencies, entry points, routes, storage, dependency rules, the most critical modules (by fan-in/fan-out), risk zones (cycles and layer violations), and an architecture-aware "how to add a feature" guide.

---

## Cache identity — why a snapshot cannot depend on which binary wrote the cache

The extractor cache is keyed by `cacheVersion` plus, per extractor, a content hash of the files it owns. `cacheVersion` is a constant a human bumps when an extractor's output changes — and that is exactly the problem: a missed bump means every entry the old binary wrote keeps being served by the new one, for files whose contents never changed, silently.

The failure mode is not theoretical, and it is the worst shape available: a snapshot served from a stale cache disagrees with a cold run of the same binary on the same tree, so a `baseline pin` and an `enola check` landing on opposite sides of the transition report facts as **added** or **removed** that nobody touched. For a gate whose entire value is "a clean diff means something", facts that depend on which binary happened to write the cache are more damaging than no cache at all.

So the cache also records a **build identity** — the release version plus the executable's size and modification time — and a cache written by any other binary is discarded wholesale rather than partially reused. Entries carry no record of which extractor logic produced them, so there is no sound way to keep part of one.

- **Size and mtime, not a content hash of the binary.** This runs on every snapshot and the binary is tens of megabytes; a stat is free. The failure mode of the cheap check is a needless re-parse (a rebuilt-but-identical binary), never a stale reuse — the right direction to be wrong in.
- **It complements `cacheVersion` rather than replacing it.** `cacheVersion` still expresses *semantic* invalidation and is still enforced by [`internal/cachecov`](internal/cachecov); the build stamp is the automatic backstop for when a bump is forgotten, and it is what makes iterating on an extractor locally safe without remembering to delete `.enola/extractor_cache.json` between builds.
- **A cache predating the stamp has an empty build field**, which fails the comparison and is discarded — the correct outcome, since it cannot say what produced it.

## Determinism & incremental updates

Two properties hold the whole design together:

- **No model in the loop.** Extraction and analysis never call an LLM. The graph is a function of your source code and the configured plugins — reproducible across runs and machines. The LLM enters only *downstream*, as the consumer of the snapshot.
- **Incremental by content hash.** `snapshot.meta.json` records a SHA-256 for every file. On a re-run, only files whose hash changed are re-parsed, so refreshing a snapshot on a large repo is fast.

The receipt's **snapshot ID** is the determinism guarantee made explicit and checkable: it is a `sha256:` fingerprint over the byte-stable fact serialization (plus the enola version and the effective-config hash), *not* a random UUID — so two runs on the same commit with the same config produce byte-identical IDs. That is what lets `compare_receipts` treat a matching ID as "provably the same graph over the same inputs."

Together these mean the architectural map an agent relies on is both *trustworthy* (it reflects the code, not a guess) and *cheap to keep current* (regenerate after changes without re-scanning everything).

## Why a snapshot, not a store

enola computes the graph when asked and keeps the result as a value, rather than maintaining one graph that is updated as your files change. That is not an implementation detail — it is forced by what enola produces. A verdict on a change is a function of two states, and the edit you want graded is the same edit that would update a graph maintained in place: whatever keeps such a graph current fires on exactly the event whose effect you were trying to measure, and the *before* is gone by the time you can ask about it.

So a baseline is a file set you keep rather than a position a process is in, which is why `.enola/baseline/` survives re-snapshots, publishes atomically, and travels to another machine. And the design is only affordable because recomputation is cheap at the cadence a verdict is needed — twice per task: 34 of the 38 benchmarked repositories re-snapshot warm in under five seconds, the Linux kernel in 33.9s.

The full argument, the consequences it forces, and the cases where a continuously-maintained graph is the better design: **[docs/SNAPSHOTS.md](docs/SNAPSHOTS.md)**.

---

## License & acknowledgements

Apache License 2.0 — see [`LICENSE`](LICENSE). Third-party components are listed in [`NOTICE`](NOTICE). Swift parsing uses the [tree-sitter-swift](https://github.com/alex-pinkus/tree-sitter-swift) grammar by Alex Pinkus (MIT), vendored under [`internal/extractors/swiftextractor/grammar/`](internal/extractors/swiftextractor/grammar/).
