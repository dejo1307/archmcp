# enola — architectural regression testing for AI-assisted development

[![MCP Toplist](https://mcptoplist.com/badge/glama%2Fenola-labs%2Fenola.svg)](https://mcptoplist.com/server/glama%2Fenola-labs%2Fenola)

**Your agent can't tell you it's finished while it has introduced a dependency cycle.**

An AI agent can write more code in an hour than you can carefully review. Your tests tell you the behaviour still works. Your linter tells you the style is fine. Nothing tells you the *structure* still makes sense — that the change didn't couple two modules that had no business knowing about each other, or quietly close a dependency loop. You find that out in review, if you're lucky, or six months later when nobody can refactor that package any more.

enola answers that question, at the moment it's cheap to fix.

---

## The loop

Two halves, and the second one is the point:

**Before a change**, your agent gets the real structure — a deterministic graph of your code's modules, symbols, routes, storage and how they depend on each other, extracted from source rather than inferred. It can ask what actually depends on the thing it's about to touch, instead of guessing from a grep.

**After a change**, enola grades what the change *did*. It pins the architecture beforehand, compares afterwards, and reports the delta: findings introduced or resolved, coupling added, symbols added and removed. It's a **delta, not a linter** — it stays completely silent about everything that was already there, so when it does speak, something actually moved.

That runs in three places, and you can use any of them on their own:

| | |
|---|---|
| **In your agent** | a hook grades each session and hands the verdict back, so the agent fixes its own regression before telling you it's done |
| **In your shell** | `enola check` — exits `1` on a structural regression |
| **In CI** | the same command, same exit code, on every pull request |

## What that looks like

A change that made `billing` and `invoice` import each other — verbatim output, nothing trimmed:

```
FAIL — 1 structural regression introduced.

Regressions (fail):
  - [cycles] 1.00 — Cyclic dependency detected (2 modules)
      module "billing" is part of the cycle

Policy: fail on new findings from [cycles] at confidence >= 1.00.

What changed
  symbols      +1
  dependencies +2
  edges        +5  (imports +2, calls +2, declares +1)

Added (3):
  symbol     invoice.Retry                                invoice/invoice.go:7
  dependency billing -> acme/shop/invoice                 billing/billing.go:3
  dependency invoice -> acme/shop/billing                 invoice/invoice.go:3

New coupling (5):
  billing                                      --imports--> invoice
  invoice                                      --imports--> billing
  billing.Charge                               --calls--> invoice.Render
  invoice.Retry                                --calls--> billing.Charge
  invoice.Retry                                --declares--> invoice
```

Two packages here, for a readable example. On a 68,000-fact repository carrying 335 pre-existing findings it behaves identically: it reported the one thing the change introduced, and none of the 335.

## Set it up

Three commands.

```bash
# 1. install the binary
curl -fsSL https://raw.githubusercontent.com/enola-labs/enola/main/install.sh | sh

# 2. tell your agent it exists, and close the loop automatically
enola install --hooks

# 3. give your agent the graph over MCP
claude mcp add enola enola
```

`enola install` writes a short instruction into the files your agents already read — Claude Code, Cursor, Copilot, Codex, Pi — and `--hooks` adds the two hooks that run the loop for you. It previews every change and asks before writing, never creates files you didn't have, and `enola uninstall` puts everything back byte-for-byte.

Prefer to drive it yourself? Skip step 2 and run the loop by hand:

```bash
enola baseline pin      # freeze the architecture before you edit
#   …make your change…
enola check             # grade it — exit 1 on a structural regression
```

Full setup, every flag and all the exit codes: **[docs/CLI.md](docs/CLI.md)**.

## What this catches that your existing tools don't

You already own four things that look like they should catch a structural regression. None of them do:

| | Tells you |
|---|---|
| **Git diff** | which lines changed |
| **Tests** | whether the behaviour you tested still works |
| **Linter** | whether local rules were violated, file by file |
| **Code review** | whatever a human notices, after the work is finished |
| **`enola check`** | **what the change did to the structure of the system** |

A dependency cycle is invisible to all four. It isn't a line, it isn't a behaviour, it isn't local to a file, and by the time it reaches review it's already written.

## Only one thing fails the build

A newly introduced **dependency cycle** fails. Everything else is reported and lets you through.

That's deliberate. A cycle is the one finding enola computes with certainty rather than infers — Tarjan's strongly-connected-components over the real import graph, confidence `1.0` — and it's a violation too consequential to wave past: it dictates load order, it makes both modules untestable in isolation, and it raises the price of every future refactor in that area. It is not a matter of taste, and it's exactly the kind of thing an agent introduces without noticing.

Everything else — god classes, hotspots, deep dependency chains, layer violations, complexity outliers — is a **heuristic**, computed by statistical outlier tests. Those get reported so you can look, and never break your build. Each carries a confidence score so you can tell the two apart at a glance.

A gate that fails on exactly one thing, and tells you which, is a gate people leave switched on. Widen it if you want to:

```bash
enola check --fail-on=cycles,layers --min-confidence=0.8
enola check --warn-only          # report everything, fail nothing
```

## How it works

Not a language model, and not embeddings. enola parses your source with tree-sitter and language-specific extractors, normalizes it into a typed fact model, links it into a directed graph, and runs real graph algorithms over it — Tarjan's SCC for cycles, cycle-safe longest-path for dependency depth, mean+2σ outlier tests for the statistical findings.

That means the same commit yields the same answer, every time — measured, not asserted: across 30 open-source repositories indexed three times each, all 30 produced a byte-identical snapshot ID and a byte-identical fact file, over 3.9 million facts with zero parse errors ([BENCHMARKS.md](docs/BENCHMARKS.md)). Every snapshot carries a **receipt**: enola's version, the git ref and whether the tree was dirty, the extractors used, and a snapshot ID that's a `sha256` fingerprint of the facts rather than a random UUID. Before trusting a comparison, enola checks the two snapshots were even built the same way — a different extractor set or changed ignore rules makes a diff meaningless, and it says so instead of reporting churn as if it were your change.

Nothing leaves your machine. It's a local binary reading local files.

**[ARCHITECTURE.md](ARCHITECTURE.md)** has the fact model, the pipeline, the MCP tool reference and the analysis internals.

## Beyond one repository

Point enola at a second repo and it links them into one graph — a web client's `fetch()` to the backend route that serves it, an iOS endpoint enum or Android Retrofit interface to that same route, a gRPC call site to the `.proto` service behind it, one service's Kafka producer to another's consumer.

The hard part isn't finding the call, it's making both sides match. A route registered as `HandleFunc("/courses", …)` inside a function that receives a `PathPrefix("/api")` subrouter doesn't live at `/courses` — it lives at `/api/courses`, and unless that prefix is composed *interprocedurally*, across function and package boundaries, the client call never resolves. The same goes for Axum's `.nest()`, Rails' `scope`/`namespace`, and a Swift endpoint enum whose version prefix is defined in a protocol extension three files away.

So an agent can answer *if I change this endpoint, which mobile screens break?* by traversal instead of inference — and `enola check` grades a change that spans repos the same way it grades one that doesn't.

Whether that actually worked on *your* code is not something you should have to take on trust:

```bash
enola coverage cluster.yaml
```

reports, per service, how many outbound call sites enola detected, how many it resolved to a loaded repository, and **how many it couldn't** — so a genuinely isolated service is distinguishable from one whose edges enola simply failed to follow. The misses are always shown, because a number you can't check is worth less than one you can.

[`examples/cross-repo/`](examples/cross-repo/) is a two-service demo you can run in one command: a prefix composed across a function boundary so the client's call resolves, and one deliberately dynamic call that stays unresolved — because reporting a gap beats inventing an edge.

---

## Supported languages

| Language   | Detected by |
|------------|-------------|
| Go         | `go.mod` (gorilla/mux + chi route composition / gRPC clients / Kafka topics aware) |
| Java       | `pom.xml` (Maven) or `.java` sources (Spring routes / JPA / Lombok DI / Dubbo SPI aware) |
| JavaScript | `tsconfig.json` / `package.json` with TypeScript (parsed by the TypeScript extractor) |
| TypeScript | `tsconfig.json` / `package.json` with TypeScript (Next.js & monorepo aware) |
| Vue        | `package.json` with `vue` dependency (Nuxt / Vue Router / Composition API aware) |
| Svelte     | `package.json` with `svelte` dependency (SvelteKit routing / `$lib` alias aware) |
| Python     | `pyproject.toml`, `requirements.txt`, `setup.py`, … (FastAPI / Django / SQLAlchemy aware) |
| Kotlin     | `build.gradle(.kts)` with Kotlin/Android (Compose / Hilt / Room aware) |
| Swift      | `Package.swift`, `.xcodeproj`, `.xcworkspace` (SwiftUI / UIKit aware) |
| Ruby       | `Gemfile` (Rails / ActiveRecord / Packwerk aware) |
| Rust       | `Cargo.toml` (workspace or single crate; crate/module/`impl`/trait aware; Axum route DSL aware) |
| C / C++    | `.c`/`.h` (tree-sitter-c) or `.cpp`/`.hpp`/… (tree-sitter-cpp), or `CMakeLists.txt`/`Makefile` + header (per-fact `language`, header/source method merging, namespaces, templates) |
| PHP        | `composer.json`, WordPress markers, or any `.php` source (WordPress / Laravel / Symfony route + outbound HTTP-client aware) |
| OpenAPI    | any spec with an `openapi:` / `swagger:` key |
| gRPC       | any `.proto` file (proto services → routes; TypeScript gRPC-web client calls detected) |

Framework- and platform-specific detection for each language is described in **[ARCHITECTURE.md → Supported languages](ARCHITECTURE.md#supported-languages)**.

> Python, Ruby, PHP, and Rust are parsed with tree-sitter and contribute call and dependency edges to the graph, so `traverse`, `find_path`, and `impact_analysis` reach into them - not just modules and routes.


## Learn more

- **[docs/CLI.md](docs/CLI.md)** - setup, every command and flag, the exit codes, and the `--explain` report.
- **[docs/BENCHMARKS.md](docs/BENCHMARKS.md)** - reproducibility, delta precision, cross-repo coverage and scale, measured on 30 public repositories - including the three defects the run found in enola itself.
- **[docs/extraction/](docs/extraction/)** - per language, what specific code produces which facts, from committed fixtures - and what each extractor deliberately does not resolve.
- **[ARCHITECTURE.md](ARCHITECTURE.md)** - the concept, the fact model, the pipeline, the MCP tool reference, and the value model.
- **[examples/](examples/)** - ready-made per-language and multi-repo configs, plus a pre-commit hook and a CI workflow.

## License

Apache License 2.0 - see [`LICENSE`](LICENSE).

**What that gets you: all of the above.** This repository is the whole engine, not a trial edition. Every extractor and every language ships here (Go, TypeScript/JavaScript/Vue/Svelte, Python, Java, Kotlin, Ruby, PHP, Swift, Rust, C/C++, gRPC/Protobuf, OpenAPI), along with the cross-repo linker, all 13 MCP tools, all 10 explainers (cycles, layers, cross-repo, coverage, unused-routes, god-class, hotspots, dependency-depth, exported-surface, complexity-outliers), baselines and `diff_snapshot`, snapshot receipts, the `--explain` report and the localhost dashboard. Nothing above is gated, metered, or degraded without a key - there is no license check anywhere in this repository, and no snapshot, fact, or usage counter leaves your machine. (The only outbound request enola makes is to GitHub's release API, and only when you explicitly run `enola upgrade`.)

## Acknowledgements

enola bundles third-party components under their own licenses; see [`NOTICE`](NOTICE). Swift parsing uses the [tree-sitter-swift](https://github.com/alex-pinkus/tree-sitter-swift) grammar by Alex Pinkus (MIT), vendored under [`internal/extractors/swiftextractor/grammar/`](internal/extractors/swiftextractor/grammar/).
