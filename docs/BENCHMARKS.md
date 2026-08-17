# Benchmarks

Everything here was measured on 2026-08-17, at extractor version v215 (release
0.4.0), on 81 public open-source repositories, with one binary, by scripts you can
re-run. Where a number is unflattering it is still here.

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

81 repositories, 373,855 source files parsed, 7,045,652 facts carrying 24
distinct language tags (Ansible, C, C++, C#, Dart, F#, Go, HCL, Java, Kotlin, PHP,
Python, Razor, Ruby, Rust, Scala, SQL, Stimulus, Swift, TypeScript, VB.NET, XAML,
gRPC, OpenAPI). Public open-source only: every row is a repository you can clone
and re-run.

**81 of 81 reproduce** — identical `snapshot_id` and identical
`facts.jsonl` hash across a cold run and two warm ones, i.e. across cache states.

**And 81 of 81 reproduce across separate sweeps.** The 0.4.0 numbers were measured
twice, forty minutes apart, and every repository's `facts.jsonl` is byte-identical
between the two runs. That is a stronger claim than the one above, which compares
three runs inside a single sweep: it holds across process lifetimes and a machine
whose memory and page cache had moved on. One caveat stated rather than hidden —
`snapshot_id` covers the fact stream, not the receipt's `file_hashes` list, so this
result says nothing about that field's stability.

This is one sweep, not a sum of several: the Dart/Flutter rows that were previously
measured separately are folded in here, so every number on this page comes from the
same run of the same binary.

