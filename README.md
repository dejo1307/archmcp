# enola — architectural regression testing for AI-assisted development

[![MCP Toplist](https://mcptoplist.com/badge/glama%2Fenola-labs%2Fenola.svg)](https://mcptoplist.com/server/glama%2Fenola-labs%2Fenola)
[![CI](https://github.com/enola-labs/enola/actions/workflows/ci.yml/badge.svg)](https://github.com/enola-labs/enola/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/enola-labs/enola)](https://github.com/enola-labs/enola/releases)
[![License](https://img.shields.io/github/license/enola-labs/enola)](LICENSE)

**The quality gate every agentic loop is missing.**

enola gives your agent the real architecture before it writes a line, then grades what it built and returns that verdict, so it fixes its own regression before calling the job done. Every finding comes from a real parser and a graph algorithm, never a model's guess.

AI agents can write more code than you can carefully review. Tests check that behaviour still works. Linters check that style rules are followed. Neither checks whether the *structure* of the code still makes sense — whether the change coupled two modules that had no business knowing about each other, or closed a dependency loop (a circular dependency). That usually surfaces in review, if someone catches it, or months later when the package is too tangled to refactor.

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

This example uses two packages for readability. On a 68,000-fact repository carrying 268 pre-existing findings, enola behaves the same way: it reports the one thing the change introduced, not the other 268.

The whole loop, on this repository, unedited — a helper is added, the check fails on the cycle it closed, the diff shows what has to change, and the same command lets it through once it's fixed:

![enola check on its own repository: a helper added to pkg/facts closes a dependency cycle, enola fails the change, the diff replaces the import with an injected interface, and the re-run passes](docs/images/story2-gate.gif)

<sub>121 findings already in this repository. The check names the one the change added — and `&& echo` never fires while the gate is red.</sub>

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

`enola install` adds a short instruction to the files your agents already read — Claude Code, Cursor, Copilot, Codex, Pi — and `--hooks` adds the two hooks that run the loop automatically. It previews every change and asks before writing, never creates a shared file like `AGENTS.md` that wasn't already there, and `enola uninstall` reverses everything byte-for-byte — including the files and directories it created itself.

After your next session, `enola doctor` reports whether those hooks actually fired. Worth running once: a hook configuration is a contract with your agent, and one it quietly ignores looks exactly like one it honours.

To run the loop by hand instead, skip step 2:

```bash
enola baseline pin      # freeze the architecture before you edit
#   …make your change…
enola check             # grade it — exit 1 on a structural regression
```

Full setup, every flag and all the exit codes: **[docs/CLI.md](docs/CLI.md)**.

### Staying current

enola releases often. It checks for a new one at most once every 12 hours, in the background, and caches the answer in `~/.enola/update.json` — no command ever waits on the network, and a machine that is offline behaves exactly like one that is up to date. When there is a newer release, `enola check`, `enola --generate` and `enola doctor` say so in one line, and `enola upgrade` installs it.

The notice reports one thing beyond the version: whether the **extractors** changed. That is the bit worth acting on — it means snapshots taken with your build are missing facts a current enola would extract, which is a data problem rather than a housekeeping one.

Your agent gets the same notice once per session over MCP, worded so it tells you rather than upgrading your machine mid-task.

It is silent for builds from source, never runs when `CI` is set, and turns off entirely with `export ENOLA_NO_UPDATE_CHECK=1`.

## What this catches that your existing tools don't

You already own four things that look like they should catch a structural regression. None of them do:

| | Tells you |
|---|---|
| **Git diff** | which lines changed |
| **Tests** | whether the behaviour you tested still works |
| **Linter** | whether local rules were violated, file by file |
| **Code review** | whatever a human notices, after the work is finished |
| **`enola check`** | **what the change did to the structure of the system** |

A dependency cycle spans files, doesn't break any test, and is easy for a reviewer to miss.

## Only one thing fails the build

Only a newly introduced **dependency cycle** fails the build. Everything else is reported but lets you through.

A cycle is computed with certainty, not inferred: Tarjan's strongly-connected-components algorithm over the real import graph, confidence `1.0`. It also has real consequences — it dictates load order, makes both modules untestable in isolation, and makes that area more expensive to refactor. It's exactly the kind of thing an agent introduces without noticing.

Everything else — god classes, hotspots, deep dependency chains, layer violations, complexity outliers — is a **heuristic**, computed with statistical outlier tests. enola reports these automatically; they never break the build. Each finding carries a confidence score so you can tell the two apart.

If you want to configure the confidence threshold or which findings fail the build, you can:

```bash
enola check --fail-on=cycles,layers --min-confidence=0.8
enola check --warn-only          # report everything, fail nothing
```

Or grade the change against what you *meant* to do. `--target` runs reverse-dependency
impact analysis on the pre-change graph and reports any package the change reached
outside that radius — a package altered by something your description did not cover:

```bash
enola check --target=internal/auth                    # did it stay where I said?
enola check --target=internal/auth --max-spillover=0  # …and fail if it did not
```

This is the one thing a delta cannot work out for itself: two snapshots record what
changed, never what was intended.

## How it works

enola parses your source with tree-sitter and language-specific extractors, normalizes it into a typed fact model, links it into a directed graph, and runs graph algorithms over it: Tarjan's SCC to find groups of modules that can all reach each other (a cycle), cycle-safe longest-path for the deepest import chain, and mean+2σ outlier tests to flag what sits two standard deviations above your own repository's average. No language model, no embeddings. Terms enola uses in its own output are defined in **[docs/GLOSSARY.md](docs/GLOSSARY.md)**.

The same commit yields the same answer, every time: across 72 open-source repositories indexed three times each, all 72 produced a byte-identical snapshot ID and a byte-identical fact file, over 6.8 million facts with zero parse errors ([BENCHMARKS.md](docs/BENCHMARKS.md)). Every snapshot carries a **receipt**: enola's version, the git ref and whether the tree was dirty, the extractors used, and a snapshot ID that's a `sha256` fingerprint of the facts rather than a random UUID. Before comparing two snapshots, enola checks they were built the same way — a different extractor set or changed ignore rules makes a diff meaningless, and it reports that instead of treating the mismatch as your change.

enola runs as a local binary reading local files. Nothing leaves your machine.

It's fast enough to run on every commit: on the same 72-repository corpus, a warm re-index of an unchanged tree took 6.8s for grafana (10,313 files, 167,987 facts) and 49.6s for the Linux kernel (55,399 files, 1.9M facts). Full per-repository numbers, cold and warm, are in [BENCHMARKS.md](docs/BENCHMARKS.md).

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
| TypeScript | `tsconfig.json` / `package.json` with TypeScript (Next.js, React Navigation & monorepo aware) |
| Vue        | `package.json` with `vue` dependency (Nuxt / Vue Router / Composition API aware) |
| Svelte     | `package.json` with `svelte` dependency (SvelteKit routing / `$lib` alias aware) |
| Ember      | `package.json` with `ember-source` dependency (`.gts`/`.gjs` template tags, `.hbs` templates, router map, ember-data) |
| Python     | `pyproject.toml`, `requirements.txt`, `setup.py`, … (FastAPI / Django / SQLAlchemy aware) |
| Kotlin     | `build.gradle(.kts)` with Kotlin/Android (Compose / Hilt / Room aware) |
| Swift      | `Package.swift`, `.xcodeproj`, `.xcworkspace` (SwiftUI / UIKit aware) |
| Ruby       | `Gemfile` (Rails / ActiveRecord / Sequel / Packwerk aware) |
| Rust       | `Cargo.toml` (workspace or single crate; crate/module/`impl`/trait aware; Axum route DSL aware) |
| Scala      | an sbt/Mill/Maven/Gradle build naming Scala, or any `.scala` source (Play `conf/routes`, Pekko/Akka HTTP and http4s routes; Slick storage; sttp clients; `for … yield` read as a bind, not a loop) |
| C / C++    | `.c`/`.h` (tree-sitter-c) or `.cpp`/`.hpp`/… (tree-sitter-cpp), or `CMakeLists.txt`/`Makefile` + header (per-fact `language`, header/source method merging, namespaces, templates) |
| .NET       | `.sln`/`.slnx`/`.csproj`/`.fsproj`/`.vbproj`, or any `.cs`/`.vb`/`.fs`/`.razor`/`.cshtml`/`.xaml` source (C#, VB.NET, F#, Razor/Blazor, XAML; MSBuild `ProjectReference` as the assembly graph; ASP.NET Core attribute, minimal-API and conventional routing; EF Core/Dapper storage; `HttpClient`/Refit clients; `partial` types merged across files and languages) |
| PHP        | `composer.json`, WordPress markers, or any `.php` source (WordPress / Laravel / Symfony route + outbound HTTP-client aware) |
| Terraform / HCL | any `.tf`/`.hcl` file (blocks as Terraform addresses; prefixed and declared-set bare references; local module sources draw directory dependencies) |
| Ansible    | `ansible.cfg` or a `roles/` directory beside plays (plays → roles by name; `include_role`/`import_role`; templates counted, never rendered) |
| OpenAPI    | any spec with an `openapi:` / `swagger:` key |
| gRPC       | any `.proto` file (proto services → routes; TypeScript gRPC-web client calls detected) |
| GraphQL    | graphql-ruby root types (server) + gql tags, `.graphql` operation documents and Ruby operation strings (clients); operation documents activate detection without a TypeScript root |

Framework- and platform-specific detection for each language is described in **[ARCHITECTURE.md → Supported languages](ARCHITECTURE.md#supported-languages)**.

> Python, Ruby, PHP, and Rust are parsed with tree-sitter and contribute call and dependency edges to the graph, so `traverse`, `find_path`, and `impact_analysis` reach into them — not just modules and routes.


## Learn more

- **[docs/CLI.md](docs/CLI.md)** — setup, every command and flag, the exit codes, and the `--explain` report.
- **[docs/BENCHMARKS.md](docs/BENCHMARKS.md)** — reproducibility, delta precision, cross-repo coverage and scale, measured on 72 public repositories.
- **[docs/SNAPSHOTS.md](docs/SNAPSHOTS.md)** — why enola computes a graph on demand and keeps it as an addressable snapshot, rather than maintaining one continuously-updated graph, and where the opposite choice is the right one.
- **[docs/GLOSSARY.md](docs/GLOSSARY.md)** — the words enola uses in its own output — finding, baseline, receipt, coverage gap, incidental shift — defined in one place.
- **[docs/EXPLAINERS.md](docs/EXPLAINERS.md)** — what the ten explainers compute, why a derived finding you can trust is still not a verdict, and how a delta turns 29,633 findings about a corpus into the one that is about your change.
- **[docs/extraction/](docs/extraction/)** — per language, what specific code produces which facts, from committed fixtures, and what each extractor deliberately does not resolve.
- **[docs/EXTENDING.md](docs/EXTENDING.md)** — teaching enola a connection it does not know: binders, cross-repo signals, and the `linking:` vocabulary that fixes a wrong edge from config rather than a patch.
- **[docs/INTENT.md](docs/INTENT.md)** — declared intent: the `enola-intent.yaml` / cluster / `enola_intent:` frontmatter carriers, the full vocabulary (via, relations, origin channels), what compiles, how verdicts behave, and the working rules for keeping declarations truthful.
- **[ARCHITECTURE.md](ARCHITECTURE.md)** — the concept, the fact model, the pipeline, the MCP tool reference, and the value model.
- **[examples/](examples/)** — ready-made per-language and multi-repo configs, plus a pre-commit hook and a CI workflow.

## License

Apache License 2.0 — see [`LICENSE`](LICENSE).

This repository is the full engine, not a trial edition. Every extractor and every language ships here (Go, TypeScript/JavaScript/Vue/Svelte/Ember, Python, Java, Kotlin, Scala, Dart/Flutter, Ruby, PHP, Swift, Rust, C/C++, .NET (C#/VB.NET/F#/Razor/XAML), Terraform/HCL, Ansible, gRPC/Protobuf, OpenAPI, GraphQL), along with the cross-repo linker, all 14 MCP tools, all 11 explainers (cycles, layers, cross-repo, coverage, unused-routes, god-class, hotspots, dependency-depth, exported-surface, complexity-outliers, intent), baselines and `diff_snapshot`, snapshot receipts, the `--explain` report, and the localhost dashboard. None of this is gated, metered, or degraded without a key — there is no license check anywhere in this repository, and no snapshot, fact, or usage counter leaves your machine. (The only outbound request enola makes is to GitHub's release API, and only when you explicitly run `enola upgrade`.)

## Acknowledgements

enola bundles third-party components under their own licenses; see [`NOTICE`](NOTICE). Swift parsing uses the [tree-sitter-swift](https://github.com/alex-pinkus/tree-sitter-swift) grammar by Alex Pinkus (MIT), vendored under [`internal/extractors/swiftextractor/grammar/`](internal/extractors/swiftextractor/grammar/); Dart parsing uses [tree-sitter-dart](https://github.com/UserNobody14/tree-sitter-dart) by UserNobody14 and others (MIT), vendored under [`internal/extractors/dartextractor/grammar/`](internal/extractors/dartextractor/grammar/). Every other grammar is a normal Go module dependency and is not vendored.
