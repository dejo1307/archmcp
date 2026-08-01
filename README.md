# enola — architectural regression testing for AI-assisted development

[![MCP Toplist](https://mcptoplist.com/badge/glama%2Fenola-labs%2Fenola.svg)](https://mcptoplist.com/server/glama%2Fenola-labs%2Fenola)

**enola checks that every change your agent makes leaves your architecture intact.**

AI agents can write more code than you can carefully review. Tests check that behaviour still works. Linters check that style rules are followed. Neither checks whether the *structure* of the code still makes sense — whether the change coupled two modules that had no business knowing about each other, or closed a dependency loop. That usually surfaces in review, if someone catches it, or months later when the package is too tangled to refactor.

enola checks structure while the change is still easy to fix.

---

## The loop

enola works in two parts.

**Before a change**, your agent has the real structure of the codebase: a deterministic graph of modules, symbols, routes, and storage, and how they depend on each other, extracted from source rather than inferred. It can look up what actually depends on the thing it's about to touch, instead of guessing from a grep.

**After a change**, enola grades what happened. It pins the architecture beforehand, compares it afterwards, and reports the delta: findings introduced or resolved, coupling added, symbols added and removed. This goes beyond what a static linter checks — it shows you everything the change actually did, and stays silent about everything that was already there.

It runs in three places, each usable on its own:

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

This example uses two packages for readability. On a 68,000-fact repository carrying 335 pre-existing findings, enola behaves the same way: it reports the one thing the change introduced, not the other 335.

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

`enola install` adds a short instruction to the files your agents already read — Claude Code, Cursor, Copilot, Codex, Pi — and `--hooks` adds the two hooks that run the loop automatically. It previews every change and asks before writing, never creates files you didn't already have, and `enola uninstall` reverses everything byte-for-byte.

After your next session, `enola doctor` reports whether those hooks actually fired. Worth running once: a hook configuration is a contract with your agent, and one it quietly ignores looks exactly like one it honours.

To run the loop by hand instead, skip step 2:

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

Only a newly introduced **dependency cycle** fails the build. Everything else is reported but lets you through.

A cycle is computed with certainty, not inferred: Tarjan's strongly-connected-components algorithm over the real import graph, confidence `1.0`. It also has real consequences — it dictates load order, makes both modules untestable in isolation, and makes that area more expensive to refactor. It's exactly the kind of thing an agent introduces without noticing.

Everything else — god classes, hotspots, deep dependency chains, layer violations, complexity outliers — is a **heuristic**, computed with statistical outlier tests. enola reports these automatically; they never break the build. Each finding carries a confidence score so you can tell the two apart.

If you want to configure the confidence threshold or which findings fail the build, you can:

```bash
enola check --fail-on=cycles,layers --min-confidence=0.8
enola check --warn-only          # report everything, fail nothing
```

## How it works

enola parses your source with tree-sitter and language-specific extractors, normalizes it into a typed fact model, links it into a directed graph, and runs graph algorithms over it: Tarjan's SCC for cycles, cycle-safe longest-path for dependency depth, mean+2σ outlier tests for the statistical findings. No language model, no embeddings.

The same commit yields the same answer, every time: across 38 open-source repositories indexed three times each, all 38 produced a byte-identical snapshot ID and a byte-identical fact file, over 4.2 million facts with zero parse errors ([BENCHMARKS.md](docs/BENCHMARKS.md)). Every snapshot carries a **receipt**: enola's version, the git ref and whether the tree was dirty, the extractors used, and a snapshot ID that's a `sha256` fingerprint of the facts rather than a random UUID. Before comparing two snapshots, enola checks they were built the same way — a different extractor set or changed ignore rules makes a diff meaningless, and it reports that instead of treating the mismatch as your change.

enola runs as a local binary reading local files. Nothing leaves your machine.

**[ARCHITECTURE.md](ARCHITECTURE.md)** has the fact model, the pipeline, the MCP tool reference and the analysis internals.

## Beyond one repository

Point enola at a second repo and it links them into one graph — a web client's `fetch()` to the backend route that serves it, an iOS endpoint enum or Android Retrofit interface to that same route, a gRPC call site to the `.proto` service behind it, one service's Kafka producer to another's consumer.

The hard part isn't finding the call, it's making both sides match. A route registered as `HandleFunc("/courses", …)` inside a function that receives a `PathPrefix("/api")` subrouter doesn't live at `/courses` — it lives at `/api/courses`, and unless that prefix is composed *interprocedurally*, across function and package boundaries, the client call never resolves. The same goes for Axum's `.nest()`, Rails' `scope`/`namespace`, and a Swift endpoint enum whose version prefix is defined in a protocol extension three files away.

So an agent can answer *if I change this endpoint, which mobile screens break?* by traversal instead of inference — and `enola check` grades a change that spans repos the same way it grades one that doesn't.

enola shows this working on your own code, rather than asking you to trust it:

```bash
enola coverage cluster.yaml
```

reports, per service, how many outbound calls enola found, how many it resolved, and **how many it couldn't**. This distinguishes a genuinely isolated service from one whose edges enola simply failed to resolve — misses are always shown, not hidden.

[`examples/cross-repo/`](examples/cross-repo/) is a two-service demo you can run in one command: a prefix composed across a function boundary so the client's call resolves, and one deliberately dynamic call that stays unresolved, to show what an unresolved edge looks like rather than hide it.

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

> Python, Ruby, PHP, and Rust are parsed with tree-sitter and contribute call and dependency edges to the graph, so `traverse`, `find_path`, and `impact_analysis` reach into them — not just modules and routes.


## Learn more

- **[docs/CLI.md](docs/CLI.md)** — setup, every command and flag, the exit codes, and the `--explain` report.
- **[docs/BENCHMARKS.md](docs/BENCHMARKS.md)** — reproducibility, delta precision, cross-repo coverage and scale, measured on 38 public repositories.
- **[docs/SNAPSHOTS.md](docs/SNAPSHOTS.md)** — why enola computes a graph on demand and keeps it as an addressable snapshot, rather than maintaining one continuously-updated graph, and where the opposite choice is the right one.
- **[docs/extraction/](docs/extraction/)** — per language, what specific code produces which facts, from committed fixtures, and what each extractor deliberately does not resolve.
- **[ARCHITECTURE.md](ARCHITECTURE.md)** — the concept, the fact model, the pipeline, the MCP tool reference, and the value model.
- **[examples/](examples/)** — ready-made per-language and multi-repo configs, plus a pre-commit hook and a CI workflow.

## License

Apache License 2.0 — see [`LICENSE`](LICENSE).

This repository is the full engine, not a trial edition. Every extractor and every language ships here (Go, TypeScript/JavaScript/Vue/Svelte, Python, Java, Kotlin, Ruby, PHP, Swift, Rust, C/C++, gRPC/Protobuf, OpenAPI), along with the cross-repo linker, all 13 MCP tools, all 10 explainers (cycles, layers, cross-repo, coverage, unused-routes, god-class, hotspots, dependency-depth, exported-surface, complexity-outliers), baselines and `diff_snapshot`, snapshot receipts, the `--explain` report, and the localhost dashboard. None of this is gated, metered, or degraded without a key — there is no license check anywhere in this repository, and no snapshot, fact, or usage counter leaves your machine. (The only outbound request enola makes is to GitHub's release API, and only when you explicitly run `enola upgrade`.)

## Acknowledgements

enola bundles third-party components under their own licenses; see [`NOTICE`](NOTICE). Swift parsing uses the [tree-sitter-swift](https://github.com/alex-pinkus/tree-sitter-swift) grammar by Alex Pinkus (MIT), vendored under [`internal/extractors/swiftextractor/grammar/`](internal/extractors/swiftextractor/grammar/).
