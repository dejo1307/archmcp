# Benchmarks

Everything here was measured on 2026-07-31, on 38 public open-source repositories,
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
| **4** | Does it finish on a real repository? | A graph you cannot build is not a graph |
| **5** | Does an agent, given the tool, actually avoid shipping the regression? | The honest test of the whole idea |

Axes 1–3 have no counterpart in a retrieval benchmark, because you cannot report a
delta without a baseline, and you cannot report a miss without knowing what a hit
would have been.

## The corpus

38 repositories, 239,349 source files parsed, 4,211,113 facts carrying 13 distinct
language tags (C, C++, Go, Java, Kotlin, PHP, Python, Ruby, Rust, Swift, TypeScript,
gRPC, OpenAPI). Public open-source only — every row is a repository you can clone and
re-run.

| Repository | Language | Files parsed | Facts | Modules | Routes | Cold | Warm |
|---|---|---|---|---|---|---|---|
| linux | C | 55,399 | 1,892,343 | 4,293 | 0 | 157.6s | 33.9s |
| gitlab | Ruby | 47,711 | 435,020 | 10,484 | 1,848 | 29.1s | 19.4s |
| rust | Rust | 36,090 | 394,970 | 3,759 | 0 | 20.4s | 9.1s |
| shopware | PHP | 13,731 | 218,814 | 4,188 | 724 | 11.1s | 4.9s |
| grafana | Go | 10,310 | 163,620 | 2,295 | 1,714 | 7.5s | 5.0s |
| thingsboard | Java | 6,371 | 158,987 | 1,164 | 1,403 | 4.8s | 3.1s |
| nextcloud-server | PHP | 6,037 | 101,462 | 1,538 | 12 | 5.9s | 2.0s |
| discourse | Ruby | 10,353 | 93,295 | 1,483 | 1,217 | 6.9s | 4.1s |
| dubbo | Java | 4,351 | 82,550 | 954 | 19 | 4.7s | 1.9s |
| supabase | TypeScript | 6,918 | 70,203 | 1,679 | 83 | 5.6s | 2.4s |
| airflow | Python | 4,061 | 67,992 | 1,261 | 463 | 8.7s | 5.3s |
| deno | Rust | 4,353 | 62,118 | 2,056 | 4 | 7.8s | 2.0s |
| superset | Python | 3,834 | 51,645 | 984 | 302 | 4.8s | 2.7s |
| cal.com | TypeScript | 4,596 | 48,637 | 1,453 | 270 | 3.0s | 1.5s |
| dbt-core | Rust | 1,371 | 46,935 | 321 | 202 | 2.9s | 1.0s |
| wordpress | PHP | 2,840 | 45,681 | 441 | 6,671 | 5.5s | 1.2s |
| gmsh | C++ | 1,679 | 41,348 | 86 | 0 | 2.9s | 0.8s |
| chatwoot | Ruby | 3,643 | 36,046 | 863 | 744 | 2.1s | 1.3s |
| gitea | Go | 2,210 | 34,088 | 395 | 758 | 1.5s | 1.1s |
| saleor | Python | 2,473 | 30,052 | 202 | 4 | 3.1s | 2.1s |
| flarum | PHP | 2,702 | 28,300 | 764 | 11 | 1.3s | 0.7s |
| cognee | Python | 1,489 | 16,909 | 394 | 214 | 1.7s | 0.8s |
| cognee-rs | Rust | 846 | 15,827 | 124 | 77 | 1.1s | 0.4s |
| tokio | Rust | 780 | 14,450 | 99 | 0 | 0.8s | 0.3s |
| crates-io | Rust | 878 | 12,451 | 214 | 96 | 0.8s | 0.4s |
| solidus | Ruby | 1,766 | 10,009 | 383 | 171 | 0.9s | 0.7s |
| excalidraw | TypeScript | 526 | 8,738 | 85 | 3 | 0.9s | 0.4s |
| isowords | Swift | 382 | 5,615 | 160 | 2 | 0.4s | 0.2s |
| nowinandroid | Kotlin | 312 | 5,110 | 123 | 4 | 0.4s | 0.3s |
| nextcloud-collectives | PHP | 383 | 4,966 | 70 | 1 | 0.5s | 0.2s |
| getdp | C++ | 169 | 4,562 | 9 | 0 | 0.8s | 0.2s |
| enola | Go | 158 | 3,506 | 56 | 6 | 0.8s | 0.3s |
| elk | Vue | 380 | 2,556 | 79 | 56 | 0.2s | 0.1s |
| nextcloud-contacts | PHP | 170 | 1,706 | 51 | 1 | 0.3s | 0.2s |
| grpc-web-example | Go + gRPC + TS | 12 | 293 | 8 | 5 | 0.1s | 0.1s |
| sveltekit-realworld | Svelte | 42 | 195 | 14 | 13 | 0.1s | 0.1s |
| cachet | PHP | 21 | 101 | 11 | 0 | 0.1s | 0.1s |
| orocrm | PHP | 2 | 13 | 3 | 0 | 0.1s | 0.1s |

