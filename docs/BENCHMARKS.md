# Benchmarks

Everything here was measured on 2026-08-23, at extractor version v253, on 91 public
open-source repositories, with one binary, by scripts you can re-run. This page
carries the latest sweep rather than a released version's, so it moves when the
extractors do. Where a number is unflattering it is still here.

Timings are the engine's own snapshot `duration` rather than wall-clock. The sweep
passes `--memstats`, which forces a full GC before reporting its figures, and on a
kernel-sized heap that is a few hundred milliseconds of process time no user pays.

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

91 repositories, 445,033 source files parsed of 743,959 seen, 8,014,480 facts
carrying 26 distinct language tags (Ansible, C, C++, C#, Dart, F#, Go, HCL, Java,
Kotlin, Markdown, PHP, Python, Razor, Ruby, Rust, Scala, SQL, Stimulus, Swift,
TypeScript, VB.NET, XAML, gRPC, OpenAPI, and enola's own intent pages). Public open-source only: every row is a
repository you can clone and re-run.

**91 of 91 reproduce** — identical `snapshot_id` and identical
`facts.jsonl` hash across a cold run and two warm ones, i.e. across cache states.

**And 81 of 81 reproduce across separate sweeps**, measured when 0.4.0 was validated:
that sweep ran twice, forty minutes apart, and every repository's `facts.jsonl` was
byte-identical between the two runs. That is a stronger claim than the one above, which compares
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

**TypeScript is now the largest language after C** — 1,150,446 facts against C's
1,920,024 (almost all of it the Linux kernel). It was 781,437 at v224, and the jump is
the v253 detection change rather than a corpus change: extractors used to answer
`Detect` with their own bounded walk, so loose `.js` and `.ts` below a scan depth were
claimed and never read. It overtakes both the .NET family — 1,122,993 facts tagged C#,
VB.NET, F#, Razor/Blazor or XAML — and C# alone at 954,285, each of which held the
position at v224. Dart is unchanged at 792,439. Every figure in this paragraph counts
facts by **language tag**, not by repository: a per-language section below totals
everything its repositories contain, which is a larger and different number.
See [the .NET rows](#net) below.

| Repository | Language | Files parsed | Facts | Cold | Warm |
|---|---|---|---|---|---|
| linux | c | 55,884 | 1,908,738 | 174.7s | 43.4s |
| gitlab | ruby | 55,937 | 544,435 | 51.9s | 33.0s |
| runtime | csharp | 23,547 | 529,820 | 75.7s | 42.2s |
| dart-sdk | dart | 16,823 | 453,013 | 58.0s | 8.3s |
| rust | rust | 37,565 | 404,094 | 29.8s | 13.6s |
| roslyn | csharp | 17,518 | 375,204 | 20.4s | 11.4s |
| shopware | php | 19,905 | 237,988 | 14.7s | 7.7s |
| spark | scala | 5,745 | 219,732 | 35.8s | 18.9s |
| flutter | dart | 7,788 | 210,794 | 10.1s | 6.4s |
| grafana | go | 11,344 | 186,369 | 9.1s | 5.7s |
| thingsboard | java | 6,618 | 163,292 | 5.8s | 4.1s |
| discourse | ruby | 13,250 | 143,940 | 10.0s | 8.7s |
| flutter-packages | dart | 3,865 | 136,165 | 7.7s | 4.3s |
| gauzy | angular | 8,870 | 119,935 | 6.4s | 2.7s |
| openproject | ruby | 11,285 | 109,514 | 10.4s | 7.6s |
| nextcloud-server | php | 6,057 | 101,673 | 7.4s | 2.2s |
| pekko | scala | 2,296 | 97,647 | 6.0s | 2.7s |
| bitwarden-clients | typescript | 5,071 | 83,221 | 4.1s | 2.1s |
| dubbo | java | 4,368 | 82,786 | 2.2s | 1.9s |
| ente | dart | 3,693 | 79,680 | 3.7s | 1.6s |
| angular | angular | 7,097 | 78,896 | 4.9s | 2.1s |
| spartacus | angular | 7,266 | 75,315 | 4.3s | 2.5s |
| supabase | typescript | 7,337 | 74,649 | 8.1s | 3.5s |
| airflow | python | 4,364 | 73,824 | 9.9s | 6.2s |
| fsharp | fsharp | 1,917 | 69,854 | 2.0s | 1.5s |
| avalonia | xaml | 3,881 | 64,757 | 3.1s | 1.5s |
| deno | rust | 4,644 | 63,983 | 7.8s | 2.4s |
| orchardcore | razor | 7,295 | 59,342 | 4.3s | 1.9s |
| appflowy | dart | 2,472 | 56,890 | 2.1s | 1.2s |
| superset | python | 4,072 | 55,315 | 8.0s | 3.3s |
| lila | scala | 2,332 | 55,025 | 4.0s | 1.6s |
| cal.com | typescript | 4,957 | 51,352 | 4.1s | 2.7s |
| dbt-core | rust | 1,481 | 49,074 | 3.6s | 1.0s |
| wordpress | php | 2,770 | 45,512 | 6.2s | 1.2s |
| gmsh | cpp | 1,758 | 42,906 | 2.9s | 0.7s |
| powershell | csharp | 1,303 | 42,837 | 3.2s | 1.1s |
| peertube | angular | 2,825 | 42,819 | 29.0s | 0.9s |
| bitwarden-server | csharp | 3,701 | 42,044 | 3.0s | 1.9s |
| mudblazor | razor | 3,184 | 38,734 | 2.2s | 0.8s |
| immich | dart | 2,155 | 38,152 | 1.8s | 0.9s |
| rails | ruby | 2,667 | 37,899 | 2.4s | 1.6s |
| chatwoot | ruby | 4,007 | 37,251 | 2.8s | 1.6s |
| gitea | go | 2,250 | 36,507 | 1.8s | 1.0s |
| taiga-ui | angular | 2,980 | 35,419 | 1.7s | 1.0s |
| flutterfire | dart | 1,149 | 32,953 | 1.7s | 1.0s |
| angular-components | angular | 2,271 | 32,419 | 1.8s | 1.0s |
| saleor | python | 2,501 | 30,589 | 3.7s | 2.3s |
| mastodon | ruby | 3,440 | 30,245 | 3.1s | 2.8s |
| flarum | php | 2,721 | 28,986 | 1.5s | 0.7s |
| zio | scala | 1,136 | 28,208 | 5.4s | 1.5s |
| mcp | csharp | 2,312 | 28,048 | 2.1s | 1.3s |
| jellyfin | csharp | 1,893 | 27,623 | 1.5s | 0.7s |
| pekko-http | scala | 1,028 | 23,759 | 1.5s | 0.9s |
| spotube | dart | 455 | 21,917 | 0.7s | 0.4s |
| drift | dart | 898 | 20,361 | 1.1s | 0.6s |
| cognee | python | 1,541 | 17,635 | 2.0s | 0.9s |
| files | xaml | 1,287 | 17,351 | 1.1s | 0.5s |
| cognee-rs | rust | 991 | 17,237 | 1.2s | 0.4s |
| fastlane | ruby | 1,071 | 15,885 | 1.3s | 0.8s |
| tokio | rust | 810 | 15,283 | 0.9s | 0.3s |
| openwhisk | scala | 479 | 15,252 | 1.7s | 1.1s |
| http4s | scala | 475 | 13,867 | 2.6s | 0.4s |
| crates-io | rust | 929 | 12,924 | 1.1s | 0.4s |
| solidus | ruby | 2,036 | 12,890 | 1.4s | 0.8s |
| localsend | dart | 442 | 10,612 | 0.6s | 0.2s |
| ngrx | angular | 1,075 | 10,356 | 0.8s | 0.4s |
| dashboard | angular | 636 | 9,995 | 0.5s | 0.3s |
| excalidraw | typescript | 544 | 9,276 | 1.4s | 0.4s |
| enola | go | 444 | 9,183 | 8.9s | 0.3s |
| rubygems.org | ruby | 1,212 | 6,682 | 0.6s | 0.4s |
| gitbucket | scala | 239 | 5,867 | 0.7s | 0.3s |
| isowords | swift | 388 | 5,847 | 0.6s | 0.3s |
| nextcloud-collectives | php | 414 | 5,458 | 0.6s | 0.3s |
| csharp-sdk | csharp | 498 | 5,403 | 0.5s | 0.3s |
| nowinandroid | kotlin | 357 | 5,322 | 0.4s | 0.3s |
| getdp | cpp | 184 | 4,662 | 0.8s | 0.1s |
| eshop | csharp | 585 | 3,625 | 0.4s | 0.2s |
| lobsters | ruby | 541 | 2,856 | 0.4s | 0.2s |
| ngx-admin | angular | 254 | 2,746 | 0.3s | 0.1s |
| elk | vue | 397 | 2,710 | 0.4s | 0.2s |
| grape | ruby | 200 | 2,406 | 0.2s | 0.2s |
| activeadmin | ruby | 293 | 2,383 | 0.2s | 0.2s |
| trading | scala | 128 | 2,266 | 0.2s | 0.1s |
| nextcloud-contacts | php | 177 | 1,812 | 0.4s | 0.2s |
| ng-alain | angular | 139 | 1,766 | 0.2s | 0.1s |
| devise | ruby | 176 | 1,387 | 0.1s | 0.1s |
| giraffe | fsharp | 49 | 1,250 | 0.1s | 0.1s |
| grpc-web-example | grpc | 14 | 339 | 0.1s | 0.1s |
| sveltekit-realworld | svelte | 45 | 215 | 0.1s | 0.1s |
| cachet | php | 28 | 163 | 0.1s | 0.1s |
| orocrm | php | 7 | 65 | 0.1s | 0.1s |

### Ruby / Rails

Thirteen repositories, 96,115 files parsed, 947,773 facts of which **556,078 are Ruby**.
All thirteen reproduce.

| Repository | Files parsed | Facts | Ruby facts | rails routes | grape routes |
|---|---:|---:|---:|---:|---:|
| gitlab | 55,937 | 544,435 | 306,745 | 2,059 | 1,554 |
| discourse | 13,250 | 143,940 | 69,909 | 1,552 | 0 |
| openproject | 11,285 | 109,514 | 74,437 | 1,678 | 250 |
| mastodon | 3,440 | 30,245 | 17,414 | 777 | 0 |
| rails | 2,667 | 37,899 | 33,668 | 23 | 0 |
| chatwoot | 4,007 | 37,251 | 14,363 | 690 | 0 |
| solidus | 2,036 | 12,890 | 11,625 | 690 | 0 |
| rubygems.org | 1,212 | 6,682 | 6,291 | 239 | 0 |
| lobsters | 541 | 2,856 | 2,749 | 246 | 0 |
| grape | 200 | 2,406 | 1,932 | 0 | 5 |
| fastlane | 1,071 | 15,885 | 13,689 | 0 | 0 |
| devise | 176 | 1,387 | 1,314 | 0 | 0 |
| activeadmin | 293 | 2,383 | 1,942 | 0 | 0 |

**The four zero rows are the result, not a gap.** `fastlane` is scale Ruby with no Rails
at all — the control for whether a Rails change breaks plain Ruby. `devise` and
`activeadmin` are engines whose only `routes.rb` files sit under `lib/` and *define*
routing DSL rather than declaring routes. `grape` yields 5 routes from 1,596 verb sites
for the same reason: `lib/grape/**` implements the DSL, and the specs that exercise it
are excluded as tests. A library defines a DSL and does not use one, so validating a
route extractor against the library that implements it measures the wrong thing — the
lesson the Scala corpus learned, applied here from the start. gitlab and openproject are
what grade the extractor; `rails`' own 23 routes come from `activestorage` and
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

Ten repositories, 39,740 files, 1,060,537 facts of which **792,041 are Dart**. All ten
reproduce, and **all ten parse with zero errors** — see the corpus notes in
[`enola-benchmarks`](https://github.com/enola-labs/enola-benchmarks) for the
up-front parse-coverage measurement that preceded the extractor.

| Repository | Files parsed | Facts | Dart facts | Cold | Warm |
|---|---|---|---|---|---|
| dart-sdk | 16,823 | 453,013 | 394,478 | 58.0s | 8.3s |
| flutter | 7,788 | 210,794 | 143,735 | 10.1s | 6.4s |
| flutter-packages | 3,865 | 136,165 | 96,074 | 7.7s | 4.3s |
| ente | 3,693 | 79,680 | 41,595 | 3.7s | 1.6s |
| appflowy | 2,472 | 56,890 | 40,484 | 2.1s | 1.2s |
| immich | 2,155 | 38,152 | 14,479 | 1.8s | 0.9s |
| flutterfire | 1,149 | 32,953 | 13,645 | 1.7s | 1.0s |
| spotube | 455 | 21,917 | 21,272 | 0.7s | 0.4s |
| drift | 898 | 20,361 | 18,703 | 1.1s | 0.6s |
| localsend | 442 | 10,612 | 7,576 | 0.6s | 0.2s |

dart-sdk is the outlier on cold time and not because of its Dart: it carries 1,138
C/C++ sources and a large `runtime/` tree, so the C/C++ extractor does substantial work
on it too. Its warm run is 4.8x faster than cold.

Two rows are cross-repo clusters rather than single applications. **immich** pairs a
Flutter client with a TypeScript server and **ente** pairs one with a Go server; split
into their halves, ente's client resolves 167 of 168 outbound call sites against its
own backend (see [Cross-repo resolution](#3-cross-repo-resolution-misses-included)).

**flutter-packages, flutterfire, drift and spotube are almost pure Dart** (96,074 of
96,081 facts; 13,645 of 13,677; 18,703 of 18,703; 21,272 of 21,528), which makes them
the rows where a Dart extraction regression shows up undiluted.

### .NET

Fourteen repositories, 68,970 files, 1,305,892 facts — everything those repositories
contain, markdown and configuration included. Counted by language tag instead, C#,
VB.NET, F#, Razor and XAML together carry 1,122,993 of them; the corpus section above
ranks the languages on that basis. All fourteen reproduce.

| Repository | Files parsed | Facts | Cold | Warm |
|---|---|---|---|---|
| runtime | 23,547 | 529,820 | 75.7s | 42.2s |
| roslyn | 17,518 | 375,204 | 20.4s | 11.4s |
| fsharp | 1,917 | 69,854 | 2.0s | 1.5s |
| avalonia | 3,881 | 64,757 | 3.1s | 1.5s |
| orchardcore | 7,295 | 59,342 | 4.3s | 1.9s |
| powershell | 1,303 | 42,837 | 3.2s | 1.1s |
| bitwarden-server | 3,701 | 42,044 | 3.0s | 1.9s |
| mudblazor | 3,184 | 38,734 | 2.2s | 0.8s |
| mcp | 2,312 | 28,048 | 2.1s | 1.3s |
| jellyfin | 1,893 | 27,623 | 1.5s | 0.7s |
| files | 1,287 | 17,351 | 1.1s | 0.5s |
| csharp-sdk | 498 | 5,403 | 0.5s | 0.3s |
| eshop | 585 | 3,625 | 0.4s | 0.2s |
| giraffe | 49 | 1,250 | 0.1s | 0.1s |

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

### Angular

Ten repositories, 33,413 files parsed, 409,666 facts. All ten reproduce; the largest
peaks at 395 MiB.

| Repository | Files parsed | Facts | Cold | Warm |
|---|---|---|---|---|
| gauzy | 8,870 | 119,935 | 6.4s | 2.7s |
| angular | 7,097 | 78,896 | 4.9s | 2.1s |
| spartacus | 7,266 | 75,315 | 4.3s | 2.5s |
| peertube | 2,825 | 42,819 | 29.0s | 0.9s |
| taiga-ui | 2,980 | 35,419 | 1.7s | 1.0s |
| angular-components | 2,271 | 32,419 | 1.8s | 1.0s |
| ngrx | 1,075 | 10,356 | 0.8s | 0.4s |
| dashboard | 636 | 9,995 | 0.5s | 0.3s |
| ngx-admin | 254 | 2,746 | 0.3s | 0.1s |
| ng-alain | 139 | 1,766 | 0.2s | 0.1s |

The corpus is split so that each mechanism has a control, the same way the .NET one is:

| Repo | What it is the control for |
|---|---|
| angular, components | the framework and the component library that **define** the decorators and the router — a library defines a DSL and does not use one |
| ngx-admin, ng-alain | classic **NgModule** applications; ngx-admin indexes in 0.4s and is the row to iterate against |
| taiga-ui | **standalone components and signals** throughout — 153 `standalone: true`, 213 `loadComponent`, and every route path written as an enum member |
| spartacus | library *and* application — 924 NgModules, and the control for a repository whose only `forRoot` calls are in specs |
| gauzy | the **Nx workspace** shape — 99 projects, a `ROUTES` provider factory, and workspace-package aliases |
| peertube, dashboard, gauzy | Angular against an **Express**, a **Go** and a **NestJS** backend |
| ngrx | a DI/state library with no application around it |

**peertube's 33.0s cold against a 1.8s warm** is the row to watch, and it is not the
Angular work: the same repository took 29.6s cold before any of it, and the cost is
its OpenAPI specification plus a 2,825-file TypeScript tree.

**Two rows correctly report almost nothing.** spartacus produces no routes at all —
its only `RouterModule.forRoot` calls live in `.spec.ts` files, and its routing is
configuration its consuming application applies. ngx-admin resolves 51% of its
template references because the other half name components from a third-party library
that lives in `node_modules`. Both numbers are reported with the cause named rather
than folded into a total.

## 1. Reproducibility

Each repository was indexed **three times** — once cold, twice warm — and the
receipt's `snapshot_id` and the SHA-256 of `facts.jsonl` were compared across all
three. Running cold then warm is the point: it tests that a cached run and a
from-scratch run agree, not merely that the same code path repeats itself.

> **91 of 91 repositories in this sweep produced a byte-identical `snapshot_id` and a
> byte-identical `facts.jsonl` across all three runs — 273 runs, 8,014,480 facts,
> zero drift.** `insights.json` is byte-stable on all 91 as well. This is one sweep:
> the Dart/Flutter rows previously measured separately are folded in.

Two defects had to be fixed for that sentence to be true again, and both had the same
shape: **a snapshot must not depend on whether a previous snapshot exists.** A
markdown link was resolved by stat-ing the filesystem, so this repository's own docs
— which cite paths under its output directory — linked to nothing on a cold run and
to something on the next. And the output directory was ignored only at the repository
root, so a cluster config that snapshots subdirectories left an `.enola` in each and
enola indexed its own `llm_context.md` as a source document. Ten repositories in this
corpus carried such facts, `linux` and `wordpress` among them. Links now resolve
against the walked file set, and the output directory is ignored at any depth.

The 0.4.0 validation ran the whole sweep **twice**, forty minutes apart, and compared
the two: all 81 `facts.jsonl` were byte-identical between sweeps as well — 486 runs in
total. That check belongs to a release rather than to every sweep, so it is the one
number here carried forward. Three runs inside one sweep share a process lifetime and a warm page cache;
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
| solidus | Ruby | 239 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| chatwoot | Ruby | 231 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| gauzy | TypeScript | 182 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| superset | Python | 130 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| cognee | Python | 111 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| drift | Dart | 102 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| jellyfin | C# | 100 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| lobsters | Ruby | 98 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| localsend | Dart | 96 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| gitea | Go | 93 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| eshop | C# | 80 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| excalidraw | TypeScript | 73 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| enola | Go | 69 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| crates-io | Rust | 67 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| gitbucket | Scala | 56 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |
| ngx-admin | TypeScript | 44 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| nowinandroid | Kotlin | 39 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| elk | TypeScript | 27 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| sveltekit-realworld | TypeScript | 2 | PASS · +0 facts | PASS · +2 facts | **FAIL · 1 regression** | PASS · +0 |
| cachet | PHP | 0 | PASS · +0 facts | PASS · +3 facts | **FAIL · 1 regression** | PASS · +0 |

Read the columns as four separate claims, all of which hold on all twenty:

- **No change → +0 facts, +0 edges, PASS.** Exactly zero on all twenty repositories.
  That's what makes a PASS or FAIL on a real change something you can rely on.
- **Benign addition → PASS**, with the delta naming exactly the 2–3 facts added.
  A new leaf module isn't a structural regression, so there's nothing to report.
- **Injected cycle → FAIL, exactly 1 regression** — out of **1,620 pre-existing
  findings across these repositories**, up to 235 in a single one. None of them was
  repeated. The ratchet holds.
- **Reverted → PASS again**, +0 facts. The verdict is a function of the tree, not
  of history.

Ten languages, one behaviour. A regression is not detected by pattern-matching a
language; it is a cycle in the module graph, computed by Tarjan's SCC over the
resolved import edges, at confidence `1.0`.

**Swift is deliberately absent**, and the reason is a property of the language rather
than a limit of enola. A Swift module is a *declared* SPM target, so two added
directories that import each other form no edge and no cycle, verified: the modules
and the symbols appear, the cycle does not. Injecting one would mean editing
`Package.swift`, and then "reverted" would be a file restore rather than a delete,
which is exactly the property that makes the no-change column worth reading.

### What is eligible to fail at all

Across the corpus enola produced **12,027 findings**. Broken down:

| Explainer | Findings | Class |
|---|---|---|
| dead-methods | 2,847 | candidate |
| god-class | 1,649 | statistical outlier |
| hotspots | 1,327 | statistical outlier |
| **cycles** | **1,227** | **structural fact + heuristic — see below** |
| exported-surface | 1,102 | candidate |
| layers | 1,078 | heuristic |
| complexity-outliers | 1,062 | statistical outlier |
| query-loops | 769 | heuristic |
| dependency-depth | 603 | statistical outlier |
| domain | 351 | heuristic |
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
ranked in a monolith's top 20 as "it calls out to 104 others". And `dead-methods` is the newest
explainer and immediately the largest row at 2,847 — every entry a candidate, never a
verdict, scoped to surfaces whose callers the graph can see.

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

Counting the corpus by confidence rather than by explainer: **1,301 of the 12,027
findings sit at the `certain` level**, the only one that reaches the `1.00` floor.
So **10.8% could fail a build even with every explainer named**, and the other 89.2%
are reported and let you through however you configure it.

That share was 3.16% at v197 and 14.3% one extractor version ago, and it moves in
both directions for the same reason: it is a denominator. Capping hotspots removed
23,194 findings that could never have failed anything, which raised it; `dead-methods`
then added 2,847 candidates, which lowered it again. **The count that can fail is
1,301 either way** — it has not moved. The ratio is the design — the confidence floor keeps an estimate from
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
(101,673 + 1,812 + 5,458 facts):

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
| Largest repository indexed | **Linux kernel** — 55,884 files, **1,908,738 facts**, 174.7s cold / 43.4s warm |
| Largest .NET | dotnet/runtime — 23,547 files, 529,820 facts, 75.7s / 42.2s |
| Largest Ruby | GitLab — 55,937 files, 544,435 facts, 51.9s / 33.0s |
| Largest Rust | rust-lang/rust — 37,565 files, 404,094 facts, 29.8s / 13.6s |
| Largest Scala | Spark — 5,745 files, 219,732 facts, 35.8s / 18.9s |
| Largest Go | Grafana — 11,344 files, 186,369 facts, 9.1s / 5.7s |
| Throughput | 1,000–36,900 facts/sec depending on language |
| Parse errors, all 91 repositories | **0** |
| Memory | peak heap per run is recorded by the sweep (`--memstats`) alongside time and hashes. The Linux kernel is the high-water mark at **6,196 MiB**; only five others exceed 1 GiB (GitLab 2,339, dotnet/runtime 1,866, roslyn 1,540, rust-lang/rust 1,303, dart-sdk 1,301). The largest Angular repository peaks at 395 MiB. No repository required tuning on this machine |

Warm runs are 1.10×–32.9× faster than cold (over the 75 repositories whose cold run
exceeds 0.5s; below that the timing is noise), from the per-file content-hash cache
in `snapshot.meta.json`. At v224 one row ran 0.94× — slower warm than cold — and this
sweep has none below 1.0×, which is a fact about this machine's run-to-run spread
rather than a fix anyone made. These numbers establish that the graph the other four
sections rely on can actually be built on real code. enola isn't benchmarked on
speed as a competitive claim.

## 5. What the extractors see

Across the corpus enola extracted **34,548 routes** (26,355 server, 8,193 client) and
recognised **49 distinct frameworks** without configuration:

```
rails 7963 · wordpress 6668 · axios 2575 · graphql 2244 · angular 2170
grape 1948 · openapi 1685 · nestjs 1569 · aspnetcore 1192 · play 923
request-options 802 · chi 751 · spring 578 · symfony 574 · resttemplate 423
fastapi 357 · flask 297 · fetch 278 · dart 173 · go_router 156
grpc 134 · axum 133 · vue 103 · auto_route 100 · utoipa 87
graphql-ruby 81 · net/http 63 · nuxt 55 · blazor 48 · openapi-fetch 46
guzzle 44 · navigator 43 · http-client 40 · nextjs 39 · httpclient 36
pekko-http 29 · net-http 24 · faraday 23 · http4s 21 · sveltekit 13
express 11 · client-seam 10 · file-get-contents 9 · hono 7 · gorilla/mux 5
retrofit 4 · django 4 · urlsession 2 · razorpages 2
```

**`angular 2170` is the fifth-largest framework and was zero.** It counts route facts
only — 612 page routes composed across files plus the client calls a component makes
— which is a small fraction of what the dialect reads: 12,174 classes carrying a
container role, 36,338 template references, 12,754 composition edges and 7,975
injection edges do not appear in a route tally at all. `axios` and `nestjs` grew for
the same reason the Angular rows exist: the corpus gained ten repositories, three of
which carry a TypeScript backend beside the Angular client.

**rails fell from 10,776 to 7,963, and the drop is the point.** The reader now honours
what a Rails route table actually declares: `resources :profiles, only: :show` is one
route, not seven, and a `scope constraints: { format: :html }` block is not a `/html`
path prefix. On rubygems.org that is 569 route facts down to 246 — the removed ones
include a `DELETE /api/v1/api_key` against a resource declared `only: %i[show create
update]`, and 200-odd paths that were reported one segment deeper than they are served.
A route count is not a score: an extractor that invents six verbs per resource scores
higher and is wrong more often.

The earlier growth still stands underneath it — routes used to be collected from the
root `config/routes.rb` alone, so every engine and plugin route file went unread, and
Grape had no extractor at all, leaving GitLab's entire v4 REST API invisible behind a
single `mount ::API::API` (`grape 1,948`).

Fact kinds: 5,655,046 symbols · 1,776,581 dependencies · 105,759 file refs ·
94,075 test refs · 78,389 modules · 34,548 routes · 6,569 associations ·
4,923 storage · 181 extraction · 6 intent. **`association` is new in 0.4.0** — a
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
the Django tag too. And **`utoipa 87` is a row that did not exist one
sweep ago**. Two Rust services in the corpus declare part of their API through
`utoipa_axum`'s `routes!()` macro, which registers a handler without repeating its
path: the path is written in a `#[utoipa::path]` attribute on the handler. Reading
that attribute takes **crates.io from 8 served routes to 74** — 57 distinct paths,
against the 91 client calls its own frontend makes — and cognee-rs from 77 to 98. The
macro is still not expanded; it does not have to be. See [rust.md](extraction/rust.md).

What that surfaced is worth more than the count. On crates.io the `domain` explainer
was reporting **19 components as calling outbound endpoints** — an inventory of
third-party integrations that do not exist, because those endpoints are served by the
Rust half of the same repository and enola could not see the routes. The explainer
checks for exactly this and excludes a call the graph shows as self-served; it had
nothing to check against. With the routes visible all 19 are gone, and on a cluster
containing crates.io the calls resolve: 2 of 91 call sites resolved before, 90 of 91
after. A missing extractor does not only subtract facts — it manufactures findings,
and they read exactly like real ones.

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
