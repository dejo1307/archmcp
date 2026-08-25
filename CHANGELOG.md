# Changelog

Every released version, newest first: the headline it shipped under and, where one was
written, its summary. Dates are the GitHub release dates.

The full per-release change lists — every Added, Changed and Fixed line — are on
[enola.tech/changelog](https://enola.tech/changelog). This file is the same history at
the resolution a reader of the repository needs.

## v0.4.7 — 2026-08-25

**Layers scored per language cohort, and a built-in provider that stops building the facts the configuration throws away**

The layers explainer recognised one architecture per repository and scored every taxonomy
over the whole tree. A taxonomy now declares the languages it may classify and is scored
over that cohort alone; selection runs strongest first and skips a taxonomy whose cohort
another has already claimed, so an ungated one yields rather than competing everywhere. A
repository running two layer orders reports both, with the strongest kept beside them so
the JSON shape does not move. Violation titles carry the taxonomy name, because two
cohorts can produce the same layer pair — that changes finding identity, so a pinned
baseline churns once.

Three classes of false positive are gone. Dependency-injection and configuration packages
are classified but placed in no direction, since every layer both uses them and is used by
them, and matching now prefers an unordered layer, so a wiring directory nested inside an
ordered one no longer inherits the enclosing layer — `core/data/…/data/di` read as data
before. `android-clean` orders data innermost, per Android's own guidance, while
`ios-clean` keeps the opposite order on purpose: the two disagree about which way domain
and data depend, and neither is wrong. The Rails autoload rule stopped claiming `app/`
children Rails does not autoload, which is where front-end applications live. And `core`
left the hexagonal domain patterns, because it names a container rather than a layer: one
platform keeping its product under `src/Core/` had 1,049 of its 1,491 classified modules
read as domain on that segment alone, and 339 findings followed.

`php-layered`, `nuxt` and `sveltekit` are new — the PHP one gated on the language, since
there is no single PHP framework, the other two built from the frameworks' own prescribed
directory structures. Python, Rust, Swift, the routers/services/models Go layout and .NET
were each measured and deliberately left out, with what was measured recorded beside the
taxonomy table: three share no vocabulary that holds across unrelated repositories, one
would re-rank every `services` package on the evidence of a single example, and the .NET
repository that looks layered is the trap — a media server whose `MediaBrowser.Controller`
is its domain abstractions assembly, which dotted-name matching would read as fifty modules
of delivery. Confidence drops the coverage term whose denominator was the layer table
rather than the repository, and its floor applies to the pattern that won rather than at
admission, where it had dropped a thin framework-gated match and handed the repository to
an ungated hexagonal one.

Explainers stated their numbers in prose and every caller parsed them back out of the
title. An insight now carries `Metrics` beside its description, shaped like a fact's props;
layers publishes its denominators, conformance counts and layer order for recognised and
declared patterns alike, which is what lets the digest serve a repository that states its
own architecture rather than one recognised for it. Metrics mirrors rather than replaces:
the numbers stay in the prose the diff gate reads.

That digest is bounded now. Its repository map was one unbounded row per module, so a large
repository spent all 64,000 characters on an alphabetical census and the architecture
section was never reached. Above a row cap the map summarises by area, entry points group
by kind, routes by path prefix, storage by kind and dependency edges by out-degree, and no
one section may take more than its share of the budget — a section over its share is
truncated, not dropped. A large monolith's digest went from 64,000 characters holding two
sections to 28,000 holding ten, with no truncation left to do.

The provider seam drops facts about files the repository's ignore globs exclude, because
a provider walks the tree itself and cannot know what the configuration leaves out. That
holds for an external provider, a separate process handed a repository path. A built-in
runs in the engine's own address space, and its input already carries the predicate.

Rubydex is a built-in, and on a Rails monolith it built 2,155,664 facts so the seam
could discard 1,520,163 of them, each one named, located, given a relation, appended to
a slice growing into the millions and sorted with the rest. Collection now takes the
predicate: an excluded document is skipped before its definitions are read, and an
excluded reference before its fact is built. The census names both counts, so what the
exclusions cost stays visible rather than disappearing quietly. Same configuration, same
globs, 440 seconds before and 193 after, with 1,800,829 facts byte-for-byte identical.
Indexing is untouched — vendored gems and engine directories are still handed to the
indexer, because that is what lets a workspace constant resolve to a declaration outside
it. Indexing is how the graph resolves; emission is what enters it.

The provider work is [Muhamed Isabegović](https://github.com/misabegovic)'s.

## v0.4.6 — 2026-08-24

**A constant reference is never its own predecessor**

Fixed: a Ruby prefix walk that could not terminate. The walk steps back over the
segments written before a leaf on its line, so that `Foo::VERSION` reports `VERSION` as
the dependency and `Foo` as the path it was named through, and it found the previous
segment by comparing columns alone. A reference spanning several lines carries an end
column belonging to its last line, and when that number plus the separator happened to
equal its own start column, the reference was returned as its own predecessor and the
walk went round forever. One reference does this on a Rails monolith; the provider was
killed at 26 minutes holding 6.7GB with nothing written, on a tree the engine indexes in
24 seconds. A second Rails application hung the same way. A candidate now qualifies only
when it is a different reference ending on the line the walk is on, which is what the
adjacency pass twenty lines earlier already asserts.

Two things ride along: the walk appends and reverses once instead of allocating a new
slice at every step, and a declaration's name is fetched across the library boundary
once rather than per reference — that monolith resolves 1,624,360 references to 132,603
declarations. Facts are byte-for-byte identical where both builds complete.

This release is the work of [Muhamed Isabegović](https://github.com/misabegovic).

## v0.4.5 — 2026-08-23

**The verdict where CI reads it, provider facts cached, and detection that stops re-walking the tree**

Most of this release is the work of
[Muhamed Isabegović](https://github.com/misabegovic).

`enola check` has four writers over one verdict: `--format` picks text, json, sarif or
annotations, and nothing is recomputed for a writer. SARIF carries one rule per declared
rule id with the team's `because:` as its description, the evidence span as its region,
and a stable identity under `partialFingerprints`. Annotations place every positioned
finding on its file and line, as GitHub workflow commands or Buildkite markdown grouped
by file; findings with no position are counted at the end rather than placed somewhere
plausible. The host is a flag and never read from the environment, so a laptop renders
what CI rendered.

Every verdict now also says what it could not see. One `could not see:` line and a
census report what each provider contributed and where two of them overlapped, joined
per site. Two providers that spell the same receiver differently produce one edge rather
than two, with the surviving relation stamped `resolution_agreement`; receivers that
genuinely differ are both kept and counted by shape. `since` and `growth` read git blame
per witness file at author time, so a rebase does not restart the clock, and a breach
that cannot be dated names which of three things was unknown instead of defaulting to
now.

**Provider facts are cached.** A per-file provider keeps one entry per file keyed by
content; the built-in Rubydex provider keeps one whole-index entry keyed by its library
version, the Ruby file set and the lockfile. The receipt says how much was reused, so a
warm run that reuses nothing is visible rather than merely slow. On a Rails monolith of
roughly 1.7 million facts, providers were 249 seconds of a 292 second run before this.

**Language detection no longer re-walks the tree.** Every extractor used to answer
`Detect` with its own bounded walk, and every bound was a cliff a real repository falls
off — dotnet/runtime keeps all 3,270 of its C/C++ sources below the three levels the C++
detector scanned, so the extractor never ran at all. Detection is now membership over
the file list the engine already walked. Across a 20-repository polyglot corpus it
recovers 13,496 of the 13,828 files an extractor claimed and no extractor read.

`constraints explain` answers incoming edges as well as outgoing ones, and reports what
moving a file out of its part would cost: the verdicts that would appear and the ones
that would vanish. Rubydex emits one dependency per qualified read, carrying the file
that defines it, and every refusal carries a cause from a closed set.

Fixed: `constraints init` and `constraints explain` returned on success instead of
exiting, so the argument loop re-read `constraints` as an unknown command and exited `1`
after printing a correct report. Their exit codes are now documented beside `lint`'s.

Documentation: a [Rails workflow page](docs/RAILS.md), the declaration standard split
into [INTENT](docs/INTENT.md), [CONSTRAINTS](docs/CONSTRAINTS.md) and
[PROVIDERS](docs/PROVIDERS.md), an [index of the docs tree](docs/README.md), and this
changelog.

## v0.4.4 — 2026-08-23

**Laws written in Ruby, Angular, and the rule forms only a graph can state**

Constraints can be written as Ruby sentences, parsed and never executed, compiling to
the same declaration YAML produces. The rule vocabulary reaches 21 forms, including the
ones that need the whole graph — one owner per table, storage that stays home, a route
that must have a consumer, a file that must be governed by a page — plus `since` and
`growth` as a time dimension, and the smallest cut named beside every breach. Angular is
read the way the framework works: decorator-declared classes, dependency injection,
composed routes, templates, and requests through an injected `HttpClient`. Convention
recipes ship with the binary, so a repository binds `rails-conventions` in one command.
Rubydex joins Prism as a built-in Ruby fact provider.

## v0.4.3 — 2026-08-20

**Rails route parity, fewer query-loop false positives, and utoipa routes**

Rails route derivation matched Rails across a 3,500-route application. This release also adds dead-method detection and utoipa routes, improves query-loop analysis, and cuts a 22-repository cluster rerun from 891s to 224s.

## v0.4.2 — 2026-08-18

**Fact paths fixed on Windows; docs checked against the code**

A declared layer order could silently match zero modules on Windows. Every fact path is now forced to forward slashes regardless of host, and a new CI check catches documentation that has drifted from what the code actually does.

## v0.4.1 — 2026-08-18

**AsyncAPI channels and Kafka call sites, cross-service**

Messaging contracts declared in AsyncAPI now join the graph the same way HTTP routes do, so a missing consumer or an undeclared producer shows up as a finding instead of staying invisible.

## v0.4.0 — 2026-08-17

**Declared architectural law**

Architecture rules you write down are now verdicted against the measured graph, checked before an edit lands, and gateable in CI. Tools Enola does not ship can contribute facts through one seam, and the recorded history moves between machines.

## v0.3.21 — 2026-08-15

**Nothing fails until you name it**

The check gate no longer ships an opinion about which findings should break a build, and repository labels come from the Git remote so a second clone compares against its own baseline.

## v0.3.20 — 2026-08-14

**Express sub-router mounts compose across files**

A router declared in one file and mounted in another now reports its routes at the path they actually serve, which is the layout most Express services use.

## v0.3.19 — 2026-08-14

**Lower peak memory, and a verdict that says why nothing failed**

Cold-run peak heap drops by a quarter to two fifths on large repositories, and advisory findings now name the reason they did not fail the build.

## v0.3.18 — 2026-08-13

**Full Rails route tables, plus Grape**

Rails routes are now read from every routes file, not just the top-level one, and Grape APIs are detected automatically.

## v0.3.17 — 2026-08-12

**Five grammar updates and a protocol bump**

Java, Ruby, C, C++, and PHP move to newer tree-sitter grammars, the bundled MCP SDK updates to the latest protocol, and CI gets hermetic tests and pinned tool versions.

## v0.3.16 — 2026-08-11

**Vendored OpenAPI specs no longer read as your own server routes**

A mobile app vendoring the OpenAPI spec of a backend it calls was indexed as if it served those routes itself, manufacturing inbound edges and unused-route findings that don't exist.

## v0.3.15 — 2026-08-09

**The update-available notice actually gets written now**

The check for a newer enola release only ever ran from two entry points, so most installs never got the cache file it depends on. It now refreshes from any command.

## v0.3.14 — 2026-08-09

**Uninstall leaves nothing behind**

enola uninstall now removes the empty files and directories it leaves behind, instead of stripping its own configuration out and abandoning the shell.

## v0.3.13 — 2026-08-08

**Dart and Flutter extraction, gin routes, and update notices**

Enola now extracts Dart and Flutter apps — widgets, navigation, storage, and outbound calls — plus gin routes in Go, and tells you when a newer enola is available.

## v0.3.12 — 2026-08-08

**Scala extraction and native Codex hooks**

Enola now extracts Scala — call graph, routes, storage, and complexity metrics tuned for its effect-typed idioms — and enola install --hooks wires real session hooks for Codex.

## v0.3.11 — 2026-08-07

**Understand the architecture of an entire .NET solution**

Enola now reads every language in a .NET solution, not just C#, and understands how the pieces are wired together — project references, EF Core storage, outbound HTTP calls, and dependency injection.

## v0.3.10 — 2026-08-07

**C# extraction**

Enola now extracts C#: types and members, dependency injection, and both of ASP.NET Core's routing mechanisms.

## v0.3.9 — 2026-08-06

**Declared architectural intent, five new extraction kinds, and a lower memory footprint**

Repos and knowledge pages can now declare what the architecture is supposed to be, graded against every snapshot. Coverage extends to five new kinds, Ember support is complete, and large graphs use substantially less memory.

## v0.3.8 — 2026-08-04

**Ember and Glimmer extraction**

Enola now resolves routes, templates, services, and data-model relationships in Ember applications.

## v0.3.7 — 2026-08-04

**Incomplete binary releases are no longer published**

A release remains a GitHub draft until binaries and checksums exist for every supported platform.

## v0.3.6 — 2026-08-03

**Architecture history — enola log, show, diff, blame, and backfill**

## v0.3.5 — 2026-08-03

**Cross-repo linking becomes a plugin architecture**

## v0.3.4 — 2026-08-02

**Scope conformance, pluggable measurements & a shared command surface**

## v0.3.3 — 2026-08-01

**Python inheritance edges, and explainer findings that attribute to the right fact**

## v0.3.2 — 2026-07-31

**Config resolution, agent-hook reliability, and reproducibility fixes across explainers and extractors**

## v0.3.1 — 2026-07-30

**See which cross-repo edges resolved, from the CLI**

## v0.3.0 — 2026-07-30

**enola check: grade architectural regressions from the CLI or CI, wired into agent sessions**

## v0.2.9 — 2026-07-30

**Traversal cap fixed to bound output, not the walk, and staleness checks scoped to the loaded graph**

## v0.2.8 — 2026-07-29

**TypeScript server routes, fixed client-call detection, and multi-repo clusters outside MCP sessions**

## v0.2.7 — 2026-07-27

**Query credit scaled by the corpus searched, and seeded across restarts**

## v0.2.6 — 2026-07-27

**Value model re-anchored on measured corpus instead of flat call weights**

## v0.2.5 — 2026-07-26

**Python test-file isolation, package re-export resolution & generated-code dead-code signals**

## v0.2.4 — 2026-07-26

**Python FastAPI route composition, safer install script & cross-repo single-segment route linking**

## v0.2.3 — 2026-07-23

**Running-server instance registry fixes cross-process dashboard and restart state bugs**

## v0.2.2 — 2026-07-22

**Kotlin-on-Maven detection, programmatic servlet routes & Kafka topic cross-repo linking**

## v0.2.1 — 2026-07-21

**Cross-repo shared-symbol linking overhaul: verified matches replace name-only guesses**

## v0.2.0 — 2026-07-21

**CLI gains --help, --list & --status, plus a read-only live dashboard**

## v0.1.40 — 2026-07-20

**Cross-repo linker stops false-linking on Rails boilerplate, Swift XcodeGen app targets sub-divided, graph restore-on-restart**

## v0.1.39 — 2026-07-20

**TypeScript HTTP-client precision fixes & Axum nested-router prefix composition**

## v0.1.38 — 2026-07-19

**Go route-prefix composition, struct-usage edges & Rails route-extraction fixes**

## v0.1.37 — 2026-07-18

**I/O metadata for Rust, PHP & C++, module-graph fixes for nested layouts, SvelteKit route entry points & concurrency-safe snapshots**

## v0.1.36 — 2026-07-17

**Fixed dead-code false positives from macro-defined C/C++ functions, Python closures & guarded imports**

## v0.1.35 — 2026-07-15

**Rust language extractor & Java SPI dead-code fix**

## v0.1.34 — 2026-07-13

**Test-path gating for architecture explainers, findings-diff dedup & TypeScript ORM storage**

## v0.1.33 — 2026-07-12

**Loop-depth parity for Java/C/C++/PHP, Swift override & #if fixes, Flask route detection**

## v0.1.32 — 2026-07-10

**Test-ref parity for Go & TypeScript, Python shadow-guard fix & LLM-context repair**

## v0.1.31 — 2026-07-09

**Cross-repo linking, diff-pairing determinism & skip-accounting fixes**

## v0.1.30 — 2026-07-09

**Python value-ref resolution, relation-kind breakdown & Ruby test-file scoping**

## v0.1.29 — 2026-07-08

**Cross-repo coverage triage, Swift/Rails route fixes & Rails explainer tuning**

## v0.1.28 — 2026-07-08

**Graph-wide receipt for multi-repo snapshots**

## v0.1.27 — 2026-07-08

**Self-update via enola upgrade**

## v0.1.26 — 2026-07-08

**Python gRPC support & call-resolution fixes**

## v0.1.25 — 2026-07-07

**Explainer correctness & determinism fixes**

## v0.1.24 — 2026-07-07

**Python dead-code false-positive fixes & complexity signal improvements**

## v0.1.23 — 2026-07-05

**Gemfile-less Ruby repo detection**

## v0.1.22 — 2026-07-05

**gRPC support & expanded cacheVersion test coverage**

## v0.1.21 — 2026-07-05

**CLI version flag**

## v0.1.20 — 2026-07-04

**Snapshot receipts**

## v0.1.19 — 2026-07-04

**Expanded cross-repo HTTP-client detection**

## v0.1.18 — 2026-07-03

**TypeScript & Kotlin extractor accuracy improvements**

## v0.1.17 — 2026-07-03

**TypeScript monorepo aliases, Swift accuracy & Ruby on Rails graph**

## v0.1.16 — 2026-07-01

**C/C++ macro parsing & Ruby dynamic dispatch**

## v0.1.15 — 2026-06-29

**Diff tool, C support & extractor improvements**

## v0.1.14 — 2026-06-28

**PHP support**

## v0.1.13 — 2026-06-28

**Svelte support, shared MCP result helpers & snapshot refresh fix**

## v0.1.12 — 2026-06-27

**JS/JSX support, query_insights tool & doc fixes**

## v0.1.11 — 2026-06-26

**Unused route detection in cross-repo environments & extended testing**

## v0.1.10 — 2026-06-25

**Coverage insights, HTTP client extraction & extended testing**

## v0.1.9 — 2026-06-24

**Extraction pipeline performance pass**

## v0.1.8 — 2026-06-23

**Security patch — CVE-2026-27896**

## v0.1.7 — 2026-06-23

**Architectural smell detection**

## v0.1.6 — 2026-06-21

**Vue & Nuxt support, per-function complexity metadata & Python polyglot detection**

## v0.1.5 — 2026-06-19

**Adaptive TypeScript project detection & Python symbol indexing**

## v0.1.4 — 2026-06-18

**--explain flag & multi-language fixes**

## v0.1.3 — 2026-06-16

**C++, Java & Ruby tree-sitter rewrite**

## v0.1.2 — 2026-06-16

**Python tree-sitter rewrite**

## v0.1.1 — 2026-06-15

**TypeScript improvements, token economy & release tooling hardening**

## v0.1.0 — 2026-06-12

**First official release — Swift AST parser, HTTP client extraction & install script**

## v0.0.8

**Cross-repo resolution fixes & org migration**

## v0.0.7

**Graph of graphs & first-response latency**

## v0.0.6

**Rebrand to enola**

## v0.0.5

**Kotlin DI detection**

## v0.0.4

**OpenAPI integration**

## v0.0.3

**Ruby extractor & memory improvements**

## v0.0.2

**In-memory graph, query performance & Rails support**

## v0.0.1

**First public release**