**Parse errors across all 38 repositories: 0.**

Cold is a first run with no extractor cache; warm is the best of two subsequent runs
on an unchanged tree. Measured on an Apple-silicon laptop; treat the ratios as the
signal, not the absolute seconds.

## 1. Reproducibility

Each repository was indexed **three times** — once cold, twice warm — and the
receipt's `snapshot_id` and the SHA-256 of `facts.jsonl` were compared across all
three. Running cold then warm is the point: it tests that a cached run and a
from-scratch run agree, not merely that the same code path repeats itself.

> **38 of 38 repositories produced a byte-identical `snapshot_id` and a
> byte-identical `facts.jsonl` across all three runs — 114 runs, 4,211,113 facts,
> zero drift.** `insights.json` is byte-stable on all 38 as well.

This is the property everything else rests on. `snapshot_id` is
`sha256(facts ‖ enola version ‖ config hash)`, not a UUID, so two runs on the same
commit are provably the same graph — which is what lets `compare_receipts` refuse to
diff snapshots that are not comparable instead of reporting churn as your change.

An ID a third party can re-derive is the point rather than a hygiene property: it is
what makes a graph a value you can keep and compare, instead of a state you are
currently in — [SNAPSHOTS.md](SNAPSHOTS.md).

## 2. Delta precision — the ratchet

The claim is that enola reports **what your change did** and stays silent about
everything that was already wrong. That is testable directly: pin a baseline, put
the repository through four states, and see what each one is graded as.

For each repository below, files were **added, never edited**, so every revert is a
delete. The cycle case adds two new modules that import each other.

| Repository | Language | Pre-existing findings | No change | Benign addition | Injected cycle | Reverted |
|---|---|---|---|---|---|---|
| superset | Python | 347 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| gitea | Go | 159 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| enola | Go | 144 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| cognee | Python | 123 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| excalidraw | TypeScript | 111 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| chatwoot | Ruby | 104 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| solidus | Ruby | 72 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| crates-io | Rust | 65 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| nowinandroid | Kotlin | 40 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| elk | TypeScript | 26 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| sveltekit-realworld | TypeScript | 1 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| cachet | PHP | 0 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |

Read the columns as four separate claims, all of which hold on all twelve:

- **No change → +0 facts, +0 edges, PASS.** A genuine zero. Not "small", not
  "noise below a threshold" — nothing at all. This is what makes the other three
  columns mean something.
- **Benign addition → PASS**, with the delta naming exactly the 2–3 facts added.
  A new leaf module is not a regression, and reporting it as one is how gates die.
- **Injected cycle → FAIL, exactly 1 regression** — out of **1,192 pre-existing
  findings across these repositories**, up to 347 in a single one. None of them was
  repeated. The ratchet holds.
- **Reverted → PASS again**, +0 facts. The verdict is a function of the tree, not
  of history.

Eight languages, one behaviour. A regression is not detected by pattern-matching a
language; it is a cycle in the module graph, computed by Tarjan's SCC over the
resolved import edges, at confidence `1.0`.

**Swift is deliberately absent**, and the reason is a property of the language rather
than a limit of enola. A Swift module is a *declared* SPM target, so two added
directories that import each other form no edge and no cycle — verified: the modules
and the symbols appear, the cycle does not. Injecting one would mean editing
`Package.swift`, and then "reverted" would be a file restore rather than a delete,
which is exactly the property that makes the no-change column worth reading.

### Why only cycles fail

Across the corpus enola produced **24,648 findings**. Broken down:

| Explainer | Findings | Class |
|---|---|---|
| hotspots | 20,891 | statistical outlier |
| layers | 953 | heuristic |
| **cycles** | **887** | **structural fact — the only one that fails a build** |
| god-class | 714 | statistical outlier |
| complexity-outliers | 491 | statistical outlier |
| exported-surface | 442 | candidate |
| dependency-depth | 270 | statistical outlier |

3.6% of findings are eligible to fail a build. The other 96.4% are reported and let
you through. That ratio is the design, not an accident: a gate that fails on one
thing, and says which, is a gate people leave switched on.

## 3. Cross-repo resolution, misses included

The number that matters is not how many edges resolved. It is whether the ones that
did **not** resolve are shown to you — because a service with no dependents and a
service whose dependents enola failed to follow look identical in a graph.

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
  collectives  isolated              0         0           0
  contacts     isolated              0         0           0
  server       coverage_gap         11         0          11
