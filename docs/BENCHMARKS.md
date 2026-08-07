# Benchmarks

Everything here was measured on 2026-08-07, on 53 public open-source repositories,
with one binary, by scripts you can re-run. Where a number is unflattering it is
still here.

## What is measured, and why these five things

A code graph for agents is usually benchmarked on retrieval: how few tool calls,
how few tokens, how fast the agent finds the right file. Those are real questions,
and enola is not benchmarked on them here. They measure how quickly an agent
reaches code it was going to read anyway.

The questions below are the ones that decide whether a *structural verdict* about
your change can be believed:

| | The question | Why it cannot be skipped |
|---|---|---|
| **1** | Does the same commit produce the same graph? | A diff between two snapshots is meaningless if the snapshots are not reproducible |
| **2** | When something regresses, is exactly that reported? | A gate that also reports pre-existing problems gets switched off in a week |
| **3** | Of the cross-repo edges that exist, how many are found — and are the misses shown? | A resolved-only count is advertising |
| **4** | Does it finish on a real repository? | If it can't parse a real codebase, there's nothing to report on |
| **5** | Does an agent, given the tool, actually avoid shipping the regression? | The honest test of the whole idea |

Axes 1–3 have no counterpart in a retrieval benchmark, because you cannot report a
delta without a baseline, and you cannot report a miss without knowing what a hit
would have been.

## The corpus

53 repositories, 309,322 source files parsed, 5,461,021 facts carrying 18
distinct language tags (C, C++, C#, F#, Go, Java, Kotlin, PHP, Python, Razor, Ruby,
Rust, Swift, TypeScript, VB.NET, XAML, gRPC, OpenAPI). Public open-source only:
every row is a repository you can clone and re-run.

**53 of 53 reproduce** — identical `snapshot_id` and identical
`facts.jsonl` hash across a cold run and two warm ones, i.e. across cache states.

