# enola — command-line reference

Every command, flag and setup step. For what enola is and why you would run it, see
[the README](../README.md); for how the engine works, see [ARCHITECTURE.md](../ARCHITECTURE.md).

---

## Quick start

### Install

Grab a prebuilt binary - no Go toolchain or C compiler required:

```bash
curl -fsSL https://raw.githubusercontent.com/enola-labs/enola/main/install.sh | sh
```

This installs `enola` to `~/.local/bin`. If that's not on your `PATH`, add it:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Binaries are published for Linux, macOS (amd64/arm64), and Windows (amd64). You can also download a specific build from the [Releases page](https://github.com/enola-labs/enola/releases), or [build from source](#build-from-source).

### Upgrade

Once installed, update to the latest release in place:

```bash
enola upgrade
```

This downloads the newest build for your platform, verifies its checksum, and replaces the running binary. If enola is installed somewhere your user can't write, re-run with elevated permissions or re-run the install script above.

Because your agent launches enola as a long-lived MCP server process, an upgrade only takes effect once that process restarts - reconnect the MCP server so it picks up the new binary:

- **Claude Code** - restart the session, or re-register with `claude mcp remove enola && claude mcp add enola enola`.
- **Cursor** - toggle the enola server off and back on in **Settings → MCP** (or reload the window).
- **GitHub Copilot (VS Code)** - restart the server from the `.vscode/mcp.json` editor (the **Restart** CodeLens above the server entry), or reload the window.

### Configuration (optional)

**enola needs no config file.** Every setting has a built-in default, so out of the box it indexes the current repo with all extractors enabled and writes to `.enola/`. A config file (`mcp-arch.yaml`) only *overrides* those defaults - it never adds capability you'd otherwise lack.

Every command prints the config it resolved, on stderr, before it does anything:

```
enola: using config /Users/you/src/api/mcp-arch.yaml
enola: no mcp-arch.yaml in /Users/you/src/api, using built-in defaults
```

It is worth reading. A config decides which extractors run and which paths are ignored, so the wrong one does not fail - it analyses something other than what you asked for. enola looks in the working directory, then (only for a binary that is *not* on your `PATH`, i.e. an unpacked bundle rather than an installed one) beside the executable; the second case says so explicitly.

Note that a list-valued setting **replaces** its default rather than extending it. That is why the bundled `mcp-arch.yaml` declares no `extractors:`, `explainers:` or `renderers:` - a copied list silently falls behind as new ones ship. Set `extractors:` only to deliberately narrow a run; enola warns when an extractor you excluded would have detected the repository.

The install script installs **only the binary**, by design - it does not place a config file. Grab the bundled one from the repo whenever you want to customize (tune the `ignore` globs, pick a subset of extractors, change the output dir, …):

```bash
curl -fsSL https://raw.githubusercontent.com/enola-labs/enola/main/mcp-arch.yaml -o mcp-arch.yaml
```

