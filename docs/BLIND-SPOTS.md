# Six blind spots in source browsing

## What this document is

A field record of six specific, reproducible failures, collected while building, measuring
and debugging enola against five public open-source codebases. Each one names the
repository, the pinned commit, the command that was run, and the answer that came back
wrong, so you can re-run it instead of taking it on trust.

The case for a code graph is often argued in the abstract - *agents need context* - or
with a benchmark score that says little about a particular codebase. This document starts
with what went wrong, why, and which failures a graph can address. The examples progress
from an avoidable truncated read to structural questions that source browsing alone does
not answer reliably:

| | |
|---|---|
| **1** | A truncated read that produced a confident, written, wrong recommendation |
| **2** | Three things a text search cannot find, because the string is not there |
| **3** | Why compiling it does not answer "what breaks" either |
| **4** | What changes when the codebase no longer fits in a context window |
| **5** | The question that is not retrieval at all: what did my change do? |
| **6** | A bug in enola found the same way - and why that is the argument, not a caveat |

This is not a benchmark ([BENCHMARKS.md](BENCHMARKS.md) is), not a feature tour
([README](../README.md) is), and not a sales document: one of the six failures is enola's
own, section 6 measures it, and the closing section lists what this evidence does *not*
support.

Read it if you are deciding whether a structural index earns its place in your agent
setup, or where source search starts to lose structural context.

## What the failures have in common

Every wrong answer in this document was produced by a capable agent reading real data
correctly. None of them is a hallucination. They are all the same failure: **a bounded,
confident read of a partial state.**

Better search does not remove that boundary. A scoped structural model makes the measured
relationships queryable and comparable, while its receipt records exclusions and known
limits.

Everything below was measured while building and fixing enola, mostly in a single working
session. Where enola itself was wrong, that is here too - it is the most useful part.

## The repositories

Public open-source only, pinned to a commit, cloned as siblings. Every one is also a row in
[BENCHMARKS.md](BENCHMARKS.md)'s corpus, so its parse coverage and fact counts are already
published rather than asserted here.