**Ruby grew from four repositories to thirteen**, which is what surfaced the Rails
work described below. The four it had — gitlab, discourse, chatwoot, solidus — shared
one shape between them, a single application with a single root `config/routes.rb`,
and three defects hid behind that. See [the Ruby rows](#ruby--rails).

**.NET is the largest language family after C** — 1,134,730 facts across fourteen
repositories covering C#, VB.NET, F#, Razor/Blazor and XAML, against C's 1,886,304
(almost all of it the Linux kernel), Dart's 792,557 and TypeScript's 775,522. C# alone,
at 954,016, is the largest single language tag after C. See [the .NET rows](#net) below.

| Repository | Language | Files parsed | Facts | Cold | Warm |
|---|---|---|---|---|---|
| linux | c | 55,408 | 1,892,480 | 169.8s | 52.1s |
| gitlab | ruby | 49,543 | 445,793 | 41.9s | 30.4s |
| dart-sdk | dart | 16,337 | 445,416 | 61.9s | 14.7s |
| runtime | csharp | 17,772 | 397,748 | 66.1s | 39.1s |
| rust | rust | 36,083 | 394,966 | 25.1s | 15.0s |
| roslyn | csharp | 17,117 | 360,842 | 24.4s | 17.9s |
| shopware | php | 13,732 | 218,968 | 14.0s | 8.2s |
| spark | scala | 5,437 | 216,768 | 39.4s | 23.3s |
| grafana | go | 10,315 | 171,445 | 10.3s | 6.9s |
| thingsboard | java | 6,372 | 160,381 | 6.5s | 5.0s |
| flutter | dart | 3,780 | 154,587 | 10.0s | 8.4s |
| discourse | ruby | 12,118 | 127,936 | 9.8s | 7.2s |
| nextcloud-server | php | 6,037 | 101,493 | 7.5s | 3.4s |
| openproject | ruby | 10,555 | 100,692 | 9.5s | 7.9s |
| flutter-packages | dart | 1,980 | 96,081 | 7.0s | 5.7s |
| pekko | scala | 1,844 | 94,270 | 7.3s | 4.3s |
| dubbo | java | 4,351 | 82,550 | 2.5s | 2.9s |
| bitwarden-clients | typescript | 4,945 | 75,713 | 4.7s | 3.2s |
| supabase | typescript | 6,983 | 71,062 | 7.6s | 3.8s |
| fsharp | fsharp | 1,769 | 68,379 | 2.8s | 2.6s |
| airflow | python | 4,068 | 68,225 | 10.0s | 6.8s |
| ente | dart | 2,762 | 63,350 | 4.2s | 2.6s |
| deno | rust | 4,523 | 63,066 | 7.5s | 3.4s |
| avalonia | xaml | 3,774 | 62,728 | 3.7s | 2.5s |
| appflowy | dart | 2,342 | 54,889 | 2.7s | 2.2s |
| lila | scala | 2,308 | 54,778 | 4.9s | 2.6s |
| orchardcore | razor | 6,994 | 53,627 | 5.0s | 3.1s |
| superset | python | 3,841 | 52,005 | 6.0s | 3.8s |
| cal.com | typescript | 4,601 | 49,208 | 3.9s | 2.4s |
| dbt-core | rust | 1,371 | 46,935 | 3.3s | 1.8s |
| wordpress | php | 2,748 | 45,425 | 6.8s | 1.9s |
| gmsh | cpp | 1,680 | 41,529 | 3.2s | 1.3s |
| powershell | csharp | 1,202 | 40,341 | 3.5s | 1.9s |
| bitwarden-server | csharp | 3,626 | 39,957 | 3.5s | 2.7s |
| chatwoot | ruby | 3,995 | 37,122 | 2.9s | 2.0s |
| mudblazor | razor | 3,172 | 36,034 | 2.6s | 1.3s |
| gitea | go | 2,220 | 35,983 | 1.9s | 1.5s |
| immich | dart | 1,971 | 35,832 | 2.2s | 1.5s |
| rails | ruby | 2,538 | 34,253 | 2.6s | 2.1s |
| mastodon | ruby | 3,425 | 30,201 | 3.0s | 1.9s |
| saleor | python | 2,474 | 30,193 | 3.7s | 2.7s |
| flarum | php | 2,687 | 28,313 | 1.6s | 1.1s |
| jellyfin | csharp | 1,883 | 27,481 | 1.8s | 1.2s |
| zio | scala | 701 | 25,224 | 7.3s | 2.3s |
| mcp | csharp | 2,033 | 22,666 | 2.3s | 1.7s |
| pekko-http | scala | 752 | 22,275 | 1.9s | 1.4s |
| spotube | dart | 435 | 21,528 | 0.9s | 0.8s |
| drift | dart | 726 | 18,703 | 1.4s | 1.0s |
| cognee | python | 1,492 | 16,924 | 2.1s | 1.2s |
| files | xaml | 1,275 | 16,272 | 1.3s | 0.8s |
| cognee-rs | rust | 846 | 15,846 | 1.3s | 0.7s |
| fastlane | ruby | 974 | 15,215 | 1.5s | 1.0s |
| tokio | rust | 780 | 14,450 | 0.9s | 0.5s |
| flutterfire | dart | 590 | 13,677 | 1.7s | 1.3s |
| openwhisk | scala | 375 | 13,448 | 2.1s | 1.5s |
| http4s | scala | 438 | 13,068 | 3.8s | 0.9s |
| crates-io | rust | 880 | 12,485 | 1.1s | 0.7s |
| solidus | ruby | 2,012 | 12,326 | 1.5s | 1.0s |
| localsend | dart | 393 | 10,000 | 0.8s | 0.5s |
| excalidraw | typescript | 526 | 8,796 | 1.3s | 0.6s |
| enola | go | 332 | 7,064 | 0.6s | 0.5s |
| rubygems.org | ruby | 1,196 | 6,821 | 0.7s | 0.6s |
| gitbucket | scala | 219 | 5,688 | 0.9s | 0.4s |
| isowords | swift | 382 | 5,615 | 0.6s | 0.4s |
| nowinandroid | kotlin | 312 | 5,110 | 0.5s | 0.5s |
| nextcloud-collectives | php | 384 | 4,983 | 0.7s | 0.3s |
| getdp | cpp | 169 | 4,564 | 0.9s | 0.2s |
| csharp-sdk | csharp | 432 | 4,526 | 0.7s | 0.5s |
| eshop | csharp | 580 | 3,488 | 0.5s | 0.4s |
| lobsters | ruby | 528 | 2,825 | 0.5s | 0.3s |
| elk | vue | 381 | 2,563 | 0.3s | 0.2s |
| trading | scala | 122 | 2,207 | 0.4s | 0.3s |
| activeadmin | ruby | 263 | 2,015 | 0.3s | 0.3s |
| grape | ruby | 191 | 1,932 | 0.3s | 0.3s |
| nextcloud-contacts | php | 171 | 1,714 | 0.5s | 0.2s |
| devise | ruby | 171 | 1,314 | 0.2s | 0.2s |
| giraffe | fsharp | 33 | 641 | 0.1s | 0.1s |
| grpc-web-example | grpc | 12 | 321 | 0.1s | 0.1s |
| sveltekit-realworld | svelte | 42 | 195 | 0.1s | 0.1s |
| cachet | php | 21 | 101 | 0.1s | 0.1s |
| orocrm | php | 3 | 17 | 0.1s | 0.1s |

### Ruby / Rails

Thirteen repositories, 87,509 files parsed, 818,445 facts of which **552,327 are Ruby**.
All thirteen reproduce.

| Repository | Files parsed | Facts | Ruby facts | rails routes | grape routes |
|---|---:|---:|---:|---:|---:|
| gitlab | 49,484 | 439,732 | 302,735 | 2,699 | 1,554 |
| discourse | 12,102 | 126,487 | 62,564 | 1,729 | 0 |
| openproject | 10,548 | 99,480 | 74,707 | 2,767 | 389 |
| chatwoot | 3,643 | 36,296 | 13,731 | 777 | 0 |
| rails | 2,534 | 34,186 | 33,684 | 36 | 0 |
| mastodon | 3,422 | 29,598 | 17,212 | 1,138 | 0 |
| fastlane | 974 | 15,214 | 13,688 | 0 | 0 |
| solidus | 1,766 | 10,727 | 10,556 | 735 | 0 |
| rubygems.org | 1,167 | 6,386 | 6,386 | 562 | 0 |
| lobsters | 526 | 2,585 | 2,585 | 273 | 0 |
| activeadmin | 263 | 1,993 | 1,920 | 0 | 0 |
| grape | 191 | 1,931 | 1,931 | 0 | 5 |
| devise | 171 | 1,299 | 1,299 | 0 | 0 |

**The four zero rows are the result, not a gap.** `fastlane` is scale Ruby with no Rails
at all — the control for whether a Rails change breaks plain Ruby. `devise` and
`activeadmin` are engines whose only `routes.rb` files sit under `lib/` and *define*
routing DSL rather than declaring routes. `grape` yields 5 routes from 1,596 verb sites
for the same reason: `lib/grape/**` implements the DSL, and the specs that exercise it
are excluded as tests. A library defines a DSL and does not use one, so validating a
route extractor against the library that implements it measures the wrong thing — the
lesson the Scala corpus learned, applied here from the start. gitlab and openproject are
what grade the extractor; `rails`' own 36 routes come from `activestorage` and
`actionmailbox`, the two gems that ship a real route file.

#### What the nine new repositories exposed

The corpus was four repositories until this run, and all four shared one shape — a single
application with a single root `config/routes.rb`. Three defects hid behind that, and a
fourth hid behind the assumption that a route file contains only route DSL:

| | before | after |
|---|---:|---:|
| solidus route files read | 0 / 5 | **5 / 5** |
| solidus rails routes | 0 | **735** |
| discourse route files read | 1 / 26 | **26 / 26** |
| discourse rails routes | 1,195 | **1,729** |
| gitlab grape routes | 0 | **1,554** |
| openproject grape routes | 0 | **389** |
| discourse layer violations | 426 | **27** |

A Rails route table is not one file. Routes are now collected from every
`<dir>/config/routes.rb` and every `.rb` below a `config/routes/` directory at any depth,
each parsed under the prefix it is actually served at — `draw(:pkg)` followed
transitively, and `mount SomeEngine, at: '/x'` resolved to the directory owning that
constant so the engine's whole route table is parsed below `/x`. Routes also carry a
handler and a `handled_by` edge to the controller action, without which a Rails route is
an isolated node and a controller reached only through the route table reads as dead
code. The walk descends through Ruby control flow, because a route file *is* Ruby and
real ones guard whole blocks with `if`/`unless` — Rails' own
`activestorage/config/routes.rb` is a `draw` block closed by an `if` modifier, and five
route files across the corpus parsed cleanly and produced nothing.

The layer row is not an extractor change. discourse is a Rails backend beside an Ember
frontend, the ember-octane pattern wins the repo on confidence, and layer matching is by
path segment with no notion of language — so with `lib` in Ember's level-0 util layer,
every Ruby `lib/` and `plugins/*/lib/*` directory became the innermost layer and each
model or service it legitimately called became a violation. 397 of 426 were that.

**Measured parse coverage.** Every `.rb`/`.rake` file in all thirteen repositories,
parsed with the pinned `tree-sitter-ruby` v0.23.1 and its error nodes attributed to the
nearest enclosing definition: **87,729 files, 0.04% with any error, 0.01% losing a
type** — the column that matters, and materially cleaner than the Scala corpus. The
conclusion that bought is the important one: the grammar was never the problem, and
every gap above was in extraction logic.

### Dart / Flutter

Ten repositories, 31,266 files, 913,258 facts of which **792,159 are Dart**. All ten
reproduce, and **all ten parse with zero errors** — see the corpus notes in
[`enola-benchmarks`](https://github.com/enola-labs/enola-benchmarks) for the
up-front parse-coverage measurement that preceded the extractor.

| Repository | Files parsed | Facts | Dart facts | Cold | Warm |
|---|---|---|---|---|---|
| dart-sdk | 16,339 | 445,127 | 394,478 | 64.0s | 13.3s |
| flutter | 3,780 | 154,586 | 143,735 | 9.0s | 7.3s |
| flutter-packages | 1,980 | 96,081 | 96,074 | 6.4s | 5.1s |
| ente | 2,717 | 63,110 | 41,595 | 3.6s | 2.4s |
| appflowy | 2,342 | 54,889 | 40,484 | 2.4s | 1.9s |
| immich | 1,965 | 35,571 | 14,597 | 1.9s | 1.3s |
| spotube | 434 | 21,514 | 21,272 | 0.8s | 0.7s |
| drift | 726 | 18,703 | 18,703 | 1.2s | 0.9s |
| flutterfire | 590 | 13,677 | 13,645 | 1.5s | 1.2s |
| localsend | 393 | 10,000 | 7,576 | 0.7s | 0.4s |

dart-sdk is the outlier on cold time and not because of its Dart: it carries 1,138
C/C++ sources and a large `runtime/` tree, so the C/C++ extractor does substantial work
on it too. Its warm run is 4.8x faster than cold.

Two rows are cross-repo clusters rather than single applications. **immich** pairs a
Flutter client with a TypeScript server and **ente** pairs one with a Go server; split
into their halves, ente's client resolves 167 of 168 outbound call sites against its
own backend (see [Cross-repo resolution](#3-cross-repo-resolution-misses-included)).

**flutter-packages, flutterfire, drift and spotube are almost pure Dart** (96,074 of
96,081 facts; 13,645 of 13,677; 18,703 of 18,703; 21,272 of 21,514), which makes them
the rows where a Dart extraction regression shows up undiluted.

### .NET

Fourteen repositories, 61,634 files, 1,134,247 facts — the
largest language block in the corpus. All fourteen reproduce.

| Repository | Files parsed | Facts | Cold | Warm |
|---|---|---|---|---|
| runtime | 17,766 | 397,608 | 65.9s | 37.2s |
| roslyn | 17,117 | 360,842 | 23.6s | 16.3s |
| fsharp | 1,769 | 68,379 | 2.7s | 2.3s |
| avalonia | 3,774 | 62,728 | 3.2s | 2.2s |
| orchardcore | 6,979 | 53,369 | 4.3s | 2.6s |
| powershell | 1,202 | 40,341 | 2.9s | 1.6s |
| bitwarden-server | 3,626 | 39,957 | 3.0s | 2.2s |
| mudblazor | 3,170 | 35,987 | 2.2s | 1.2s |
| jellyfin | 1,883 | 27,481 | 1.6s | 1.0s |
| mcp | 2,033 | 22,666 | 2.0s | 1.4s |
| files | 1,275 | 16,272 | 1.2s | 0.7s |
| csharp-sdk | 427 | 4,488 | 0.6s | 0.4s |
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
| lila | Play `conf/routes` — one file declaring 923 endpoints, with four included sub-routers |
| openwhisk, pekko-http | a Pekko HTTP **application** against the library that defines the DSL |
| trading, http4s | the same pairing for http4s, and an effect-heavy Scala 3 codebase |
| spark, pekko | Scala **beside Java** in one tree — 1,355 and 582 `.java` in the same packages |

**giraffe is the row that matters most.** It produced **0 facts** before this work —
detection matched its `.slnx`, the extractor claimed the repository, and reported a
successful snapshot of 46 unread F# sources. A green snapshot of nothing is worse
than an unsupported language, because nothing in the output says so.

## 1. Reproducibility

Each repository was indexed **three times** — once cold, twice warm — and the
receipt's `snapshot_id` and the SHA-256 of `facts.jsonl` were compared across all
three. Running cold then warm is the point: it tests that a cached run and a
from-scratch run agree, not merely that the same code path repeats itself.

> **81 of 81 repositories in this sweep produced a byte-identical `snapshot_id` and a
> byte-identical `facts.jsonl` across all three runs — 243 runs, 7,045,652 facts,
> zero drift.** `insights.json` is byte-stable on all 81 as well. This is one sweep:
> the Dart/Flutter rows previously measured separately are folded in.

The 0.4.0 validation ran the whole sweep **twice**, forty minutes apart, and compared
the two: all 81 `facts.jsonl` are byte-identical between sweeps as well — 486 runs in
total. Three runs inside one sweep share a process lifetime and a warm page cache;
two sweeps do not, so this rules out a class of drift the in-sweep check cannot see.

What it does not cover is worth naming. `snapshot_id` hashes the fact stream, the
version and the config — **not** the receipt's `file_hashes` list, which records a
content hash per walked file and is what `enola check` and `plan` use to judge whether
a snapshot still describes the working tree. A discrepancy confined to that list would
leave every number above unchanged.

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

**The policy these runs enforced was `--fail-on=cycles`**, stated because it has to be:
`enola check` fails nothing unless a policy names it, so the FAIL column is what that
flag does, not what a bare run does. The column that matters for precision is the same
either way — the delta itself, and the fact that it is exactly one finding.

| Repository | Language | Pre-existing findings | No change | Benign addition | Injected cycle | Reverted |
|---|---|---|---|---|---|---|
| gitea | Go | 171 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| enola | Go | 145 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| superset | Python | 133 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| cognee | Python | 121 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| excalidraw | TypeScript | 111 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| chatwoot | Ruby | 104 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| jellyfin | C# | 103 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| eshop | C# | 89 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| solidus | Ruby | 72 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| crates-io | Rust | 65 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| gitbucket | Scala | 50 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| nowinandroid | Kotlin | 37 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| elk | TypeScript | 26 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| sveltekit-realworld | TypeScript | 1 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| cachet | PHP | 0 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |

Read the columns as four separate claims, all of which hold on all fifteen:

- **No change → +0 facts, +0 edges, PASS.** Exactly zero on all fifteen repositories.
  That's what makes a PASS or FAIL on a real change something you can rely on.
- **Benign addition → PASS**, with the delta naming exactly the 2–3 facts added.
  A new leaf module isn't a structural regression, so there's nothing to report.
- **Injected cycle → FAIL, exactly 1 regression** — out of **1,228 pre-existing
  findings across these repositories**, up to 171 in a single one. None of them was
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

### What is eligible to fail at all

Across the corpus enola produced **9,131 findings**. Broken down:

| Explainer | Findings | Class |
|---|---|---|
| god-class | 1,648 | statistical outlier |
| hotspots | 1,322 | statistical outlier |
| **cycles** | **1,225** | **structural fact + heuristic — see below** |
| exported-surface | 1,102 | candidate |
| layers | 1,078 | heuristic |
| complexity-outliers | 1,062 | statistical outlier |
| query-loops | 678 | heuristic |
| dependency-depth | 603 | statistical outlier |
| domain | 401 | heuristic |
| entry-points | 12 | candidate |

**The total fell from 29,633 at v197, and 23,194 of that drop is one explainer.**
hotspots was roughly 80% of every finding enola produced, which meant the ranking of
one explainer decided what a reader saw first, and the other nine were buried
underneath it. It now reports at most its top 20 per repository, highest score first,
ties broken by name so the cut is deterministic. The outlier threshold already kept
the set small on most repositories, so the cap only bites where the volume was noise.

Two smaller corrections travelled with it. hotspots scores fan-in × fan-out, and
`has_method` edges — a type to the methods it declares — were being counted as calls
*out of* the type, so a large class read as a pinch point for being large: one
449-line importer whose body is 102 one-line delegations and exactly one call out
ranked in a monolith's top 20 as "it calls out to 104 others". And `query-loops`,
`domain` and `entry-points` are new in 0.4.0, which is where 1,091 of the remaining
findings come from.

**None of these fail anything by default** — `enola check` names no explainer unless
you do. The number worth reading is how much of the corpus is even *eligible* to fail
at the `1.00` floor, whatever you name: only findings enola proves reach it, and every
statistical row above caps below it by construction.

**The cycles row is not all one thing, and the difference is instructive.** The
explainer emits a load-order cycle at confidence `1.0` and, separately, a *highly
coupled module cluster* at `0.4` — mutual references between directories in an
autoloaded codebase (Rails, say), where constants resolve lazily and there is no
load-order defect to break. Both carry `source: cycles`, so counting the explainer
overstates what `--fail-on=cycles` would do.

Counting the corpus by confidence rather than by explainer: **1,298 of the 9,131
findings sit at the `certain` level**, the only one that reaches the `1.00` floor.
So **14.2% could fail a build even with every explainer named**, and the other 85.8%
are reported and let you through however you configure it.

That share was 3.16% at v197, and the rise is a denominator, not a loosening: capping
hotspots removed 23,194 findings that could never have failed anything, so what is
left is a higher proportion of provable ones. The count that can fail barely moved.
The ratio is the design either way — the confidence floor keeps an estimate from
breaking a build even when someone asks it to, and the empty default keeps enola from
asking on your behalf.

## 3. Cross-repo resolution, misses included

enola reports both resolved and unresolved edge counts, so you know how many
dependents a service actually has.

### On the committed multi-repo fixtures

Eleven cross-language fixtures, each with a golden fact file the test suite asserts on:

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
| dart_multirepo | Dart ↔ Go | 3 | 3 | 0 | 0 | verified |
| **Total** | | **24** | **19** | **3** | **2** | |

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

### On a Dart/Flutter cluster

`ente-io/ente`'s Flutter client plus its Go server — a mobile app and the backend it
talks to, indexed as two repositories. A mobile client is the extreme case for
cross-repo linking: it serves nothing, imports nothing from the backend, and shares no
code with it, so an outbound call site is the *only* structural evidence the two belong
to one system.

```
  service   classification  detected  resolved  unresolved
  mobile    connected           168       167           0
  server    isolated              0         0           0
```

Before, this read `coverage_gap: 168 detected / 0 resolved / 167 unresolved` — and the
missing side was **not** the Dart one. ente's server is written with **gin**, which the
Go extractor did not read, so the client's 168 call sites had nothing to match against.
Adding gin recovers 359 server routes (one per registration outside test files, exactly)
and 112 of the client's 117 distinct paths match one exactly.

`server` reads `isolated` rather than `coverage_gap` because it makes no outbound calls
of its own — a backend at the edge of the graph, which is the correct classification and
not a gap.

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
| Largest repository indexed | **Linux kernel** — 55,408 files, **1,892,479 facts**, 145.0s cold / 24.3s warm |
| Largest .NET | dotnet/runtime — 17,766 files, 397,608 facts, 64.2s / 29.4s |
| Largest Ruby | GitLab — 49,484 files, 439,732 facts, 30.7s / 20.1s |
| Largest Rust | rust-lang/rust — 36,082 files, 394,930 facts, 20.3s / 8.1s |
| Largest Scala | Spark — 5,437 files, 216,767 facts, 35.4s / 18.1s |
| Largest Go | Grafana — 10,313 files, 167,987 facts, 7.4s / 4.6s |
| Throughput | 3,100–30,300 facts/sec depending on language |
| Parse errors, all 81 repositories | **0** |
| Memory | peak heap per run is recorded by the sweep (`--memstats`) alongside time and hashes. The Linux kernel is the high-water mark at **5,228 MB**; only five others exceed 1 GB (GitLab 1,549, dotnet/runtime 1,454, roslyn 1,320, dart-sdk 1,266, rust-lang/rust 1,128). No repository required tuning on this machine |

Warm runs are 1.09×–5.78× faster than cold (over the 57 repositories whose cold run
exceeds 0.5s; below that the timing is noise), from the per-file content-hash cache
in `snapshot.meta.json`. These numbers establish that the graph the other four
sections rely on can actually be built on real code. enola isn't benchmarked on
speed as a competitive claim.

## 5. What the extractors see

Across the corpus enola extracted **32,110 routes** (26,833 server, 5,277 client) and
recognised **47 distinct frameworks** without configuration:

```
rails 10716 · wordpress 6668 · graphql 2114 · grape 1948 · openapi 1378
axios 1240 · aspnetcore 1192 · play 923 · request-options 779 · chi 751
spring 578 · symfony 574 · nestjs 456 · resttemplate 423 · fastapi 357
flask 297 · fetch 268 · dart 173 · go_router 156 · grpc 134 · axum 133
vue 103 · auto_route 100 · graphql-ruby 81 · net/http 63 · nuxt 55
blazor 48 · openapi-fetch 46 · guzzle 44 · navigator 43 · http-client 40
nextjs 39 · httpclient 36 · pekko-http 29 · faraday 23 · http4s 21
net-http 14 · sveltekit 13 · client-seam 10 · file-get-contents 9 · hono 7
gorilla/mux 5 · express 5 · retrofit 4 · django 4 · urlsession 2
razorpages 2
```

**rails goes from 3,612 to 10,716 and grape enters at 1,948**, and only part of that is
the nine new Ruby repositories. The rest is the Rails route work: routes were previously
collected from the root `config/routes.rb` alone, so every engine and plugin route file
in the corpus went unread — solidus reported 0 Rails routes against 195 declarations in
its five engine route files — and Grape had no extractor at all, leaving GitLab's entire
v4 REST API invisible behind a single `mount ::API::API`.

Fact kinds: 5,206,702 symbols · 1,557,462 dependencies · 89,977 test refs ·
80,394 file refs · 67,528 modules · 32,110 routes · 6,543 associations ·
4,902 storage · 28 extraction · 6 intent. **`association` is new in 0.4.0** — a
model's declared `has_many`/`belongs_to` relations, which is what lets `endpoint`
walk from a URL to the tables behind it. Service nodes
are absent here by construction: the sweep indexes one repository at a time, and a
service node only exists in a multi-repo graph — those are section 3's subject.

**Scala's three route surfaces appear at `play 923`, `pekko-http 29` and
`http4s 21`**, and the shape of that split is the point rather than the totals. Play
declares a whole application's endpoints in one file, so one application supplies
923 of them; the other two are DSLs written per route tree, and the corpus holds one
real application for each beside the library that defines it. A library defines a
DSL and does not use one — which is why counting `pathPrefix` in a routing library
finds the directive's own definition and almost no endpoints.

`aspnetcore` at 1,192 is the fourth-largest framework, and `storage` is non-zero for
.NET at all — both were **zero** before this corpus was indexed for it. `blazor 48`
and `razorpages 2` count `@page` routes only; the bulk of Razor's contribution is
reference edges rather than routes, which is what the format is mostly used for.

Two rows are worth reading carefully rather than as a score. **django 4** counts
routes, not coverage: Saleor is GraphQL-first, so its REST surface really is that
small — 140 of its 144 routes carry the `graphql` tag — while its facts still carry
the Django tag too. And **axum 132** comes almost entirely from two other Rust
services (77 and 47); crates.io supplies only 8, because it declares most of its API
through `utoipa`'s `routes!()` macro, which enola deliberately does not expand — a
documented limit in [rust.md](extraction/rust.md).

A third belongs to .NET. OrchardCore contributes 82 routes where it declares 288
verb attributes, because 94% of its controllers are conventionally routed and 20 of
its registrations use a `{area}/{controller}/{action}` template that cannot be
resolved without each controller's area. That gap is counted and logged rather than
closed by guessing — see [dotnet.md](extraction/dotnet.md).

## 6. Agent A/B — does the agent ship the regression?

> Unlike sections 1–5, this one was **not** re-run for the current corpus: it needs
> real agent sessions rather than a script, so its numbers are carried forward from
> the earlier run. The fixture and the finding are unchanged; only the date differs.

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
- **Scala has no cross-repo cluster.** It is measured in sections 1 and 2 across
  nine repositories, but section 3 pairs a backend with its own clients and no
  Scala pair exists in the corpus, so Scala contributes nothing to the coverage
  numbers. Its client-side extraction (sttp, Play WS) is exercised by unit tests
  and the golden fixture only.
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
enola check --fail-on=cycles .   # exit 1 on a NEW cycle; a bare `check` reports
                                 # everything and exits 0, so name what should fail

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
