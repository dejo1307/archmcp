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
│   Extractors     │  Swift · Ruby · C/C++ · PHP · OpenAPI (source → facts)
└──────────────────┘
   │
   ▼
┌──────────────────┐
│   Fact Store     │  indexed by kind / file / name / repo
└──────────────────┘
   │
   ▼
┌──────────────────┐
│ Cross-Repo Linker│  only with 2+ repos loaded
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
4. **Cross-repo linker** — only when two or more repos are loaded. It connects the per-repo graphs by matching HTTP client/server routes and shared-library imports, emitting `service` nodes and cross-repo dependency edges. The link set is recomputed from scratch on every append, so it always reflects exactly the repos currently loaded.
5. **Graph index** — builds the bidirectional graph (with the synthetic edges above) that powers `traverse`, `find_path`, and `impact_analysis`.
6. **Explainers** — run deterministic analyses over the facts and emit insights (next section).
7. **Renderer** — produces `llm_context.md`, a compact, token-budgeted architecture summary an agent can read directly.
8. **Artifacts** — everything is written to `.enola/` (see [Output artifacts](#output-artifacts)). Content hashes enable incremental re-extraction on the next run.

Three plugin roles drive the middle of the pipeline — **extractors** (source → facts), **explainers** (facts → insights), and **renderers** (snapshot → artifacts). Each is a small Go interface with a registry, so adding a language or an analysis is a self-contained addition rather than a change to the engine.

**One-shot explain mode.** `enola --explain [repo_path]` is an alternative exit path through the pipeline: stages 1–6 run normally, but instead of proceeding to stage 7 (Renderer) and stage 8 (Artifacts), `pkg/explain.Compute()` reads the fact store, produces a `Report` struct, and `report.Render()` prints a human-readable statistical summary to stdout. No artifacts are written; `.enola/` is not touched. See [The explain package (`pkg/explain`)](#the-explain-package-pkgexplain) below.

---

## Insights (explainers)

Explainers turn raw facts into architectural observations. Each insight carries a **confidence** score: `1.0` means it's a structural fact, below `1.0` means it's a heuristic. Every insight is also tagged with the explainer that produced it (`Insight.Source`), and the whole set is retrievable through the **`query_insights`** tool — filter by `explainer`, `repo`, or `min_confidence` — so an agent fetches a finding directly instead of re-deriving it from raw facts or scraping it out of `explore depth=2` / `.enola/insights.json`.

- **Cycles** ([`internal/explainers/cycles`](internal/explainers/cycles/cycles.go)) — finds cyclic module dependencies using **Tarjan's strongly-connected-components algorithm**. A cycle either exists in the import graph or it doesn't, so these land at confidence `1.0`, with every module in the cycle listed as evidence.
- **Layers** ([`internal/explainers/layers`](internal/explainers/layers/layers.go)) — recognizes common architectural shapes by matching module paths against known patterns: **hexagonal** (application / port / adapter / domain / …), **Next.js** (pages / components / hooks / lib / api / …), and **Go-standard** (cmd / internal / pkg / api). Confidence is computed from how much of the codebase matches. It also flags **layer violations** — an inner layer importing an outer one — as lower-confidence heuristic warnings.
- **Cross-repo** ([`internal/explainers/crossrepo`](internal/explainers/crossrepo/crossrepo.go)) — summarizes the cross-repo edges found by the linker. Returns nothing for a single-repo snapshot.
- **Coverage** ([`internal/explainers/coverage`](internal/explainers/coverage/coverage.go)) — turns the per-service `edge_coverage` counts the linker records into **coverage-gap** insights: a service with no resolved outbound edges but unresolved outbound call sites is flagged as a blind spot ("appears isolated but…"), distinct from one that is genuinely a leaf. Distinguishes absence of edges from a gap in coverage. Returns nothing for a single-repo snapshot. Surfaced programmatically by the `coverage_report` tool.
- **Unused-routes** ([`internal/explainers/unusedroutes`](internal/explainers/unusedroutes/unusedroutes.go)) — the **server-side inverse** of the cross-repo HTTP linker: it rolls up the `route` facts that *no loaded client calls* (tagged `unmatched_by_clients` during linking — see [Finding unused endpoints](#finding-unused-endpoints)) into one candidate-cleanup insight per service. Deliberately conservative: it only considers repos that actually serve a cross-repo client (an HTTP *provider* — never a frontend's own page routes), skips low-signal generic paths (`/health`, single-segment), and biases toward false negatives. Each insight carries the mandatory caveat that candidates are unused *by the loaded clients only* — consumers outside the snapshot (admin scripts, cron, webhooks, third-party clients, deep links) don't appear, so verify before deleting. Confidence `0.6` (a candidate to review, not a verdict). Returns nothing for a single-repo snapshot.
- **God-class** ([`internal/explainers/godclass`](internal/explainers/godclass/godclass.go)) — flags symbols with an outlier **fan-in** (depended upon by far more symbols than average), computed from the graph's reverse adjacency list. High fan-in concentrates change risk.
- **Hotspots** ([`internal/explainers/hotspots`](internal/explainers/hotspots/hotspots.go)) — flags call-graph **pinch points** (symbols with both high fan-in and high fan-out, scored `fanIn × fanOut`). A cheap degree-centrality proxy for betweenness — chokepoints most call chains pass through.
- **Dependency-depth** ([`internal/explainers/depth`](internal/explainers/depth/depth.go)) — flags modules whose **longest transitive import chain** is unusually long (cycle-safe longest-path over the module graph). Deep modules are slow to grasp and widen rebuild/retest impact.
- **Exported-surface** ([`internal/explainers/surface`](internal/explainers/surface/surface.go)) — flags **large public surfaces**: sizeable modules that export almost all their symbols, so they encapsulate little. Because "public is the default" in Go and Ruby (so a raw ratio test floods), it skips mock/test/generated packages, requires a meaningful size and a near-total export ratio, and reports only the **top N worst offenders** (largest public surface first) rather than every match — a digestible shortlist for a visibility review, not a list of definite defects.
- **Complexity-outliers** ([`internal/explainers/complexity`](internal/explainers/complexity/complexity.go)) — flags functions/methods whose **cyclomatic complexity** is a statistical outlier, using the language-agnostic `cyclomatic` prop every extractor records.

The shared module-graph construction and statistical-outlier helpers used by several of these live in [`internal/explainers/common`](internal/explainers/common/common.go).

---

## The explain package (`pkg/explain`)

`pkg/explain` ([`pkg/explain/explain.go`](pkg/explain/explain.go)) is a **public** package rather than `internal/` for one reason: `enola-enterprise` imports it to append its own license-gated sections (dead code, package metrics) to the base `Report` before rendering. It is the only package in the OSS codebase with that cross-module consumer.

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

Pins the current snapshot as a diff baseline by copying the snapshot artifacts (`facts.jsonl`, `insights.json`, `snapshot.meta.json`) into `.enola/baseline/`. Call it once at the start of a task, after the first `generate_snapshot`. The pinned baseline **survives subsequent `generate_snapshot` runs**, so it stays valid across several rounds of edits — unlike the auto-rotated `.enola/previous/`, which only ever holds the immediately-preceding run. Takes no parameters.

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

The typical loop is `generate_snapshot → set_baseline → edit → generate_snapshot → diff_snapshot`.

| Parameter | Description |
|-----------|-------------|
| `baseline` | What to compare against: `pinned` (default — the `set_baseline` snapshot), `previous` (the immediately-preceding run, auto-rotated into `.enola/previous/`), or an explicit path to a directory holding `facts.jsonl`. |
| `focus` | Optional: narrow the report to entries referencing a module/file/symbol (substring), to verify just what you touched. |
| `output_mode` | `summary` (default — headline regressions/improvements + structural tally) → `compact` (adds finding descriptions, evidence, and the changed edges/facts) → `full` (complete JSON). |
| `max_tokens` | Optional hard cap on output size. |

The engine lives in [`internal/diff`](internal/diff/diff.go) (pure `Compute` + deterministic renderers) and is re-exported for out-of-module use via [`pkg/diff`](pkg/diff/diff.go); baseline persistence and the on-disk loader live in [`internal/engine/baseline.go`](internal/engine/baseline.go). `diff_snapshot` also runs the **comparability guard** (`diff.CompareMeta`): it reads the receipt fields on both snapshots' metadata and warns, above the delta, when they were not generated over equivalent inputs.

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

Appending several repos does more than pool their facts — a linking pass connects the per-repo graphs using three signals the extractors already capture:

- **HTTP route role matching** — a route a repo *calls* (`role:"client"`, e.g. from a generated OpenAPI client) is matched to a route another repo *serves* (`role:"server"`, or a framework route) by normalized path + method. The caller is recorded as depending on the servee.
- **Import / shared-lib references** — an import whose scope or leading segment names another loaded repo (e.g. `@app-web/lib-api`, `lib-core/money`) records a dependency on that repo.
- **Shared symbol surface** — when two repos declare enough of the same distinctive types (e.g. a vendored protocol header copied between them — the `onelab::*` / `GmshClient` / `GmshServer` classes shared by *gmsh* and *getdp*), they are coupled. The match is on each type's portable identity (the namespace-qualified name with the repo-specific directory prefix stripped), filtered to type-like symbols (class/struct/interface/enum) and to distinctive names — namespaced identities always count; bare names must be non-generic and reasonably long. A pair links only above a small shared-type threshold, so an incidental `Config`/`JsonParser` collision can't fabricate a dependency. This relationship is symmetric, so it is emitted as a **bidirectional** pair of edges marked `via:"shared_symbols"`.

These become real, queryable facts:

- A `service` node per repo (`query_facts kind=service`), named by its repo label.
- A cross-repo dependency edge per `consumer → provider` pair, carrying the matched endpoints, import samples, and shared-symbol samples.

Because they're ordinary graph nodes and edges, the traversal tools become cross-repo aware with no extra steps — `traverse`, `find_path`, and `impact_analysis` all reach across repo boundaries. The cross-repo dependencies also appear as a **Cross-Repo Dependencies** section in `llm_context.md`, so an agent reading the snapshot sees them without running a tool.

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

> **Config note:** the `crossrepo` explainer (which adds a cross-repo entry to `insights.json`) must be listed under `explainers:` in your config — the bundled configs already include it. The `service` nodes, graph edges, traversal, and the `llm_context.md` section work regardless of explainer config; only the `insights.json` entry depends on it.

---

## Configuration

The config file is **optional**. Every field has a built-in default (see `config.Default()` in [`internal/config/config.go`](internal/config/config.go)), so with no `mcp-arch.yaml` enola runs entirely on those defaults — the file only *overrides* the keys you set. Create one (or pass a custom path as the first CLI argument) when you want to depart from the defaults below:

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
  - cpp
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

The bundled [`mcp-arch.yaml`](mcp-arch.yaml) ships a much fuller `ignore` list (Android/Gradle, Xcode/SPM, Rails, CI, Docker, env files, …); see it and the per-language configs under [`examples/`](examples/) for ready-made starting points.

| Field | Description | Default |
|-------|-------------|---------|
| `repo` | Repository root path | `"."` |
| `ignore` | Glob patterns for files/dirs to skip | vendor, node_modules, .git, tests, build dirs, minified JS (`*.min.js`/`*.bundle.js`), docs, config data, … |
| `extractors` | Enabled extractors | `["cpp", "go", "java", "kotlin", "openapi", "python", "typescript", "swift", "ruby"]` |
| `explainers` | Enabled explainers | `["cycles", "layers", "crossrepo", "coverage", "unused-routes", "god-class", "hotspots", "dependency-depth", "exported-surface", "complexity-outliers"]` |
| `renderers` | Enabled renderers | `["llm_context"]` |
| `output.dir` | Output directory for artifacts | `".enola"` |
| `output.max_context_tokens` | Token budget for `llm_context.md` | `16000` |
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
| C/C++      | tree-sitter      | a C source (`.c`) or C++ source (`.cpp`/`.cc`/`.cxx`/`.hpp`/...) present, or a build file (`CMakeLists.txt`/`Makefile`/`meson.build`/`*.vcxproj`) plus any header |
| PHP        | tree-sitter      | `composer.json`, a WordPress bootstrap file (`wp-load.php`/`wp-settings.php`/`wp-config.php`), or any `.php` source within 3 directory levels |
| OpenAPI    | YAML/JSON scanner| any file containing `openapi:` or `swagger:` |
| gRPC       | proto3 scanner   | any `.proto` file present |

**Go** uses the standard-library parser directly, so symbols, methods, interfaces, imports, and call edges are exact.

**TypeScript** (tree-sitter) includes Next.js route detection (App Router and Pages Router), monorepo detection one level deep, and parsing of `openapi-typescript`-generated client files — each operation is emitted as a `route` fact with `role:"client"`. App Router route groups like `(standard)` are stripped from URLs. Like the other AST extractors it walks function/method bodies for the complexity metrics (`cyclomatic`, `loop_depth`, `loop_count`, `calls_in_loop`, `recursive_self`) the enterprise `analyze_performance` tool consumes, and — mirroring Swift — it tags a body `io_direct` when it directly invokes a network/file primitive (`fetch`, `axios`, `fs.readFile`, `navigator.sendBeacon`, `new WebSocket`/`XMLHttpRequest`/`EventSource`) **or** calls a binding imported from a network module (a known HTTP-client package, or any path with a `network` segment — e.g. a `request` helper from a `.../lib/network/request` module). A serial post-pass (`computeTSPerformsIO`) then propagates that flag transitively over the `calls` graph into a `performs_io` prop via a cycle-safe monotone fixpoint, so a function reaching the network only through wrapper helpers is still flagged — letting the analyzer catch a per-iteration network call (an N+1) hidden behind a wrapper. *Limitation:* default-imported internal wrappers aren't resolved by the call-edge pass, so the fixpoint does not cross that hop; in practice most wrappers call their I/O sink directly (so they are seeded `io_direct` without needing the edge), and the analyzer's short-name I/O index still matches the in-loop call.

**Vue** support is integrated within the TypeScript extractor. `.vue` Single File Components are handled natively — the extractor parses each SFC's `<script>` and `<script setup>` blocks (case-insensitive, with `lang` attribute detection for TypeScript vs JavaScript) and feeds them through the existing tree-sitter TypeScript pipeline. Detection checks for a `"vue"` dependency in `package.json` (TypeScript root first, then repo root fallback); **Nuxt** is additionally detected by `nuxt.config.js/ts/mjs` or a `"nuxt"` package dependency. Each `.vue` file emits a `symbol` fact for the component, named `<dir>.<ComponentName>` (kebab-case converted to PascalCase), carrying `web_component: "component"`, `framework: "vue"` or `"nuxt"`, and `vue_setup: true` when using `<script setup>`. Functions named `use*` anywhere in the project are automatically classified as composables (`web_component: "composable"`). **Nuxt file-based routing** emits one `route` fact per file under `pages/`, with the URL derived from the file path — index files resolve to `/`, dynamic segments like `[id].vue` are preserved — each with `method: "GET"` and `router: "pages"`. Files containing a `createRouter()` call are emitted as a route fact with `type: "router_config"`. Import statements in all script blocks become `dependency` facts, and call edges from method bodies participate in `traverse`, `find_path`, and `impact_analysis`. Vue detection runs automatically inside the `typescript` extractor — no separate entry is needed under `extractors:` in config.

**JavaScript** (`.js`/`.jsx`) is handled by the TypeScript extractor. Tree-sitter's TypeScript parser natively parses JavaScript (JS is a subset of TS), so all extraction features — imports, declarations, call graphs, JSX component detection — work identically for `.js` and `.jsx` files. No separate configuration is needed; any project detected by the TypeScript extractor will have its JavaScript files processed automatically alongside TypeScript files. **Minified/bundled JS is skipped**: before parsing, any file with a line longer than ~2000 characters is treated as a generated artifact and produces no facts (a directory containing only such files emits no module either), so checked-in vendor bundles do not distort the graph or the enterprise complexity/performance analyses.

**Svelte** support is integrated within the TypeScript extractor, following the same pattern as Vue. `.svelte` Single File Components are parsed by extracting `<script>` and `<script module>` blocks (Svelte 5 syntax; the older `<script context="module">` form from Svelte 4 is also supported) and feeding them through tree-sitter. Detection checks for a `"svelte"` dependency in `package.json`; **SvelteKit** is additionally detected by `svelte.config.js/ts/mjs` or a `"@sveltejs/kit"` package dependency. Each `.svelte` file emits a component fact with `web_component: "component"`, `framework: "svelte"` or `"sveltekit"`. **SvelteKit file-based routing** emits route facts for `+page.svelte`, `+layout.svelte`, `+error.svelte`, and `+server.ts` files under `src/routes/` — route groups in parentheses like `(groupName)` are stripped from URLs, dynamic segments like `[slug]` and catch-all `[...rest]` are preserved. Server-side load files (`+page.server.ts`, `+layout.server.ts`) are not emitted as routes. The SvelteKit `$lib` path alias is automatically resolved to `src/lib/`, ensuring imports like `$lib/utils` appear as internal dependency edges rather than unresolved externals.

**Python** is parsed with tree-sitter (the concrete syntax tree handles nested classes/methods and docstrings natively, replacing the older indentation scanner). It understands **FastAPI/Starlette** route decorators and **Django** routes — `@api_view([...])` and `urls.py` `path()`/`re_path()` — emitting a `route` fact per endpoint. It emits `storage` facts for **SQLAlchemy** `__tablename__` and **Django models** (table name inferred from the class name), and classifies Django views and serializers via a `django_component` prop. It captures `async def` (`async: true`), decorator props (`@property`, `@staticmethod`, `@classmethod`, `@abstractmethod`, and Celery `@task`/`@shared_task`), and return-type hints. Each class emits an `implements` edge per base class, with generic type parameters stripped (`CRUDBase[Model, Id]` → `CRUDBase`), and both `import` forms become `dependency` facts. Crucially, the Python extractor now walks function and method bodies for call sites, emitting `calls` and `instantiates` edges (filtering out builtins) — so Python code participates in the dependency/call graph and is reachable by `traverse`, `find_path`, and `impact_analysis`. Monorepo detection walks up to 3 levels.

**Java** (tree-sitter) is framework-aware for the JVM server ecosystem. It emits symbol facts for classes, interfaces, enums, records, and annotation types, plus their methods, constructors, and fields, named with enola's `<dir>.<Type>` / `<dir>.<Type>.<method>` convention (nested types are qualified through the enclosing type). `extends`/`implements` become `implements` edges, `new X()` becomes `instantiates`, same-class method calls become `calls`, and both import forms become `dependency` facts split into internal vs. external. Because Java imports are explicit, type-reference edges are resolved through a project-wide fully-qualified-name index built in a second pass — so `implements`/`instantiates`/`injects` targets point at the canonical declaring symbol in another file or module rather than a bare name. Framework specialization covers **Spring MVC** (a `@RestController`/`@Controller` class's `@RequestMapping` base path is combined with method-level `@GetMapping`/`@PostMapping`/`@PutMapping`/`@DeleteMapping`/`@PatchMapping`/`@RequestMapping(method=…)` into one `route` per endpoint, carrying the HTTP method and the handler symbol), **Spring stereotypes** (`@Service`/`@Component`/`@Repository`/`@Controller`/`@Configuration` classified via a `component` prop), **dependency injection** (`@Autowired` fields, constructor injection, and Lombok `@RequiredArgsConstructor` over `final` fields → `injects` edges), and **JPA / Spring Data storage** (`@Entity` → a `storage` fact with `storage_kind: entity`; `@Repository` and `JpaRepository`/`CrudRepository`-style interfaces → `storage_kind: repository`). A `@Table(name = …)` is captured, and when the name is given as a `static final String` constant it is resolved to its literal value — the original identifier is preserved in a `table_constant` prop. **Apache Dubbo** is recognized too: `@SPI`/`@Activate`/`@DubboService` tag the type with `framework: "dubbo"` (`dubbo_spi`, `dubbo_activate`). Detection requires Maven (`pom.xml`) or real `.java` sources, so a pure-Kotlin Gradle project is left to the Kotlin extractor.

**Kotlin** is Android-aware: it detects Jetpack Compose (`@Composable`), Hilt DI (`@HiltViewModel`, `@Module`, `@AndroidEntryPoint`), Room (`@Entity`, `@Dao`, `@Database`), ViewModels, Repositories, Use Cases, and Workers.

**Swift** (tree-sitter) emits symbol facts for classes, structs, enums, protocols, and extensions plus their methods, initializers, and properties, named `<targetDir>.<Type>.<member>` — where `<targetDir>` is the file's resolved SPM/XcodeGen *target* module (parsed from `Package.swift` and `project.yml`), not its leaf directory. Members declared inside a type are classified `symbol_kind: method`; free functions stay `function`. It walks bodies for the call graph: same-type `self.`/`self?.` dispatch, member calls on any receiver (`coordinator?.show()`, `delegate?.tap()`), and cross-`extension` calls all become `calls` edges — emitted as bare short names at walk time (extraction is parallel-per-file) and bound in a serial post-pass against a project-wide method index (unique name → the qualified `dir.Type.method`, ambiguous → the bare name still matched by short name, unmatched → dropped so stdlib/framework calls don't create phantom edges). A further post-pass resolves **inherited-method calls** — a subclass or protocol conformer calling a base-class / protocol-extension method — by walking the caller type's supertype chain (from the `implements` edges) nearest-first and rewriting the otherwise-dangling call target to the declaring ancestor's method fact (`dir.DataModel.runRequest`), so class/protocol hierarchies are traversable for impact analysis, dead-code, and the performs_io closure. `Foo()` → `instantiates`, constructor/property DI → `injects`, SwiftUI `View`→`ViewModel` → `depends_on`, and custom-operator usage (`a <- b`, but not stdlib operators like `+`/`<=`) → a `calls` edge to the operator. Top-level calls in `#!/usr/bin/swift` scripts are captured via a file-scope reference fact. Like the other AST extractors, it walks function/method bodies — and also computed-property getters and `willSet`/`didSet` observers — for the standard complexity metrics `cyclomatic`, `loop_depth`, `loop_count`, `calls_in_loop`, and `recursive_self`, which the enterprise `analyze_performance` tool consumes; syntactic `for`/`while`/`repeat-while` and iterator closures (`map`/`forEach`/`filter`/…) count as loops, but **constant-bounded loops do not add scaling depth** — a literal integer range (`for i in 0..<10`), a literal-bound `stride(...)`, or an iterator over an array/dictionary literal or ALL-CAPS constant (`STOP_CHARS.forEach`) runs a fixed number of times, so it never inflates a genuine O(n) into a false O(n²)/O(n³). A method whose body invokes a network/file I/O primitive (`URLSession`/`dataTask`/`.data(for:)`, Alamofire `request`/`download`/`upload`, `Data(contentsOf:)`) is tagged `io_direct`; a serial post-pass then propagates that up the call graph into a transitive `performs_io` prop — crossing ambiguous kept-bare member-call edges by expanding them through the method-name index (bounded) rather than mutating the graph — so the enterprise `analyze_performance` tool can flag a per-iteration network N+1 (a loop calling a method that transitively hits the network) even when the I/O sits behind wrapper layers. It is **iOS-aware**: SwiftUI views (`View`/`App`/`Scene`), UIKit (`UIViewController`/`UIView` subclasses), Combine view models (`ObservableObject`, `@Observable`), architectural roles (Repositories, Use Cases, Coordinators, Services, DI containers), and `@MainActor`. *Limitation:* the vendored tree-sitter-swift grammar cannot parse a few advanced constructs — notably a tuple-type metatype `(A, B).self` (e.g. `withTaskGroup(of: (UUID, Result<T, Error>).self)`) — and its error recovery then flattens the whole enclosing type to file scope, so that file's type node is lost and its methods surface as top-level `function` symbols (~3% of files in a large iOS codebase). Dead-code detection stays accurate on these — a member call whose method was flattened falls back to resolving against the top-level function of that name (a rare same-name collision biases toward a missed lead, never a false accusation) — but the type's coupling/impact edges are degraded for the affected file until the construct is removed or the grammar gains support.

**OpenAPI** scans for spec files independently of the main walker (so it finds them even when `*.yaml`/`*.json` are globally ignored), confirming candidates by an `openapi:`/`swagger:` key. It emits one `route` per operation enriched with method, `operationId`, summary, tags, and a spec back-reference; specs under an `openapi/client/` directory are marked `role:"client"`. Gateway extensions (`x-gateway-config`, `x-gateway-capabilities`) are parsed into props.

**gRPC** models Protocol Buffers services the same way HTTP endpoints are modeled, so a gRPC surface answers the same cross-repo and unused-endpoint questions as a REST one. A small dependency-free proto3 scanner (comment-stripped, brace-depth aware — the same class of parser as OpenAPI's) reads each `.proto` and emits, for every `rpc`, a **server-role `route`** whose `Name` is the gRPC wire path `/pkg.Service/Method` (e.g. `/users.v1.UserService/GetUser`) with `method:"POST"`, `framework:"grpc"`, `source:"grpc-proto"`, `type:"grpc"`, and `rpc_service`/`rpc_method`/`streaming` props — the exact path+method a gRPC-web client hits over HTTP, so these flow through the cross-repo linker's normalized path+method matching and the `unused-routes` explainer with no linker special-casing. Each service also emits an `interface` symbol, each RPC a `method` symbol (`has_method`-linked to its service), and each message a `struct`/`enum` symbol, and proto `import`s become `dependency` facts — so proto participates in `traverse`, `find_path`, and `impact_analysis`. **Client-side detection** covers both TypeScript and Go. In the **TypeScript** extractor a repo-wide pre-pass resolves generated stubs — `@protobuf-ts` (`new ServiceType(...)`), connect-es (`typeName`), and **classic grpc-web** (where it derives the service and methods from the `MethodDescriptor`/`rpcCall` `/pkg.Service/Method` path literals) — into a service→method map, then per-file it binds `new XxxServiceClient(...)` variables (including typed constructor-injected fields) and emits a **client-role `route`** (`source:"ts-grpc-client"`) for each `client.method(...)` **call site** — only for methods actually called, so an RPC the frontend never invokes correctly surfaces as unmatched by clients. The **Go** extractor does the same for grpc-go consumers: because a Go call site (`client.GetUser(ctx, req)`) carries no wire path, a repo-wide pre-pass reads the authoritative `/pkg.Service/Method` from the *generated* code — the concrete client's `Invoke`/`NewStream` string literal (grpc-go, unary + streaming) or the `…Procedure` const (connect-go) — and builds a client-interface→method→path index. Per-file, it reuses the Go extractor's own receiver/field/local-variable type resolution (`resolveChain`) so a client is recognized whether it's a **local variable**, an **inline construction**, a **struct field** (`s.users.GetUser(...)` — dependency injection), or a **package-level var**, emitting a **client-role `route`** (`source:"go-grpc-client"`) per call site. Both **grpc-go** and **connect-go** consumers are covered. On the TypeScript side, **connect-es** consumers using `createClient(Service, transport)` / `createPromiseClient(...)` are detected alongside the `new XxxClient(...)` form. Cross-repo gRPC edges are tagged `via:"grpc"`. **Go handler binding:** a post-extraction pass connects each gRPC server route to the Go method that serves it via a `handled_by` edge (route → `pkg.Type.Method`) and a `handler` prop, so `impact_analysis`/`find_path` traverse from the RPC to its implementation (and, through the cross-repo edges, on to its clients). The bridge is the `protoc-gen-go-grpc` forward-compatibility convention — a server impl embeds `Unimplemented<Service>Server`, which the Go extractor already records as an `implements` edge, so the service short name matches the route's `rpc_service` with no new Go parsing; ambiguous or non-embedding impls are left unbound. *Scope:* client detection targets protoc-gen-go-grpc, connect-go, `@protobuf-ts`, connect-es, and classic grpc-web generated stubs; hand-rolled clients that bypass the generated stubs are not recognized (a namespaced commonjs grpc-web constructor, `new proto.pkg.XxxClient(...)`, binds only best-effort).

**Ruby** is parsed with tree-sitter, replacing the former line-based regex scanner — the grammar handles heredocs, endless methods (`def x = expr`), multi-line expressions, and the nested scopes that tripped up the line scanner. It is Rails-aware: ActiveRecord models (`has_many`/`has_one`/`belongs_to`/`has_and_belongs_to_many`, scopes, table inference, explicit `self.table_name`) emit `storage` facts; the route DSL in `config/routes.rb` (plus `config/routes/*.rb` and packwerk `draw`) is walked from the real block structure, so nested `namespace`/`scope`/`resources`/`member`/`collection` blocks produce one `route` per RESTful action (honoring `only:`/`except:`); and Packwerk package boundaries (`package.yml` dependency enforcement, `app/public/` privacy) are parsed. It tracks modules, classes, methods with `public`/`private`/`protected` visibility, `class << self` eigenclass and `module_function` methods — now correctly typed as class methods rather than instance methods — mixins (`include`/`extend`/`prepend` → `implements` edges), `ActiveSupport::Concern` (flagged `concern: true`), constants, and `attr_*` accessors. Like the other AST extractors, it walks method bodies for call sites, emitting `calls` edges (qualified `Const.method`/`Ns::Class.method` and receiver `var.method`, deduplicated) and `implements` edges for superclasses — so Ruby participates in `traverse`, `find_path`, and `impact_analysis`.

**C/C++** is parsed with tree-sitter and handles the header/source split that defines the language. The extractor owns both languages: `.c` files (and bare `.h` headers in a C context) are parsed with the **tree-sitter-c** grammar, while `.cpp`/`.cc`/`.cxx`/`.hpp`/... (and `.h` headers in a C++ context) use **tree-sitter-cpp**; every fact carries a `language` prop (`"c"` or `"cpp"`). A real C grammar is required rather than reusing the C++ one, because C code routinely uses C++ keywords as ordinary identifiers (`new`, `try`, `class`, `delete`, `private`, ...) plus C-only constructs (`_Generic`, `restrict`, GCC range designators) that the C++ grammar rejects. Bare `.h` headers are attributed **per directory subtree**: a header is treated as C++ only when its own subtree contains unambiguous C++ sources — so a handful of stray `.cpp` files (e.g. `tools/` in an otherwise pure-C kernel tree) no longer flip every `.h` in the repo to C++; absent that signal, `.h` defaults to C. In C, a `static` function has internal linkage and is emitted with `exported=false` (file-private); C++ keeps `exported=true`. Classes, structs, unions, enums (incl. `enum class`), namespaces, free functions and methods, data members, and `typedef`/`using` aliases become symbol facts named `<dir>.<ns1::ns2::Class::member>` — enola's `<dir>.` module convention on the outside, native C++ `::` scope inside. Because an out-of-line definition `Class::method` (parsed from a `qualified_identifier`) yields the same canonical name as its in-class declaration, a dedup pass **merges a header's method prototype with its `.cpp` definition** into a single symbol (the definition wins for file/line and carries the call-graph edges). Base classes become `implements` edges; method bodies are walked for `calls`/`instantiates` edges; quoted `#include "x.h"` becomes a `dependency` resolved to the declaring module, while system `<...>` includes are skipped. Templates are unwrapped to their inner declaration and flagged `templated`, and the walker descends through `#if`/`#ifdef` preprocessor guards (so code wrapped in `#if defined(HAVE_*)` and headers behind include guards are still extracted). Like the other AST extractors, it walks each function/method body for complexity metrics — `cyclomatic`, `loop_depth`, `loop_count`, `calls_in_loop`, and `recursive_self` (counting `for`/`while`/`do-while`/range-`for` and STL-algorithm lambdas like `for_each`/`transform` as loops) — which the enterprise `analyze_performance` tool consumes. *Limitation:* header/source merging relies on the `.h` and `.cpp` living in the same directory (the common layout); split `include/` + `src/` trees are not merged.

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
| `receipt.json` | The **snapshot receipt** — a compact manifest of what the graph was generated over (enola version, git ref + dirty status, a content-fingerprint snapshot ID, the extractor/explainer sets, ignore-glob hash, output-artifact hashes) and extraction-quality metrics (files seen/parsed/skipped, parse errors, coverage gaps). Read it via the `snapshot_receipt` tool. |
| `previous/` | The immediately-preceding snapshot, auto-rotated on each write — the `baseline='previous'` source for `diff_snapshot` |
| `baseline/` | A snapshot pinned by `set_baseline`, preserved across re-snapshots — the default `diff_snapshot` baseline |

### The snapshot receipt

`receipt.json` (and the same fields inside `snapshot.meta.json`) exists to answer *"what was this graph deterministic over, and how complete is it?"* — the trust question before an agent relies on an `impact_analysis` or a `diff_snapshot`. It serves two consumers:

- **Provenance / audit.** enola version, git ref + dirty-tree status, the extractor/explainer sets actually used, a **config hash** (over the effective extractors/explainers/renderers/globs/output settings) and its narrower `ignore_glob_hash`, per-artifact output hashes, and a **snapshot ID** that is a *content fingerprint* (SHA-256 over the byte-stable fact serialization plus the version and config hash), not a random UUID — so re-running on identical inputs yields the same ID and it can key equivalence. Every hash value carries a `sha256:` prefix.
- **The improvement loop.** Extraction-quality metrics — files seen vs. parsed vs. skipped, a parse-error count and sample, the count of heuristic (confidence < 1.0) insights, and the cross-repo coverage-gap / unresolved-edge rollup — give a machine-readable signal a consumer (a human, a `diff_snapshot`, or an agent improving enola itself) can poll to detect *thin extraction* (a missing detection, a bad ignore glob, a failing extractor) and turn it into targeted work. The same metrics appear as an **Extraction Quality** section in `llm_context.md`, so an agent reading the snapshot sees thin extraction without a tool call.

Because the receipt fields live in `snapshot.meta.json`, they ride into every pinned/`previous` baseline, and `diff_snapshot` reads them to add a **comparability guard**: it warns (above the delta) when the baseline and current snapshots were *not* generated over equivalent inputs — a different repo, enola version, extractor set, or ignore-glob set — since a diff across a mismatched extractor set would report every one of that language's facts as spurious churn. `compare_receipts` surfaces the same verdict plus the metric deltas directly.

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

## Determinism & incremental updates

Two properties hold the whole design together:

- **No model in the loop.** Extraction and analysis never call an LLM. The graph is a function of your source code and the configured plugins — reproducible across runs and machines. The LLM enters only *downstream*, as the consumer of the snapshot.
- **Incremental by content hash.** `snapshot.meta.json` records a SHA-256 for every file. On a re-run, only files whose hash changed are re-parsed, so refreshing a snapshot on a large repo is fast.

The receipt's **snapshot ID** is the determinism guarantee made explicit and checkable: it is a `sha256:` fingerprint over the byte-stable fact serialization (plus the enola version and the effective-config hash), *not* a random UUID — so two runs on the same commit with the same config produce byte-identical IDs. That is what lets `compare_receipts` treat a matching ID as "provably the same graph over the same inputs."

Together these mean the architectural map an agent relies on is both *trustworthy* (it reflects the code, not a guess) and *cheap to keep current* (regenerate after changes without re-scanning everything).

---

## License & acknowledgements

Apache License 2.0 — see [`LICENSE`](LICENSE). Third-party components are listed in [`NOTICE`](NOTICE). Swift parsing uses the [tree-sitter-swift](https://github.com/alex-pinkus/tree-sitter-swift) grammar by Alex Pinkus (MIT), vendored under [`internal/extractors/swiftextractor/grammar/`](internal/extractors/swiftextractor/grammar/).