**.NET is now the largest single language in the corpus by facts**, ahead of
TypeScript, across fourteen repositories covering C#, VB.NET, F#, Razor/Blazor and
XAML. See [the .NET rows](#net) below.

| Repository | Language | Files parsed | Facts | Cold | Warm |
|---|---|---|---|---|---|
| linux | c | 55,399 | 1,892,342 | 166.3s | 48.6s |
| gitlab | ruby | 49,484 | 437,219 | 35.4s | 27.3s |
| runtime | csharp | 17,766 | 397,608 | 65.9s | 37.2s |
| rust | rust | 36,082 | 394,930 | 24.1s | 13.0s |
| roslyn | csharp | 17,117 | 360,842 | 23.6s | 16.3s |
| shopware | php | 13,727 | 218,750 | 13.1s | 7.0s |
| grafana | go | 10,313 | 167,987 | 8.9s | 6.7s |
| thingsboard | java | 6,371 | 158,992 | 5.8s | 4.3s |
| discourse | ruby | 12,102 | 126,003 | 8.8s | 6.2s |
| nextcloud-server | php | 6,037 | 101,464 | 6.6s | 2.9s |
| dubbo | java | 4,351 | 82,550 | 2.9s | 2.5s |
| bitwarden-clients | typescript | 4,895 | 73,445 | 4.2s | 2.8s |
| supabase | typescript | 6,919 | 70,211 | 6.6s | 3.3s |
| fsharp | fsharp | 1,769 | 68,379 | 2.7s | 2.3s |
| airflow | python | 4,066 | 68,163 | 9.7s | 6.4s |
| avalonia | xaml | 3,774 | 62,728 | 3.6s | 2.3s |
| deno | rust | 4,307 | 62,005 | 6.7s | 2.9s |
| orchardcore | razor | 6,979 | 53,369 | 4.6s | 2.6s |
| superset | python | 3,829 | 51,609 | 5.4s | 3.7s |
| cal.com | typescript | 4,596 | 48,643 | 3.5s | 2.0s |
| dbt-core | rust | 1,371 | 46,935 | 3.3s | 1.6s |
| wordpress | php | 2,748 | 45,424 | 5.8s | 1.6s |
| gmsh | cpp | 1,679 | 41,348 | 3.1s | 1.1s |
| powershell | csharp | 1,202 | 40,341 | 3.4s | 1.6s |
| bitwarden-server | csharp | 3,626 | 39,957 | 3.4s | 2.4s |
| chatwoot | ruby | 3,643 | 36,234 | 2.2s | 1.7s |
| mudblazor | razor | 3,170 | 35,987 | 2.6s | 1.2s |
| gitea | go | 2,220 | 35,492 | 1.9s | 1.6s |
| saleor | python | 2,474 | 30,193 | 3.4s | 2.5s |
| flarum | php | 2,687 | 28,273 | 1.5s | 0.9s |
| jellyfin | csharp | 1,883 | 27,481 | 1.8s | 1.0s |
| mcp | csharp | 2,033 | 22,666 | 2.2s | 1.5s |
| cognee | python | 1,489 | 16,911 | 2.0s | 1.1s |
| files | xaml | 1,275 | 16,272 | 1.3s | 0.7s |
| cognee-rs | rust | 846 | 15,827 | 1.2s | 0.6s |
| tokio | rust | 780 | 14,450 | 0.9s | 0.4s |
| crates-io | rust | 878 | 12,451 | 1.0s | 0.6s |
| solidus | ruby | 1,766 | 10,009 | 1.1s | 0.9s |
| excalidraw | typescript | 526 | 8,738 | 1.1s | 0.5s |
| isowords | swift | 382 | 5,615 | 0.5s | 0.3s |
| nowinandroid | kotlin | 312 | 5,110 | 0.5s | 0.4s |
| enola | go | 229 | 5,049 | 0.7s | 0.4s |
| nextcloud-collectives | php | 383 | 4,968 | 0.6s | 0.3s |
| getdp | cpp | 169 | 4,562 | 0.9s | 0.2s |
| csharp-sdk | csharp | 427 | 4,488 | 0.7s | 0.5s |
| eshop | csharp | 580 | 3,488 | 0.5s | 0.3s |
| elk | vue | 380 | 2,556 | 0.3s | 0.2s |
| nextcloud-contacts | php | 170 | 1,706 | 0.4s | 0.2s |
| giraffe | fsharp | 33 | 641 | 0.1s | 0.1s |
| grpc-web-example | grpc | 12 | 297 | 0.1s | 0.1s |
| sveltekit-realworld | svelte | 42 | 195 | 0.1s | 0.1s |
| cachet | php | 21 | 101 | 0.1s | 0.1s |
| orocrm | php | 3 | 17 | 0.1s | 0.1s |

### .NET

Fourteen repositories, 61,634 files, 1,134,247 facts — the
largest language block in the corpus. All fourteen reproduce.

| Repository | Files parsed | Facts | Cold | Warm |
|---|---|---|---|---|
| runtime | 17,766 | 397,608 | 65.9s | 37.2s |
| roslyn | 17,117 | 360,842 | 23.6s | 16.3s |
| fsharp | 1,769 | 68,379 | 2.7s | 2.3s |
| avalonia | 3,774 | 62,728 | 3.6s | 2.3s |
| orchardcore | 6,979 | 53,369 | 4.6s | 2.6s |
| powershell | 1,202 | 40,341 | 3.4s | 1.6s |
| bitwarden-server | 3,626 | 39,957 | 3.4s | 2.4s |
| mudblazor | 3,170 | 35,987 | 2.6s | 1.2s |
| jellyfin | 1,883 | 27,481 | 1.8s | 1.0s |
| mcp | 2,033 | 22,666 | 2.2s | 1.5s |
| files | 1,275 | 16,272 | 1.3s | 0.7s |
| csharp-sdk | 427 | 4,488 | 0.7s | 0.5s |
| eshop | 580 | 3,488 | 0.5s | 0.3s |
| giraffe | 33 | 641 | 0.1s | 0.1s |

The corpus is deliberately split so each mechanism has a control:

| Repo | What it is the control for |
|---|---|
| jellyfin | ASP.NET Core **attribute** routing — 420 routes |
| eShop | **minimal APIs** with `MapGroup` prefixes, EF Core, .NET Aspire |
| OrchardCore | **conventional** MVC routing, Razor Pages — 94% of its controllers carry no `[Route]` |
| roslyn | **VB.NET at scale** — its VB compiler is the largest VB codebase in the open |
| fsharp, Giraffe | **F#**; Giraffe ships no `.csproj` at all |
| MudBlazor | **Blazor** components — 1,987 `.razor` against 1,154 `.cs` code-behind |
| Avalonia, Files | **XAML** — Avalonia at scale, Files as a real WinUI 3 MVVM app |
| bitwarden-server | EF Core + Dapper; the backend half of the cross-repo cluster |
| runtime, PowerShell, mcp, csharp-sdk | scale, `partial`-heavy code, a 148-project monorepo, a library serving no HTTP |

**giraffe is the row that matters most.** It produced **0 facts** before this work —
detection matched its `.slnx`, the extractor claimed the repository, and reported a
successful snapshot of 46 unread F# sources. A green snapshot of nothing is worse
than an unsupported language, because nothing in the output says so.

## 1. Reproducibility

Each repository was indexed **three times** — once cold, twice warm — and the
receipt's `snapshot_id` and the SHA-256 of `facts.jsonl` were compared across all
three. Running cold then warm is the point: it tests that a cached run and a
from-scratch run agree, not merely that the same code path repeats itself.

> **53 of 53 repositories produced a byte-identical `snapshot_id` and a
> byte-identical `facts.jsonl` across all three runs — 159 runs, 5,461,021 facts,
> zero drift.** `insights.json` is byte-stable on all 53 as well.

This is the property everything else rests on. `snapshot_id` is
`sha256(facts ‖ enola version ‖ config hash)`, not a UUID, so two runs on the same
commit are provably the same graph. That's what lets `compare_receipts` refuse to
diff snapshots that are not comparable instead of reporting churn as your change.

An ID a third party can re-derive is the point rather than a hygiene property: it is
what makes a graph a value you can keep and compare, instead of a state you are
currently in. See [SNAPSHOTS.md](SNAPSHOTS.md).

## 2. Delta precision — the ratchet

The claim is that enola reports **what your change did** and stays silent about
everything that was already wrong. That is testable directly: pin a baseline, put
the repository through four states, and see what each one is graded as.

For each repository below, files were **added, never edited**, so every revert is a
delete. The cycle case adds two new modules that import each other.

| Repository | Language | Pre-existing findings | No change | Benign addition | Injected cycle | Reverted |
|---|---|---|---|---|---|---|
| gitea | Go | 171 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| enola | Go | 138 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| superset | Python | 133 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| cognee | Python | 121 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| excalidraw | TypeScript | 111 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| chatwoot | Ruby | 104 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| jellyfin | C# | 103 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| eshop | C# | 89 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| solidus | Ruby | 72 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| crates-io | Rust | 65 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| nowinandroid | Kotlin | 37 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| elk | TypeScript | 26 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| sveltekit-realworld | TypeScript | 1 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |
| cachet | PHP | 0 | PASS · +0 facts | PASS · +2–3 facts | **FAIL · 1 regression** | PASS · +0 |

Read the columns as four separate claims, all of which hold on all twelve:

- **No change → +0 facts, +0 edges, PASS.** Exactly zero on all twelve repositories.
  That's what makes a PASS or FAIL on a real change something you can rely on.
- **Benign addition → PASS**, with the delta naming exactly the 2–3 facts added.
  A new leaf module isn't a structural regression, so there's nothing to report.
- **Injected cycle → FAIL, exactly 1 regression** — out of **974 pre-existing
  findings across these repositories**, up to 159 in a single one. None of them was
  repeated. The ratchet holds.
- **Reverted → PASS again**, +0 facts. The verdict is a function of the tree, not
  of history.

Eight languages, one behaviour. A regression is not detected by pattern-matching a
language; it is a cycle in the module graph, computed by Tarjan's SCC over the
resolved import edges, at confidence `1.0`.

**Swift is deliberately absent**, and the reason is a property of the language rather
than a limit of enola. A Swift module is a *declared* SPM target, so two added
directories that import each other form no edge and no cycle, verified: the modules
and the symbols appear, the cycle does not. Injecting one would mean editing
`Package.swift`, and then "reverted" would be a file restore rather than a delete,
which is exactly the property that makes the no-change column worth reading.

### Why only cycles fail

Across the corpus enola produced **24,012 findings**. Broken down:

| Explainer | Findings | Class |
|---|---|---|
| hotspots | 20,625 | statistical outlier |
| layers | 585 | heuristic |
| **cycles** | **887** | **structural fact — the only one that fails a build** |
| god-class | 714 | statistical outlier |
| complexity-outliers | 489 | statistical outlier |
| exported-surface | 442 | candidate |
| dependency-depth | 270 | statistical outlier |

3.7% of findings are eligible to fail a build. The other 96.3% are reported and let
you through. That ratio is the design, not an accident: a gate that fails on one
thing, and says which, is a gate people leave switched on.

## 3. Cross-repo resolution, misses included

enola reports both resolved and unresolved edge counts, so you know how many
dependents a service actually has.

### On the committed multi-repo fixtures

Ten cross-language fixtures, each with a golden fact file the test suite asserts on:

| Fixture | Languages | Detected | Resolved | Unresolved | External | Edge confidence |
|---|---|---|---|---|---|---|
| ts_nest_multirepo | TypeScript ↔ TypeScript | 4 | 4 | 0 | 0 | verified |
| go_grpc_multirepo | Go ↔ Protobuf | 3 | 3 | 0 | 0 | verified |
| php_multirepo | PHP ↔ PHP | 2 | 2 | 0 | 0 | verified |
| py_fastapi_multirepo | Python ↔ TypeScript ↔ Go | 2 | 2 | 0 | 0 | — |
| py_grpc_multirepo | Python ↔ Protobuf | 1 | 1 | 0 | 0 | verified |
| multirepo | OpenAPI ↔ OpenAPI | 1 | 1 | 0 | 0 | verified |
| ts_express_multirepo | TypeScript ↔ JavaScript | 4 | 2 | **2** | 0 | verified |
| go_httpclient_multirepo | Go ↔ Go | 4 | 1 | **1** | 2 | probable |
| go_kafka_multirepo | Go ↔ Go (Kafka) | — | — | — | — | topic-matched |
| kotlin_swift_multirepo | Kotlin ↔ Swift | — | — | — | — | — |
| **Total** | | **21** | **16** | **3** | **2** | |

The three unresolved edges are all deliberate, and each is a documented limit rather
than a bug: a path too generic to attribute (`/healthcheck`), a path no loaded server
serves, and a base URL injected from config with no literal binding.

`verified` versus `probable` is a real distinction the fact carries: an edge matched
through a declared contract (a `.proto` service, an OpenAPI `operationId`) is an
identity; an edge matched by comparing URL strings is a resemblance. See
[gRPC and OpenAPI](extraction/grpc-openapi.md).

### On a real public cluster

Nextcloud server plus two first-party apps, indexed into one graph
(101,462 + 1,706 + 4,966 facts):

```
  service      classification  detected  resolved  unresolved
  collectives  coverage_gap          2         0           2
  contacts     isolated              0         0           0
  server       coverage_gap         13         0          13
```

Zero of thirteen resolved. The report doesn't disguise it as success: `server` is
classified **`coverage_gap`**, not `isolated`, and the output says so in words —

> 1 service is classified `coverage_gap`: no resolved outbound edges, but enola DID
> detect outbound call sites. Do not read these as isolated — they are the case where
> "depends on nothing" and "enola could not tell" look identical from the graph alone.

Nextcloud apps reach the server through in-process PHP APIs rather than cross-repo
HTTP, so there is little here for the linker to match. The point of including it is
that a report which only showed clusters where enola does well would not be evidence
of anything.

### On a .NET cluster

`bitwarden/server` (C#) plus `bitwarden/clients` (TypeScript) — two independent
repositories, different build systems, talking over HTTP. This is the shape the
other clusters lack: cluster 1 is in-process PHP, and the gRPC-web example is one
tree.

```
  service            classification  detected  resolved  unresolved
  bitwarden-clients  connected            22         2          20
  bitwarden-server   coverage_gap         13         7           6
```

Before this corpus supported .NET, `bitwarden-server` read **`isolated` with 0
unresolved** — no `role=client` routes were emitted at all, so the linker had one
side of every edge and the server appeared to call nothing. `coverage_gap` with 6
unresolved is the honest replacement: enola found calls it cannot place.

Seven of thirteen resolve. The twenty unresolved on the client side are mostly
endpoints served by repositories not in this cluster.

### On the example a reader can run

[`examples/cross-repo/`](../examples/cross-repo/) — one command, two Go services:

```
==> Routes the api service actually serves
    (registered as "/orders/{id}" — stored at the composed runtime path)
    "/api/v2/orders"
    "/api/v2/orders/{id}"

  service  classification  detected  resolved  unresolved
  api      isolated              0         0           0
  web      connected             3         2           1
```

`api/server.go` registers the bare `/orders/{id}` inside a function that receives a
subrouter mounted at `/api/v2` in `main`. Neither function contains the served path;
the fact does. The web service's call resolves *because* that prefix was composed
across the call boundary. The one unresolved call is a deliberately dynamic URL,
so the demonstration proves its own limit in the same run.

## 4. Scale

| | |
|---|---|
| Largest repository indexed | **Linux kernel** — 55,399 files, **1,892,342 facts**, 166.3s cold / 48.6s warm |
| Largest .NET | dotnet/runtime — 17,766 files, 397,608 facts, 65.9s / 37.2s |
| Largest Ruby | GitLab — 49,484 files, 437,219 facts, 35.4s / 27.3s |
| Largest Rust | rust-lang/rust — 36,082 files, 394,930 facts, 24.1s / 13.0s |
| Largest Go | Grafana — 10,313 files, 167,987 facts, 8.9s / 6.7s |
| Throughput | 7,500–35,000 facts/sec depending on language |
| Parse errors, all 53 repositories | **0** |
| Memory, Linux kernel | heap peaked well inside a laptop's budget; no repository required tuning |

Warm runs are 1.14×–3.77× faster than cold (over the 44 repositories whose cold run
exceeds 0.5s; below that the timing is noise), from the per-file content-hash cache
in `snapshot.meta.json`. These numbers establish that the graph the other four
sections rely on can actually be built on real code. enola isn't benchmarked on
speed as a competitive claim.

## 5. What the extractors see

Across the corpus enola extracted **18,552 routes** (16,909 server, 1,643 client) and
recognised **36 distinct frameworks** without configuration:

```
wordpress 6668 · rails 2880 · aspnetcore 1838 · fastapi 1214 · openapi 1039
flask 895 · chi 751 · axios 707 · spring 578 · symfony 552 · resttemplate 418
fetch 178 · request-options 170 · axum 132 · vue 106 · nextjs 78 · nuxt 55
blazor 48 · grpc 39 · http-client 36 · openapi-fetch 35 · httpclient 34
file-get-contents 21 · faraday 16 · sveltekit 13 · net/http 10 · graphql 10
net-http 8 · django 8 · retrofit 4 · gorilla/mux 2 · express 2 · guzzle 2
urlsession 2 · razorpages 2 · symfony-httpclient 1
```

Fact kinds: 3,769,650 symbols · 1,158,084 dependencies · 67,490 test refs ·
58,090 file refs · 52,658 modules · 18,552 routes · 4,331 storage · 20 services.

`aspnetcore` at 1,838 is the third-largest framework, and `storage` is non-zero for
.NET at all — both were **zero** before this corpus was indexed for it. `blazor 48`
and `razorpages 2` count `@page` routes only; the bulk of Razor's contribution is
reference edges rather than routes, which is what the format is mostly used for.

Two rows are worth reading carefully rather than as a score. **django 8** counts
routes, not coverage: Saleor is GraphQL-first, so its REST surface really is that
small, while its facts still carry the Django framework tag. And **axum 132** is
depressed by the same repository that supplied it: crates.io declares most of its
API through `utoipa`'s `routes!()` macro, which enola deliberately does not expand —
a documented limit in [rust.md](extraction/rust.md).

A third belongs to .NET. OrchardCore contributes 82 routes where it declares 288
verb attributes, because 94% of its controllers are conventionally routed and 20 of
its registrations use a `{area}/{controller}/{action}` template that cannot be
resolved without each controller's area. That gap is counted and logged rather than
closed by guessing — see [dotnet.md](extraction/dotnet.md).

## 6. Agent A/B — does the agent ship the regression?

The one A/B worth running is against **no tool**, on a task where the obvious
implementation is structurally wrong.

**Setup.** A small TypeScript service with three modules: `src/domain`, `src/store`
(imports domain), `src/api`. The task: *add `recentOrders(customerId)` to the domain
package and expose it from the API handler.* The natural implementation makes
`domain` import `store`, closing a module cycle. TypeScript compiles it without
complaint and every test still passes.

Three arms, three trials each, Claude Code headless (`claude -p`, Claude Code 2.1.205).
Each arm adds one thing to the one before it:

- **bare** — no MCP servers, no instruction, no hooks.
- **instruction** — enola's MCP server plus the exact instruction `enola install`
  writes into the project's agent rules, which tells the agent to pin a baseline
  before editing and diff afterwards.
- **loop** — the same, plus the session hooks `enola install --hooks` configures, so
  the change is graded when the agent stops whether or not it remembered to ask.

Ground truth is identical for all three and does not trust the agent: the tree each
run leaves behind is graded against a baseline pinned before it started.

| Arm | Shipped the regression | Median turns | Median cost | Median wall |
|---|---|---|---|---|
| bare | **3 / 3** | 12 | $0.31 | 53s |
| instruction | **1 / 3** | 14 | $0.66 | 125s |
| loop | **0 / 3** | 24 | $1.12 | 252s |

The bare agent shipped a dependency cycle every time. With the hooks installed, none
of the three did, and the transcripts say why. From one of them, unprompted:

> The regression was `src/domain ↔ src/store`: my first attempt made the domain
> `import { listOrders }` from the store, which closed a loop with the store's
> pre-existing `import type { Order }` from the domain. I removed the offending edge.

The loop worked as designed: the agent introduced the cycle, the Stop hook graded
the session and handed back the verdict, and the agent corrected itself before
reporting done. It took roughly double the turns and 3.6× the money of the bare
run: the cost of catching and fixing the regression, against the bare run's
$0.31, which bought a tree with a dependency cycle already in it.

**Read this before quoting the table.**

- **Nine runs is not a statistic.** 3/3 versus 0/3 is a real gap and the ordering is
  monotonic, but the difference between the instruction arm's 1/3 and the loop arm's
  0/3 is one run. Treat this as a demonstration that the mechanism works end to end,
  not as a measured effect size.
- **No arm ever called `diff_snapshot`.** Every enola-equipped run used exactly one
  tool, `generate_snapshot`. The loop arm did not succeed because the agent asked
  enola the right question. It succeeded because something asked on its behalf when
  it stopped. That is the whole argument for the hook over the instruction.
- **The cost column is not enola's price.** It compares an agent that checked its work
  against one that did not: the bare arm is cheap because it stopped early and shipped
  the cycle three times out of three. The arm that would isolate what enola costs does
  not exist here: an agent told to establish the same property *without* a graph,
  re-deriving the module structure from source on every task. Do not read $0.31 → $1.12
  as the overhead of adding enola to a run that was otherwise identical.
- **The instruction arm improved on bare anyway** (1/3 vs 3/3), which is worth being
  honest about: having a structural map in context appears to help even when the
  agent never queries the delta. With three trials, that could equally be noise.
- **One trap, one language, one model.** A deliberately subtle module-level cycle in
  TypeScript.

## What this does not measure

Stated so the numbers above are not read as more than they are.

- **Retrieval speed, token counts and cost per query.** Deliberately absent. It is
  the axis where every tool in this space competes, and enola has no evidence to offer
  on it. The A/B's cost column is not that evidence: it compares checking against not
  checking, not one retrieval path against another.
- **The session hooks in interactive use.** Section 6 exercises them headlessly, under
  `claude -p`. The configuration is the same either way, but interactive sessions were
  not measured.
- **Swift in the ratchet.** Measured in every other section; excluded from section 2
  only, because a Swift module is a declared SPM target and no additive cycle
  injection exists. Stated above rather than silently omitted.
- **Kotlin and Swift at *large* scale.** Both now have a real public repository
  (Google's reference Android app; a SwiftUI application), but at 312 and 382 parsed
  files they are the small end of the corpus. The framework constructs are exercised
  on production code; the scale claims are not.
- **Vue and Svelte route surfaces are small.** Nuxt and SvelteKit are measured on real
  applications, but 56 and 13 routes respectively, enough to show file-based routing
  works, not enough to characterise it.
- **Recall of cross-repo edges.** Section 3 reports what enola detected and what it
  resolved. It does not report edges enola never detected at all: that would need a
  hand-labelled ground truth for each cluster, which does not exist here.
- **Comparisons with other tools.** Not measured, not estimated, not implied.

## Reproducing

```bash
# one repository, three times — the reproducibility check in miniature
enola --generate /path/to/repo && cp .enola/receipt.json /tmp/r1.json
enola --generate /path/to/repo && cp .enola/receipt.json /tmp/r2.json
diff <(jq -r .snapshot_id /tmp/r1.json) <(jq -r .snapshot_id /tmp/r2.json) && echo reproducible

# the ratchet, on your own repository
enola baseline pin .          # freeze the architecture
#   …make a change…
enola check .                 # exit 1 on a structural regression

# cross-repo coverage, misses included
enola coverage cluster.yaml
```

> **Read the `enola: using config …` line each command prints, and check it is the
> config you meant.** A config's `extractors:` list *replaces* the built-in default
> rather than merging with it, so the wrong one does not fail — it quietly analyses
> something other than what you asked for. enola looks in the working directory first
> and falls back to the directory holding the binary.

---

Per-language extraction detail: **[docs/extraction/](extraction/README.md)** ·
What the words mean: **[docs/GLOSSARY.md](GLOSSARY.md)** ·
Commands and flags: **[docs/CLI.md](CLI.md)** ·
Why the graph is a snapshot: **[docs/SNAPSHOTS.md](SNAPSHOTS.md)** ·
What the findings are for: **[docs/EXPLAINERS.md](EXPLAINERS.md)** ·
How the engine works: **[ARCHITECTURE.md](../ARCHITECTURE.md)**