| Repository | Language | Pinned at | What it was used for |
|---|---|---|---|
| [gitea](https://github.com/go-gitea/gitea) | Go | `38b07d0c295f` (2026-07-31) | §1's data, §2's unqualified call and interface embedding, §3's compiler waves |
| [excalidraw](https://github.com/excalidraw/excalidraw) | TypeScript | `1acf66edabc2` (2026-07-28) | §6's alias bug - found, quantified and fixed here |
| [ente](https://github.com/ente-io/ente) | Dart + Go | `fba00000a07c` (2026-08-07) | a Flutter client and its own gin backend, for the cross-repo questions |
| [supabase](https://github.com/supabase/supabase) | TypeScript | `67c983caefb7` (2026-07-29) | §6 control - a pnpm monorepo with **no** tsconfig `paths`, so the bug could not apply |
| [bitwarden/clients](https://github.com/bitwarden/clients) | TypeScript | `454d56be335e` (2026-08-06) | §6 scale check - 49 exact alias entries, the largest affected set found |

Three of the five were chosen for a property rather than for size. **excalidraw** declares
its `paths` aliases in two tsconfigs at different depths, which is what exposed the bug.
**supabase** declares none at all, which is what proved the diagnosis rather than a
coincidence - the fix changed its numbers by one edge out of 110,524. **ente** keeps a
mobile client and the server it talks to in one tree, so a cross-repo question can be asked
against code nobody wrote for this document.

Note what is *not* here: no repository enola was developed against, and no fixture built for
the occasion. The one deliberately constructed thing in this document is the two-service
demo in [`examples/cross-repo/`](../examples/cross-repo/), which §2 cites for the composed
route path and which is a runnable example rather than evidence.

## How this was tested

Some of what follows came from ordinary work on the repository. The rest came from
**510 headless agent sessions** across three runs, driven by a script that lives beside
this project's other benchmark harnesses.

| | |
|---|---|
| Harness | Claude Code 2.1.205, `claude -p`, `--output-format stream-json` |
| Session isolation | one fresh session per trial; execution order a seeded shuffle so provider-time effects cannot align with a condition |
| Answer keys | built and frozen **before** any session ran, each carrying its own oracle - `gopls`, `tsc`, `go build`, `go list` - never enola |
| Writes | `Edit`/`Write` denied; every repository checked for modification after every session |
| Recording | one row appended per session as it finished, so a rate limit costs one session rather than a run |

The model was **not pinned** - sessions used the CLI default. That is a real limitation:
these results characterise one harness at one point in time, not agents in general.

### Six ways the harness lied to us

Measuring an agent honestly turned out to be harder than the questions being asked, and
every one of these produced a wrong number before it was caught. If you run your own
evaluation, expect them:

**Global MCP servers leak into your control group.** Claude Code loads the user's
configured MCP servers by default. The "no tools" baseline silently inherited a running
enola server and stopped being a baseline. `--strict-mcp-config` with an explicit empty
config is what makes a control group a control group.

**The delivery channel is part of the experiment, and the obvious one is wrong.**
`--append-system-prompt` is *not* how enola ships instructions: `enola install` writes
`CLAUDE.md`, the repo-root `AGENTS.md` and `.claude/rules/enola.md`. Text delivered through
the wrong channel behaves differently, so an entire run measured a mechanism the product
does not use. Verify the channel is live before trusting a null result - drop a canary rule
into it (*"begin every reply with PINEAPPLE"*) and check the reply.

**The harness's own system prompt outranks your instructions.** With the real installer
run, the hook firing, and the instruction placed directly in the user's message, agents
called an enola tool in **3 of 120 sessions**. Claude Code's own guidance governs tool
selection, so a null adoption result inside Claude Code is a fact about Claude Code - not
about agents, and not about the tool. Separating those needs a second harness with no
system prompt of its own, which we did not have credentials to run.

**Subagents do not inherit tool restrictions.** `--disallowedTools` binds the session that
declares it. An agent spawned through `Agent`, `Workflow`, `Skill` or `ToolSearch` starts
clean and can use everything you just forbade. Sessions escaped a tool restriction this way
in two separate runs before the deny list was closed.

**`git status` cannot see side effects in ignored paths.** gitea gitignores `/.claude/` and
`/.cursor/`, which is exactly where an agent-tooling installer writes. A cleanliness check
built on `git status` reported all-clear over real residue. Snapshot the paths before, and
compare after, rather than asking git.

**Build oracles fail silently in the direction of success.** gitea will not resolve its
dependencies under `GOPROXY=direct`, so `go build ./...` died at module resolution *before*
type-checking - producing an empty error list that looks exactly like a clean build. Any
oracle whose failure mode is "no output" needs a positive check that it ran.

### What these runs were not good for

The three runs were designed to compare answer accuracy with and without a graph. They
could not: nearly every question had an answer that was a string present in a single
repository, which is a shape text search handles well. That comparison is not reported
here, and the sections below use those runs only for the specific defects they exposed -
each independently re-verified by hand against a compiler or language server.

The reason is worth stating, because it is a trap in evaluation design generally:
**insisting on a mechanical oracle selects for the questions a text search can answer.**
`gopls`, `tsc` and `go list` answer statically resolvable, single-repository questions.
Build a benchmark around what they can grade and you will have built a retrieval benchmark
by accident, then measured that grep is good at retrieval.

---

## 1. It starts with a truncated read

While reconciling stale numbers in enola's own documentation, the agent doing the work ran

```
head -60 out/sweep/results.jsonl | python3 -m json.tool
```

saw the keys `lang`, `name`, `path`, `runs`, and concluded the benchmark harness did not
record per-explainer finding counts. It then recommended patching the harness to add them.

The counts were in the same JSON object, about twenty lines past where the output was cut.
The script had recorded them all along.

Read that back slowly, because every property matters:

- **Nothing was invented.** Every line the agent read was real and correctly interpreted.
- **The tool was right for the job.** You do not pipe a 175 KB JSONL file into a terminal.
  Bounded reads are not a bad habit; they are the only way to work.
- **More reasoning would not have helped.** The conclusion followed validly from what was
  visible. It was caught by going back and looking at the whole object.
- **The error was confident.** It reached a written recommendation before anyone doubted it.

An architectural claim derived this way is wrong in a way that reads exactly like being
right. Repeating the question does not help either: a second run truncates somewhere else
and may well answer correctly, which tells you nothing about the first.

## 2. Three things a text search cannot see

The truncation above is an accident. The next three are structural - no pattern, no
budget, and no amount of care will find them, because the string you would search for
does not exist in the file.

### A call that is not package-qualified

*Which files call `typesniffer.DetectContentType`?* On gitea, the obvious command is right:

```
grep -rln "typesniffer\.DetectContentType(" --include="*.go" .
```

Eleven files. The true answer is twelve. The missing one is
`modules/typesniffer/typesniffer_test.go`, which calls the function **45 times** -
unqualified, as `DetectContentType(...)`, because Go does not package-qualify a name
inside its own package.

`gopls` resolves all 45. No refinement of the pattern reaches them, because
`typesniffer.DetectContentType` never appears in that file. The blind spot is a property
of the language, and it costs the same recall every single time.

### An interface satisfied by embedding

*Which types implement `PasswordSaltHasher`?* Grepping the interface name finds only its
own declaration - Go's interface satisfaction is implicit, so nothing textually links an
implementer to the interface it implements.

Grepping the method name instead, `HashWithSaltBytes`, finds six implementers. `gopls
implementation` finds **seven**. The extra one is `PasswordHashAlgorithm`, which satisfies
the interface by **embedding** it, and therefore declares no method to search for.

Six of seven, with no signal that anything is missing.

### A route that lives at a path written nowhere

A handler registered as `HandleFunc("/orders/{id}", …)` inside a function that receives a
subrouter mounted at `PathPrefix("/api/v2")` in `main` does not serve `/orders/{id}`. It
serves `/api/v2/orders/{id}`.

A client calling that endpoint has the composed path in its source. The server has the two
halves in two different files. **Neither file contains the string that connects them**, so
no text search relates the call to the route it hits. This is what
[`examples/cross-repo/`](../examples/cross-repo/) demonstrates in one command.

## 3. The compiler is not a graph either

The obvious answer to "what breaks if I change this" is: change it and compile.

Adding a method to that same `PasswordSaltHasher` interface and running `go build ./...`
reports four files. That looks definitive. It is wave one.

A package that fails to compile **masks its own dependents** - they are never type-checked.
Fixing the four and rebuilding surfaces `modules/setting/testenv.go`. Running `go vet`,
which type-checks tests, adds `dummy_test.go`. A third call site in
`models/unittest/fixtures.go` is still masked behind that.

So a single compiler pass answers a *different question* than the one asked: not "what
must change" but "what fails first". Getting the real blast radius means iterating to a
fixed point, and knowing that you have to.

This one cost us directly. An answer key built from one `go build` pass graded agents at
0.80 for giving a **more correct** answer than the key.

## 4. Then multiply it by a repository count

Everything above happens inside one tree, where the whole corpus is at least reachable.
Multi-repo removes even that.

An eight-repository product ecosystem in enola's own measurements is **8.45M tokens** of
parsed source; GitLab is 32.77M; the Linux kernel is 218.15M, which is
[218× a single context window](../ARCHITECTURE.md#tokens). A cross-repo edge - an iOS
client calling a route its backend serves - can only be derived with both sides resident
at once. Past that threshold the counterfactual is not expensive, it is **unavailable**:
no patience or budget gets an agent there by re-reading files, because it cannot hold the
two sides of the comparison in the same context.

And the composed-prefix problem from §2 is now the normal case rather than the exception,
in a codebase where the two halves are in different repositories.

## 5. The part that is not retrieval at all

Sections 1–4 are about finding things. The question enola exists for is not a search
question, and this is the distinction that matters most:

> You cannot compute it from the current structure of the code, however accurate that
> structure is, because "your change made this worse" is not a property of the code in
> front of you - it is a relation between the code in front of you and the code before
> you started.
>
> - [SNAPSHOTS.md](SNAPSHOTS.md)

After an edit lands, the *before* state does not exist anywhere. Not in the working tree,
not in the model's context, not in git - git records line diffs, and "these two modules now
form a cycle" was never a line in any file. It is a relation between two graphs, one of
which is gone unless something kept it.

This is why the ratchet is the product rather than a feature of it. Across fifteen
repositories carrying **1,228 pre-existing findings**, an injected cycle is reported as
exactly one regression and none of the 1,228 is repeated
([BENCHMARKS.md § 2](BENCHMARKS.md#2-delta-precision--the-ratchet)). Without a kept
baseline there is no "new" - every run re-reports the whole pile, and a tool that does
that gets switched off in a week.

## 6. We found a bug in enola this way, and that is the argument

While measuring, a query that should have returned every caller of an exported TypeScript
function returned nothing. The graph was wrong.

`tryParseTSConfigAliases` recorded a tsconfig `paths` entry only when **both** the pattern
and the target ended in `*`. Exact mappings were silently dropped - and the exact form is
how a monorepo names a sibling package:

```jsonc
"@excalidraw/common":   ["./common/src/index.ts"],   // dropped
"@excalidraw/common/*": ["./common/src/*"]           // kept
```

So `import { randomId } from "@excalidraw/common"` matched nothing, was classified
external, and its call edge fell back to the **caller's** directory. One phantom node per
calling package, none of them the real symbol, and `impact_analysis` and `traverse`
therefore blind to every cross-package symbol.

Measured on excalidraw: 22,348 TypeScript call edges, of which **4,847 dangled although
the callee was declared in the repo**. The control that proved the diagnosis: 93 files
import `@excalidraw/math` bare and their edges dangled, while the 4 files using the
subpath form resolved correctly. Same repo, same symbol, same extractor - only the
specifier shape differed.

After the fix, all 24 `randomId` call edges point at one canonical node, and the 14 caller
files match what `tsc` reports **exactly** - nothing missing, nothing extra.

Two things about that are worth more than the fix:

**It was invisible to a 72-repository benchmark.** The bug only appears in a monorepo
using the dominant `@scope/pkg` convention. Every reproducibility, scale and delta-precision
number in [BENCHMARKS.md](BENCHMARKS.md) stayed green while this was broken.

**It was catchable because the graph makes a falsifiable claim.** A deterministic artifact
can be checked against a compiler, disagree with it, and be *proven* wrong. A mental model
assembled from greps cannot be audited, because it was never written down - it produces
the same confident eleven-of-twelve and nothing contradicts it.

## What this does not claim

- **Not that agents are careless.** Every failure here came from a correct read of real
  data by a capable model.
- **Not that enola finds things faster.** On a well-specified lookup in a single repository
  - an exact identifier, a distinctive import path - a good agent with grep does fine, and
  measurement bears that out. Retrieval speed is
  [deliberately absent](BENCHMARKS.md#what-this-does-not-measure) from enola's benchmarks.
- **Not that enola replaces anything the agent already does.** It reads code, edits code
  and runs the build exactly as before. enola adds the structure beforehand and the graded
  delta afterwards.
- **Not that enola is always right.** Section 6 is a bug we shipped. The claim is that a
  deterministic, addressable artifact can be shown to be wrong, and then fixed - which is
  more than can be said for the alternative.