The [`examples/`](../examples/) directory has ready-made per-language and multi-repo starting points, and [`examples/full.yaml`](../examples/full.yaml) documents every option. For the full field reference and defaults, see **[ARCHITECTURE.md → Configuration](../ARCHITECTURE.md#configuration)**.

### Connect it to your agent

**Claude Code** - register enola as an MCP server with one command. This assumes the `enola` binary is on your `PATH` (the install script above puts it in `~/.local/bin`):

```bash
claude mcp add enola enola
```

The shape is `claude mcp add <name> <command> [args…]`: the first `enola` names the server, the second is the binary. The trailing config path is **optional** - omit it (as above) to run on built-in defaults, or pass one to override them:

```bash
claude mcp add enola enola /path/to/enola/mcp-arch.yaml
```

When you do pass a config, its `repo:` is only the *default* repository - you can still snapshot any repo by passing `repo_path` to `generate_snapshot`. Verify it registered with `claude mcp list`, then start Claude Code and ask it to generate a snapshot.

**Cursor / other MCP clients** - add enola to your client's MCP configuration. For example, in Cursor's `mcp.json` (the config path in `args` is optional - drop it to use defaults):

```json
{
  "mcpServers": {
    "enola": {
      "command": "enola",
      "args": ["/path/to/enola/mcp-arch.yaml"]
    }
  }
}
```

**GitHub Copilot (VS Code)** - add enola to `.vscode/mcp.json` in your workspace (or your user-level MCP config via **MCP: Open User Configuration**). Note the top-level key is `servers` (not `mcpServers`), and the config path in `args` is optional - drop it to use defaults:

```json
{
  "servers": {
    "enola": {
      "command": "enola",
      "args": ["/path/to/enola/mcp-arch.yaml"]
    }
  }
}
```

Or add it from the command line: `code --add-mcp "{\"name\":\"enola\",\"command\":\"enola\"}"`. Then open a project and ask Copilot to generate a snapshot.

### Use it

Everything below is a prompt you type at your agent in plain English. enola picks the tool.

#### 1. Map it

> "Generate an architectural snapshot of /path/to/my/project"

That's the whole setup. Snapshots are fast - seconds even on very large polyglot repos - and your agent now has all 13 tools plus a ready-to-read summary at `.enola/llm_context.md`.

#### 2. Understand it

> "I just joined this project - based on the snapshot, give me a tour: the main modules, how they relate, and where to start reading."

> "Draw me a mermaid diagram of the module dependencies from the snapshot."

> "Where are the architectural risks - dependency cycles, layer violations, god classes with high fan-in, call-graph hotspots, complexity outliers, or modules buried deep in the dependency chain?"

> "Which modules have the largest public surface? We're trying to tighten up what's exported."

#### 3. Plan the change

> "I need to add an API endpoint for user preferences. Which packages should I touch, and in what order?"

> "What would break if I refactor `internal/server`? Show me the impact analysis."

> "How does the HTTP handler layer reach the database layer? Show me the shortest path."

#### 4. Make the change - and verify it

This is the loop that keeps an agent honest, and it's worth building the habit:

> "Pin the current architecture as a baseline before we start."

> *…now let the agent do the work…*

> "Re-snapshot and show me the architecture diff against the baseline. Did we introduce any coupling or cycles we didn't intend?"

If the diff shows a regression, hand it straight back:

> "You introduced a cycle between `internal/auth` and `internal/session`. Refactor to remove it, then diff again."

Two prompts, no file re-reading, no review meeting. Repeat until the diff is boring.

**The same loop without an agent.** Everything above is also a shell command, so the check can run in a git hook or CI instead of depending on the agent remembering to ask:

```bash
enola baseline pin      # before editing
enola check             # after - exits 1 on a structural regression
```

See [The gate - `enola check`](#the-gate---enola-check).

#### 5. Go multi-repo

Generate the first repo, then add the rest with append mode - enola links them into one cross-repo graph:

> "Generate a snapshot of /path/to/go-service with append mode"

> "If I change the auth service, which other services are impacted?"

> "Trace the login flow from the web client through to the backend route that serves it."

> "Which of my backend's endpoints aren't called by any of the client apps?" *(cleanup candidates - check for callers outside these repos first: cron, webhooks, third-party clients)*

> "Which cross-repo calls did enola fail to resolve? I want to know where the map has blind spots."

When you snapshot a *different* repo without `append`, enola assumes you're extending the set and auto-appends it - handy when you forgot `append` on repo #2. If you've actually **moved to another project** and want a clean single-repo snapshot instead, ask for a fresh one (`fresh=true`) so the old repos are discarded rather than merged in.

#### 6. Look at it yourself - without spending a token

Some questions don't need an agent at all. The MCP server also serves a **read-only dashboard** on localhost (URL printed at startup, or run `enola --status`), and it already answers, on one page:

- *What is in this graph right now?* - the repos loaded, services, cross-repo edges (with a node-link diagram), fact and insight counts.
- *What did the analysis find?* - every insight grouped by explainer and filterable by confidence, so you can see the cycles and hotspots without asking a model to list them.
- *Is this snapshot trustworthy?* - the receipt: snapshot ID, enola version, git ref and dirty flag, extractors used.
- *Why does this snapshot look thin?* - extraction quality: files seen vs. parsed vs. skipped, parse errors with samples, unresolved cross-repo edges, coverage gaps.
- *What has this actually saved me?* - the same value estimate `--status` prints, per tool and lifetime ([how it's calculated](../ARCHITECTURE.md#the-value-model)).

Reading it costs nothing and burns no context. It's also the fastest way to sanity-check a snapshot before you trust an answer built on it.

#### 7. Especially good with local and smaller models

If you run a local LLM - Ollama, LM Studio, an on-prem endpoint, or a smaller hosted model - enola is not a nice-to-have, it's the difference between usable and not. A smaller model's weakness usually isn't writing code; it's holding a large repository in its head and doing multi-hop structural reasoning over it. enola does that part deterministically and hands over the answer:

- **Context stays small.** Instead of stuffing 40 files into a short context window hoping the dependency is in there, the model gets the exact dependent set - a handful of names, precisely scoped.
- **No long inference chains to get wrong.** "What depends on this, transitively, across three repos" is a graph traversal, not a reasoning task. The model never attempts it, so it never fluffs it.
- **Fewer round trips.** Every avoided grep-open-read cycle is a full local inference pass you don't wait for. On local hardware that's wall-clock time, not just tokens.
- **Nothing leaves your machine.** enola is a local binary, the graph is a local file, the dashboard binds loopback only. A fully offline architecture-intelligence stack.

#### Keeping it current

**Regenerate after major changes** so the snapshot stays current. Refreshes are fast: enola caches each language's facts and re-parses a language only when one of its files (or a shared config like `package.json`) actually changed, reusing the rest. If a snapshot does go stale, enola tells your agent so on every tool call - a warning, never a block.

> **Very large repositories (e.g. the Linux kernel).** The first, cold index of a huge repo can take a minute or more and may exceed your MCP client's per-tool-call timeout, surfacing as `MCP error -32001: Request timed out`. The snapshot usually still finishes and is cached server-side - but to avoid the error, either:
> - **Raise your MCP client's tool-call timeout.** In Claude Code, set the `MCP_TOOL_TIMEOUT` environment variable (milliseconds) before launching, e.g. `MCP_TOOL_TIMEOUT=600000`.
> - **Pre-generate from the shell once**, then start the server: run `enola --generate <config-pointing-at-the-repo>` (writes `.enola/`), after which the MCP server auto-loads the cached snapshot on startup and later `generate_snapshot` calls reuse the extractor cache (only changed files are re-parsed), so they return quickly.

---

---

## Explain a repository at a glance

**Point it at any repository - yours or one you've never seen - and get its architecture on one screen, in seconds, with no AI, no API key, and no account.**

`enola --explain [repo_path]` is a one-shot mode that generates a snapshot, computes statistics over the fact graph, and prints a human-readable report to stdout - no MCP server started, no artifacts written to `.enola/`, nothing sent anywhere. It is the fastest honest answer to "what *is* this codebase?"

**When to use it:**
- **Onboarding onto an unfamiliar codebase** - module count, architecture pattern, hottest packages, where the complexity lives. Ten seconds instead of a week of reading.
- **Evaluating code you didn't write** - a dependency, an acquisition, an open-source project, a contractor's delivery. Cycles, coupling and complexity are hard to hide from a graph.
- **Pre-refactor sanity check** - cycles, layer violations, blast radius of the top modules, before you commit to a plan.
- **CI and audits** - plain text, no color codes, safe to pipe or capture.

```bash
# Use the config in the current directory (mcp-arch.yaml)
enola --explain

# Analyze a specific repository path
enola --explain /path/to/repo

# Report over a whole cluster, from a config that names it with `repos:`
enola --explain ci/cluster.yaml
```

The argument is a **repository** when it is a directory and a **config file** when it is a file, so both forms work without a flag to tell them apart.

**The report covers nine sections:**
- **Overview** - path, analysis time, active languages, total fact count
- **Architectural kinds** - counts of modules, symbols, routes, storage, dependencies, services
- **Relations** - the edge census: declares, imports, calls, implements, instantiates, injects, has_method
- **Symbol breakdown** - functions, methods, structs, interfaces, and other kinds
- **API & data surface** - route count broken down by HTTP method, plus storage count
- **Dependencies** - external, internal, and stdlib import counts
- **Architecture** - detected pattern with confidence, cyclic dependencies, layer violations, cross-repo edges
- **Impact analysis (hotspots)** - top modules ranked by fan-in + fan-out coupling, with criticality tier and blast radius
- **Code health** - per-explainer findings with their top offenders: god classes (high fan-in symbols), call-graph hotspots, deep dependency chains, large public surfaces, and complexity outliers

Every finding carries a confidence score, and it means something exact: `1.0` is a structural fact — in practice a dependency cycle, the one thing enola computes rather than infers — while anything below is a flagged heuristic for you to review (a god class is a statistical fan-in outlier, not a rule, so it tops out below `1.0` however extreme it gets). The analyses are computed by graph algorithms - Tarjan's SCC, which finds groups of modules that can all reach each other, for cycles; longest-path for the deepest import chain; and mean+2σ outlier tests, which flag what sits two standard deviations above your own repository's average, for the rest - so the same commit yields the same report. The vocabulary here is defined in **[docs/GLOSSARY.md](GLOSSARY.md)**.

Here's the actual report for [Apache Airflow](https://github.com/apache/airflow) - a large polyglot codebase (Python, TypeScript, Java and gRPC in one tree) analyzed in a single pass: **136,859 facts, 510,000+ resolved edges, in about 6 seconds** on a laptop (extraction parses files in parallel across cores). Nothing here was written by a model.

```
════════════════════════════════════════════════════════════
 Repository explanation: /path/to/airflow
════════════════════════════════════════════════════════════

Overview
  Generated:           2026-07-25T20:22:17Z
  Analysis time:       5.845487583s
  Languages:           python, typescript, java, grpc
  Total facts:         136859

Architectural kinds
  module                   2563
  symbol                  68496
  route                    6876
  storage                    64
  dependency              53765

Relations
  declares                68764
  imports                 53765
  calls                  200204
  implements               4909
  instantiates           183505
  injects                     1
  has_method                  2

Symbol breakdown
  method                  43730
  function                13384
  class                    9047
  type                     1454
  variable                  801
  interface                  60
  struct                     10
  enum                        6
  constant                    4

API & data surface
  routes                   6876
    PATCH                  6475
    GET                     258
    POST                     76
    DELETE                   46
    PUT                      20
    HEAD                      1
  storage                    64

Dependencies
  internal                27383
  stdlib                  13952
  external                12429
  unclassified                1

Architecture
  Pattern:             (none detected)
  cyclic dependencies        24
  layer violations            0

Impact analysis (hotspots)
  coupled modules          1024
    high criticality        632
    medium criticality      392
  Top hotspots (by coupling):
    module                            fan-in  fan-out crit     blast radius
    airflow-core/src/airflow/models     2257      507 high     1452
    devel-common/src/tests_common/t…    1592      266 high     698
    airflow-core/src/airflow/utils      1588      201 high     1429
    providers/common/compat/src/air…    1708       26 high     1301
    task-sdk/src/airflow/sdk            1554       73 high     1452
    airflow-core/src/airflow            1416      112 high     1454
    providers/common/compat/tests/u…    1100        0 high     1423
    providers/amazon/tests/system/a…       1      879 high     1

Code health
  god classes (high fan-in)     25
    dev/breeze/src/airflow_breeze/utils/console… 407 dependents
    airflow-core/src/airflow/utils/session.crea… 382 dependents
    providers/amazon/src/airflow/providers/amaz… 225 dependents
    dev/breeze/src/airflow_breeze/utils/run_uti… 200 dependents
    airflow-core/src/airflow/utils/helpers.prun… 181 dependents
  call-graph hotspots       132
    task-sdk/src/airflow/sdk/execution_time/tas… fan-in 118 / out 37
    providers/hashicorp/src/airflow/providers/h… fan-in 79 / out 42
    providers/fab/src/airflow/providers/fab/aut… fan-in 28 / out 103
    airflow-core/src/airflow/providers_manager.… fan-in 13 / out 176
    airflow-core/src/airflow/utils/session.crea… fan-in 382 / out 5
  deep dependency chains     10
    providers/common/ai/src/airflow/providers/c… depth 8
    providers/common/ai/src/airflow/providers/c… depth 8
    airflow-core/src/airflow/ui/src/components/… depth 7
    providers/airbyte/docs                       depth 7
    providers/akeyless/docs                      depth 7
  large public surfaces      20
    task-sdk/src/airflow/sdk/execution_time/com… 120/133 (90%)
    task-sdk/src/airflow/sdk/definitions/mapped… 100/111 (90%)
    airflow-ctl/src/airflowctl/api/operations    92/98 (94%)
    providers/google/src/airflow/providers/goog… 67/72 (93%)
    dev/breeze/src/airflow_breeze/utils/packages 62/68 (91%)
  complexity outliers        15
    airflow-core/src/airflow/jobs/scheduler_job… complexity 72
    task-sdk/src/airflow/sdk/execution_time/sup… complexity 63
    airflow-core/src/airflow/ui/src/pages/DagsL… complexity 55
    airflow-core/src/airflow/ui/src/hooks.useDa… complexity 53
    dev/breeze/src/airflow_breeze/commands/ci_c… complexity 52
```

No artifacts are written; `.enola/` is not touched. For a persistent snapshot with agent-readable output, use `--generate` or the MCP server.

For interactive per-module blast-radius queries with configurable depth, see the `impact_analysis` tool reference in **[ARCHITECTURE.md → The tools](../ARCHITECTURE.md#the-tools)**.

---

---

## Command-line reference

Run `enola --help` for the full text. With no flags, enola starts the MCP server on stdio.

Every path argument follows the same rule: **a directory is a repository, a file is a config.** Anything that is neither is rejected rather than silently ignored.

| Command | What it does |
|------|--------------|
| `install [--hooks] [--global]` | **Tell your coding agents enola is here.** Writes its instructions into the files they actually read - `.claude/rules/enola.md`, `.cursor/rules/enola.mdc`, and a marked block in `AGENTS.md` if you have one. Previews every change and asks before writing. See [Wiring it into your agents](#wiring-it-into-your-agents---enola-install). |
| `coverage [--repo=<svc>] [--unresolved] [--json]` | **Which cross-repo edges enola resolved, and which it did not** — per service, so you can tell a genuinely isolated service from one whose outbound edges enola could not follow. The unresolved list is always shown: it is what makes the resolved count worth believing, and each entry is either a repository you have not loaded, a third-party endpoint, or a blind spot in enola. Needs two or more repositories in one graph. A report, not a gate — it always exits `0`. |
| `doctor [repo]` | **Are the session hooks actually firing?** `install --hooks` writes a hook configuration and reports success — but whether your agent honours that configuration is a contract owned by the agent, not by enola, and a config it ignores looks identical to one it runs. So the hooks record every time they fire, *including* the runs where they deliberately say nothing, and this reports when each last ran, what it concluded, and whether the pinned baseline can still be graded against at all. `NEVER FIRED` after a real session means the wiring is not working. A report, not a gate — it always exits `0`. |
| `uninstall [--global]` | Remove everything `install` wrote, leaving the rest of each file byte-for-byte as it was. |
| `baseline pin\|show\|clear [repo\|config]` | Manage the diff baseline - the "before" a change is graded against. `pin` snapshots the repository and freezes it (no separate `--generate` needed); `show` reports what the current baseline describes; `clear` removes it. Stored per repository, in that repo's `.enola/baseline`, so several repos each keep their own. |
| `check [flags] [repo\|config]` | **Grade what a change did to the architecture**, and exit with a code CI can act on. Read-only: writes nothing and leaves the baseline in place, so it can be run repeatedly. See [The gate](#the-gate---enola-check). |
| `upgrade` | Download and install the latest release over the running binary. |

| Flag | What it does |
|------|--------------|
| `--generate [repo_path\|config_path]` | Generate a snapshot and exit - no MCP server. Artifacts go to `output.dir` (default `.enola/`). With `repos:` in the config, indexes the whole cluster in one run. |
| `--explain [repo_path\|config_path]` | Print the statistics report above and exit. Read-only: nothing is written to `.enola/`. A directory is a repository; a file is a config, so a `repos:` config reports over the whole cluster. |
| `--list` | List the MCP tools this build serves, with one-line summaries. |
| `--status` | List every enola server running right now - PID, repos, uptime, calls, dashboard URL - plus per-tool call counts and an estimate of the reconstruction those calls saved, in time and tokens. |
| `--status --all` | The same usage, broken down per repository. |
| `--no-dashboard` | Start the MCP server without the localhost dashboard. |
| `--version` | Print the build version. |
| `--help`, `-h` | Show usage. |

### Wiring it into your agents - `enola install`

An MCP server your agent forgets to use is a tool you don't have. `enola install` writes a short instruction into the files your agents already read, so they know the graph is there and what it's for:

```bash
enola install                 # this repository (shared with the team via source control)
enola install --global        # this user, every project
enola install --dry-run       # show what would change, write nothing
enola install --yes           # skip the confirmation prompt (for scripts)
enola install --targets=claude,cursor   # a subset instead of every detected agent
enola uninstall               # remove it all again
```

| Target | Local (this repo) | Global (`--global`) |
|---|---|---|
| Claude Code | `.claude/rules/enola.md` *(owned)* | `~/.claude/rules/enola.md` *(owned)* |
| Cursor | `.cursor/rules/enola.mdc` *(owned)* | — no user-level rules directory |
| GitHub Copilot | `.github/instructions/enola.instructions.md` *(owned)* | — lives in IDE/account settings |
| Codex · Copilot · Pi | `AGENTS.md` *(marked block, only if it already exists)* | — |
| Codex | *covered by `AGENTS.md`* | `~/.codex/AGENTS.md` *(marked block)* |
| Pi | *covered by `AGENTS.md`* | `~/.pi/agent/AGENTS.md` *(marked block)* |

**Codex, Copilot and Pi all read the repository's `AGENTS.md`**, so locally one block serves all three - enola won't write a second repo-local file for them, which would only put the same instruction into the same context window twice. Their `--global` entries add what `AGENTS.md` can't: guidance in projects where nobody has run `enola install`. Those are written only when the tool's config directory already exists, so enola never creates `~/.codex` for someone who doesn't use Codex.

Restrict the run with `--targets=claude,copilot` if you only want some.

**It never surprises you.** Every run previews what it will touch and asks before writing. It never creates an `AGENTS.md` that wasn't already there. Re-running reports `unchanged` rather than churning files. And `uninstall` restores shared files byte-for-byte - the block is delimited by explicit `<!-- enola:begin -->` / `<!-- enola:end -->` markers, and if those markers have been hand-edited into an unbalanced state, enola refuses to write rather than guess where its section ends.

#### Closing the loop automatically - `--hooks`

```bash
enola install --hooks
```

This installs both halves of the loop, so it runs without anyone remembering to:

- **`SessionStart`** freezes the architecture as a baseline when a session begins - the "before".
- **`Stop`** grades what the session changed when your agent finishes a turn, and hands the verdict back **only if** the change introduced a structural regression.

The agent gets a chance to fix a dependency cycle before telling you it's done, rather than you finding it in review.

It is deliberately opt-in and deliberately quiet:

- **Session start is never delayed.** The baseline snapshot runs detached, so the hook returns in milliseconds whether your repo takes 0.2 seconds or two minutes to index. A timeout would only *cap* that cost; detaching removes it.
- **Your own baseline is never replaced.** A baseline you pinned yourself - or that your agent pinned with `set_baseline` - is left alone. Only one enola pinned automatically is refreshed, and only when the tree has actually moved.
- **Several open terminals do one snapshot, not six.** The pin is single-flight across processes; sessions that arrive while one is running do nothing.
- **Silent unless it matters.** No baseline, nothing changed, a clean result, an incomparable baseline - all produce no output at all. The gate speaks only when the change actually regressed the architecture.
- **It never blocks.** The verdict is context the agent can act on, not a wall it has to get past.
- **It never breaks your session.** Every failure path - no snapshot, unreadable input, a directory that isn't a repository - exits cleanly and says nothing. A broken enola must never look like a broken session.
- **It merges into your config.** Your existing hooks, permissions and settings are preserved; `uninstall` removes exactly enola's entries and nothing else.

The hooks shell out to `enola hook session-start` and `enola hook stop`, which is what you'll see in `.claude/settings.json`, pinned to the absolute path of the binary you installed with. You never run them yourself.

### The gate - `enola check`

`diff_snapshot` answers "what did my change actually do?" for an agent. `enola check` asks the same question from a shell, and turns the answer into an **exit code** - so the same delta can gate a commit or a CI job with no agent in the loop.

```bash
enola baseline pin /path/to/repo    # 1. freeze how it looks now, BEFORE editing
#   …make your changes…
enola check /path/to/repo           # 2. grade what they did
```

The baseline is a pinned artifact rather than "whatever state the tool last held" - it survives re-snapshots, publishes atomically, and travels to another machine. Why the graph works that way at all: **[docs/SNAPSHOTS.md](SNAPSHOTS.md)**.

| Exit | Meaning |
|------|---------|
| `0` | **clean** - no structural regression |
| `1` | **regression** - the policy was violated |
| `2` | **error** - the gate could not run (no baseline pinned, bad argument, inverted snapshot pair) |
| `3` | **declined** - the baseline is not comparable, so it refused to grade |

`3` is deliberately not `1`. When the two snapshots were built over different inputs - a different enola version, a different extractor set, changed ignore globs - the delta describes *how they were produced*, not what you edited. Reporting that as a failing change would be a lie, so the gate says it declined and why.

**A stale baseline warns; it never blocks.** Past three days it tells you exactly how stale and what that means (the delta now also contains whatever the repo itself changed in between) - then grades anyway, because a long-lived baseline is a legitimate way to measure a multi-day refactor and only you know which you meant.

**What fails by default is narrow: a newly introduced dependency cycle, and nothing else.** Everything below that is reported, not failed - so a red gate is always real. Widen it per repo:

```bash
enola check --fail-on=cycles,layers --min-confidence=0.8   # also fail on new layer violations
enola check --warn-only                                    # report everything, never fail
enola check --json                                         # machine-readable verdict
enola check --detail                                       # full delta under the verdict
enola check --baseline=previous                            # compare against the preceding snapshot
enola check --focus=internal/auth                          # narrow the delta to what you touched
enola check --write                                        # also persist the snapshot (default: read-only)
```

**Declaring what you meant to change.** The flags above grade the delta. `--target` grades
it against your *intent*: reverse-dependency impact analysis runs on the pre-change graph,
and any package the change reached outside that predicted radius is reported as
**spillover** — a package altered by something your description did not cover.

```bash
enola check --target=internal/auth                    # what should this change have reached?
enola check --target=internal/auth --expected=cmd/api # …plus a package you know you touched
enola check --target=internal/auth --max-spillover=0  # and fail if it reached anywhere else
```

```
## Scope

**Reached beyond the declared scope.** 1 of 2 package(s) touched were predicted or
declared, match ratio 0.5.

Spillover — touched but neither predicted nor declared:
  - unrelated

Predicted but not touched (usually fine — the change was narrower than its blast radius):
  - api
```

Spillover is **reported, never failed, until you ask for it**: `--max-spillover=N` allows
up to N and fails above that, so `--max-spillover=0` means "fail on any". A scope check
that broke the build the first time someone passed `--target` would only teach people not
to pass it.

This is the one question a delta cannot answer on its own. A diff is a function of two
snapshots, so it can say what changed and nothing about what you *meant* to change —
spillover needs that third input.

The output names what moved rather than counting it - the added symbols with their `file:line`, the new coupling with its relation kinds, and any finding whose content shifted:

```
FAIL — 1 structural regression introduced.

Regressions (fail):
  - [cycles] 1.00 — Cyclic dependency detected (2 modules)
      module "pkga" is part of the cycle

What changed
  symbols      +2
  dependencies +1
  edges        +4  (imports +1, calls +1, declares +2)

Added (3):
  symbol     pkga.AlphaViaB                    pkga/a.go:7
  symbol     pkgb.Helper                       pkgb/b.go:7
  dependency pkga -> example.com/gate/pkgb     pkga/a.go:3

New coupling (4):
  pkga                --imports--> pkgb
  pkga.AlphaViaB      --calls--> pkgb.Helper
  pkga.AlphaViaB      --declares--> pkga
  pkgb.Helper         --declares--> pkgb
```

Lists cap at 12 entries with a `--detail` pointer, and `declares` edges - the mechanical one-per-new-symbol link to their module - always sort last, since they say nothing about what got coupled.

**Baselines are portable.** A baseline is identified by the repository's normalized git remote (falling back to the checkout directory name), not by the absolute path it was pinned at - so one pinned on a CI runner grades against a checkout anywhere else. That's what makes the CI shape cheap: the default branch publishes `.enola/baseline/` once, every PR restores it and diffs against it, and no job ever indexes the base a second time.

Ready-made wiring: [`examples/hooks/pre-commit`](../examples/hooks/pre-commit) (blocks only on exit `1`; a missing or incomparable baseline skips the gate rather than blocking someone over setup they haven't done) and [`examples/ci/architecture-gate.yml`](../examples/ci/architecture-gate.yml) (publish-on-main, restore-on-PR).

### What it saved you - `--status`

enola isn't just a token saver, but it is one, and it keeps score. `--status` shows every enola server running right now, what your agents have actually called, and an estimate of what those calls replaced:

```
=== enola MCP Status ===
Servers running: 2

      pid  repos                        uptime   calls  dashboard
    59903  api, web, mobile            57m 12s      42  http://127.0.0.1:56730 (shared)
    60122  auth-service                12m 04s       8  http://127.0.0.1:56744
Tracking since: 2026-07-21 11:54:19
Repos tracked: 21

Tool Usage:
  tool                running     total
  explore                   1         1
  generate_snapshot         1         1
  query_facts               1         1
  query_insights            1         1

Value Estimate (approximate):
  tool                calls   ~time saved   ~tokens saved
  explore                 1            6s           11.2K
  generate_snapshot       1  21h 48m 52s           130.9M
  query_facts             1            3s            6.3K
  query_insights          1           14s           23.4K
  TOTAL                   4  21h 49m 16s           130.9M†
  running now             4  21h 49m 16s           130.9M

  † corpus exceeds a single context window — not reproducible by re-reading files.
```

That's a single session over the **Linux kernel** - 218M tokens of C and Rust across 55,399 parsed files, indexed into 1.9M facts in 2m20s. The 130.9M is exactly `218M × 0.6`: priced from the corpus, not from the fact that one call happened. And the dagger is doing more work than the number is - at 218× a context window, that graph isn't expensive to rebuild by reading files, it's *impossible* to.

Run the same session again over an unchanged repo and it collapses to a few thousand tokens: the snapshot ids match, so each call earns confirmation credit instead of a rebuild. Building an understanding and confirming one still holds are different things, and the estimate says so.

`--status --all` gives the same figures broken down **per repository**, sorted by tokens saved - useful for seeing which part of your estate the tooling is actually earning its keep on.

Be clear about what these numbers are. They answer one question: **what would an agent have had to ingest to reach the same answer with ordinary tools - grep, glob, open a file, read it, infer?** So a snapshot is priced from the *corpus it indexed*, measured, not from the fact that a call happened; a 17.9K-token service and the 218M-token Linux kernel are not the same act of work, and no flat per-call price is right for both. Reading time converts to your time waiting on the agent, including the rework a non-deterministic reconstruction implies. Failed calls are counted but earn nothing, and the tokens you spend reading enola's own response are subtracted - so `output_mode='summary'` genuinely scores better than `'full'`.

They're an estimate, labelled as one - but the inputs are real: corpus sizes measured at snapshot time, call counts recorded per repository under `~/.enola/usage/`. They survive server restarts and deleting a repo's `.enola/` directory, and `--status` works from any directory, not just a snapshotted one. The full model, its constants and what it deliberately leaves out are in [ARCHITECTURE.md](../ARCHITECTURE.md#the-value-model).

**The dagger matters more than the number.** When the corpus exceeds what an agent can hold at once - as an 8-repo ecosystem or a large monorepo does - the counterfactual isn't expensive, it's *impossible*: cross-repo edges can't be derived by re-reading files when both sides can't be in context together. Those rows are flagged, because "not reproducible by re-reading" is a stronger claim than any figure.

And the estimate stays conservative where it can't measure. It prices the ingestion an agent avoided and a slice of the rework; it doesn't try to price the missed caller found in code review, or the afternoon spent reconstructing how two services talk. That's the saving `impact_analysis` and `diff_snapshot` are really for, and it's the one you'll feel first.

### The dashboard

Starting the MCP server also starts a **read-only dashboard** on a free loopback port (`127.0.0.1`), printed to stderr on startup - run `enola --status` while the server is up to get the URL again. It refreshes every 30 seconds and shows, in one page:

- **this server** - its PID, binary, uptime, the repos *it* has loaded, and the directory it was launched from;
- **every enola server running right now**, with a link to each one's dashboard, so you can switch between them;
- the same activity and value data as `--status`, split into what this server has served and the lifetime total across all of them;
- the **snapshot receipt** - snapshot ID, enola version, git ref, extractors, fact/insight counts;
- the **graph receipt** - the repos in this server's graph, and clickable counters listing the services and cross-repo edges (the edges also render as a node-link diagram);
- the **insights** grouped by explainer, filterable by confidence band, so you can see what each finding is and how certain it is;
- **extraction quality** - per-service coverage, unresolved routes, and samples of skipped files and parse errors, which is where you look when a snapshot seems thin.

It is strictly a viewer: every request reads through the same concurrency-safe accessors the MCP tools use and never mutates server state. It binds loopback only and serves nothing but that one page. Pass `--no-dashboard` to skip it.

#### Several servers at once

Agent tooling starts one enola server per session, so opening four terminals means four servers - each with its own graph, its own dashboard, and its own ephemeral port. Two things keep that legible.

**One bookmarkable URL.** Besides its own port, every server competes for a fixed **shared URL**, `http://127.0.0.1:7171` by default. The first to start wins it; when that one exits another takes over within a few seconds, so the address keeps working for as long as any server is up. Whichever server answers there lists all the others. Set `ENOLA_DASHBOARD_PORT` (or `dashboard.port` in the config) to move it, or `ENOLA_DASHBOARD_PORT=off` to keep only the ephemeral ports.

**Every page describes its own server.** The PID, uptime, repos and per-server call counts on a page belong to the process serving it - never to whichever server happened to start last. If a page shows a graph you did not expect, the switcher tells you which server holds the one you want.

Running servers register themselves under `~/.enola/instances/`; a record is removed on exit, and one left behind by a hard-killed process is cleaned up by the next reader. Each workspace also keeps its own graph receipt under `~/.enola/graphs/`, so restarting a server in one repo restores *that* repo's graph rather than whatever another terminal snapshotted last.

---

---

## Build from source

Prerequisites: **Go 1.25+** and a **C compiler** (for the tree-sitter bindings).

```bash
go build -o enola ./cmd/enola   # or: go install ./cmd/enola
```

To run a one-shot snapshot without starting the MCP server:

```bash
enola --generate [config_path]   # config_path is optional; defaults to mcp-arch.yaml, falling back to built-in defaults if absent
```

Artifacts are written to the configured `output.dir` (default `.enola/`). The config file is optional - see **[ARCHITECTURE.md → Configuration](../ARCHITECTURE.md#configuration)** for the full field reference and defaults.

**Indexing a whole cluster in one command.** Cross-repo linking needs several repositories in one graph. Name them with `repos:` and a single run indexes them all - the first fresh, the rest appended - producing the service nodes, cross-repo edges, `coverage_report` and unused-route findings that a single-repo snapshot cannot have:

```yaml
# ci/cluster.yaml
repos:
  - ../api
  - ../web
  - ../sdk
```

```bash
enola --generate ci/cluster.yaml
```

Entries resolve **relative to the config file**, not to your working directory, so a cluster config can be checked in and means the same thing on a laptop and in CI. (`repo:` is unchanged: a single repository, relative to the working directory.) Order matters - the first entry resets the graph and the rest are added to it.

---