```

Zero of eleven resolved. That is the honest number, and the report is built so it
cannot be mistaken for success: `server` is classified **`coverage_gap`**, not
`isolated`, and the output says so in words —

> 1 service is classified `coverage_gap`: no resolved outbound edges, but enola DID
> detect outbound call sites. Do not read these as isolated — they are the case where
> "depends on nothing" and "enola could not tell" look identical from the graph alone.

Nextcloud apps reach the server through in-process PHP APIs rather than cross-repo
HTTP, so there is little here for the linker to match. The point of including it is
that a report which only showed clusters where enola does well would not be evidence
of anything.

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
across the call boundary — and the one unresolved call is a deliberately dynamic URL,
so the demonstration proves its own limit in the same run.

## 4. Scale

| | |
|---|---|
| Largest repository indexed | **Linux kernel** — 55,399 files, **1,892,343 facts**, 157.6s cold / 33.9s warm |
| Largest Ruby | GitLab — 47,711 files, 435,020 facts, 29.1s / 19.4s |
| Largest Rust | rust-lang/rust — 36,090 files, 394,970 facts, 20.4s / 9.1s |
| Largest Go | Grafana — 10,310 files, 163,620 facts, 7.5s / 5.0s |
| Throughput | 7,500–35,000 facts/sec depending on language |
| Parse errors, all 38 repositories | **0** |
| Memory, Linux kernel | heap peaked well inside a laptop's budget; no repository required tuning |

Warm runs are 1.33×–5.22× faster than cold (over the 21 repositories whose cold run
exceeds 0.5s; below that the timing is noise), from the per-file content-hash cache
in `snapshot.meta.json`. Speed is not a claim enola competes on; these numbers are here
to establish that the graph the other four sections rely on can actually be built on
real code.

## 5. What the extractors see

Across the corpus enola extracted **17,098 routes** (14,620 server, 2,478 client) and
recognised **32 distinct frameworks** without configuration:

```
wordpress 6668 · rails 3612 · openapi 1124 · axios 871 · request-options 761 · chi 751
spring 578 · symfony 574 · resttemplate 418 · fastapi 354 · flask 297 · fetch 216
nestjs 175 · axum 132 · grpc 107 · vue 103 · net/http 63 · nuxt 55 · openapi-fetch 46
guzzle 44 · nextjs 39 · http-client 36 · faraday 16 · sveltekit 13 · net-http 12
file-get-contents 9 · hono 7 · gorilla/mux 5 · retrofit 4 · django 4 · express 2
urlsession 2
```

Fact kinds: 3,065,486 symbols · 979,468 dependencies · 61,328 file refs ·
42,546 modules · 41,753 test refs · 17,098 routes · 3,434 storage.

Two rows are worth reading carefully rather than as a score. **django 4** counts
routes, not coverage: Saleor is GraphQL-first, so its REST surface really is that
small, while 123 of its facts carry the Django framework tag (models, mostly). And
**axum 132** is depressed by the same repository that supplied it: crates.io declares
most of its API through `utoipa`'s `routes!()` macro, which enola deliberately does
not expand — the documented limit in [rust.md](extraction/rust.md), observed on
production code for the first time here.

**What specific code produces which of these facts is documented per language, from
committed fixtures, in [docs/extraction/](extraction/README.md)** — including what
each extractor deliberately does *not* resolve.

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
of the three did — and the transcripts say why. From one of them, unprompted:

> The regression was `src/domain ↔ src/store`: my first attempt made the domain
> `import { listOrders }` from the store, which closed a loop with the store's
> pre-existing `import type { Order }` from the domain. I removed the offending edge.

That is the loop working as designed: the agent introduced the cycle, the Stop hook
graded the session and handed back the verdict, and the agent corrected itself before
reporting done. It took roughly double the turns and 3.6× the money of the bare run —
which is what checking and then fixing costs against not checking at all, since the
bare run's $0.31 bought a tree with a dependency cycle in it.

**Read this before quoting the table.**

- **Nine runs is not a statistic.** 3/3 versus 0/3 is a real gap and the ordering is
  monotonic, but the difference between the instruction arm's 1/3 and the loop arm's
  0/3 is one run. Treat this as a demonstration that the mechanism works end to end,
  not as a measured effect size.
- **No arm ever called `diff_snapshot`.** Every enola-equipped run used exactly one
  tool, `generate_snapshot`. The loop arm did not succeed because the agent asked
  enola the right question — it succeeded because something asked on its behalf when
  it stopped. That is the whole argument for the hook over the instruction.
- **The cost column is not enola's price.** It compares an agent that checked its work
  against one that did not: the bare arm is cheap because it stopped early and shipped
  the cycle three times out of three. The arm that would isolate what enola costs does
  not exist here — an agent told to establish the same property *without* a graph,
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
  on it. The A/B's cost column is not that evidence — it compares checking against not
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
  applications, but 56 and 13 routes respectively — enough to show file-based routing
  works, not enough to characterise it.
- **Recall of cross-repo edges.** Section 3 reports what enola detected and what it
  resolved. It does not report edges enola never detected at all — that would need a
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
Commands and flags: **[docs/CLI.md](CLI.md)** ·
Why the graph is a snapshot: **[docs/SNAPSHOTS.md](SNAPSHOTS.md)** ·
How the engine works: **[ARCHITECTURE.md](../ARCHITECTURE.md)**
