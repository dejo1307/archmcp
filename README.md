# enola

**A deterministic map of your codebase for AI coding agents — the real architecture, extracted from your source, not guessed.**

enola is a local [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server. Point it at one or more repositories and it builds a precise graph of your code's architecture — modules, types, routes, dependencies, and how they all connect — then exposes tools your AI agent can use to read, traverse, and reason about that structure. So before your agent writes a line of code, it already knows the shape of the thing it's editing.

---

## Why enola

AI coding agents are powerful, but they're non-deterministic. On every task they re-discover your codebase from scratch — grepping, opening files, inferring how things fit together — and they get it subtly wrong often enough to matter. That guessing costs you **time** (wrong turns, re-prompts) and **tokens** (re-reading the same files, every session).

enola removes the guessing from the part that should never be guessed: the structure.

It gives your agent a **deterministic architectural ground truth** — a graph of your code's real types and relationships, built by parsers and graph algorithms, not by a language model. Run it twice on the same commit and you get the same answer, every time. The agent starts from facts instead of assumptions.

The result is the difference between *vibe coding* — prompt, hope, fix — and **AI-augmented engineering**: fewer wrong turns, fewer tokens burned, and work you can reproduce. enola adds determinism where AI lacks it, and your agent spends its intelligence on the actual problem instead of re-learning your repo.

> enola is a **first step**, not a replacement. It runs *before* your agent explores, so it knows where to look and what connects to what. It doesn't replace grep, file reading, or code search — it makes them precise.

---

## What it is

Under the hood, enola models your codebase as a **graph of architectural types — which we call _kinds_ — and the relations between them.** That's the whole concept: not a magic "knowledge graph," just a deeply technical, structural map of what your code actually contains.

The **kinds** (the nodes):

- **module** — a package or directory
- **symbol** — a function, method, struct, interface, type, class, or constant
- **route** — an HTTP/API endpoint
- **storage** — a database table or data store
- **dependency** — an import relationship
- **service** — a whole repository (used when you analyze several at once)

The **relations** (the edges) connect them: *declares*, *imports*, *calls*, *implements*, *depends_on*, and more. On top of this graph, enola builds a small set of tools your agent can call to answer real structural questions with exact answers.

For the full mental model and internals, see **[ARCHITECTURE.md](ARCHITECTURE.md)**.

---

## Who it's for

- **Anyone pairing with an AI coding agent** — Claude Code, Cursor, Copilot, Opencode, or any MCP-compatible tool.
- **Teams working across multiple repos** — a backend, a web frontend, a mobile app. enola links them into one cross-repo graph so an agent can follow a call from the web client all the way into the service that answers it.
- **Anyone about to refactor** — and wanting to know the blast radius *before* touching code.

---

## The tools (and how they work together)

The workflow is simple: **generate the map once, then ask.** After that, your agent has seven tools on top of the graph:

| Tool | The question it answers |
|------|-------------------------|
| `generate_snapshot` | "Map this repo." Build or refresh the graph. Run it first; use `append` to add more repos. |
| `explore` | "What's in this module/file/symbol, and what touches it?" A guided tour. |
| `query_facts` | "List exactly these." Every route, every interface, every external dependency. |
| `show_symbol` | "Show me the code." Jump straight to a symbol's source. |
| `traverse` | "What does X depend on?" / "What depends on X?" Walk the graph. |
| `find_path` | "How does A reach B?" The call or dependency chain between two points. |
| **`impact_analysis`** | **"If I change X, what breaks?"** The blast radius of a change. |

**`impact_analysis` is the one to know.** Before a refactor, it computes the full set of code that transitively depends on what you're about to change — grouped by how many hops away it is, and aware of cross-repo dependencies. Instead of your agent *guessing* what a change might affect (and missing things), it gets the exact dependent set. That's determinism turned into a concrete payoff: safer changes, planned in the right order, the first time.

See **[ARCHITECTURE.md](ARCHITECTURE.md)** for every tool's full parameters.

---

## See it in action

The examples below ask different models to explain the authentication and authorization flow across **three repositories** — a web UI client, a backend, and a custom auth provider — using the enola snapshot as context.

### Claude Code

![Generating an architectural snapshot in Claude Code](docs/screenshots/claude_code_haiku_three_repos_analysis.png)

### Opencode

![Generating an architectural snapshot in Opencode](docs/screenshots/opencode_qwen3_coder_next_three_repos_analysis.png)

---

## Quick start

### Install

Grab a prebuilt binary — no Go toolchain or C compiler required:

```bash
curl -fsSL https://raw.githubusercontent.com/enola-labs/enola/main/install.sh | sh
```

This installs `enola` to `~/.local/bin`. If that's not on your `PATH`, add it:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Binaries are published for Linux, macOS (amd64/arm64), and Windows (amd64). You can also download a specific build from the [Releases page](https://github.com/enola-labs/enola/releases), or [build from source](#build-from-source).

### Connect it to your agent

**Claude Code** — register enola as an MCP server with one command. This assumes the `enola` binary is on your `PATH` (the install script above puts it in `~/.local/bin`):

```bash
claude mcp add enola enola /path/to/enola/mcp-arch.yaml
```

The shape is `claude mcp add <name> <command> [args…]`: the first `enola` names the server, the second is the binary, and the trailing path is the config it reads. That config's `repo:` is only the *default* repository — you can still snapshot any repo by passing `repo_path` to `generate_snapshot`. Verify it registered with `claude mcp list`, then start Claude Code and ask it to generate a snapshot.

**Cursor / other MCP clients** — add enola to your client's MCP configuration. For example, in Cursor's `mcp.json`:

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

### Use it

Open a project and ask your agent to map it:

> "Generate an architectural snapshot of /path/to/my/project"

That's it. The snapshot takes milliseconds even on large repos, and your agent now has the tools above plus a ready-to-read summary at `.enola/llm_context.md`. From here, just ask your questions naturally:

> "I just joined this project — based on the snapshot, give me a tour: the main modules, how they relate, and where to start reading."

> "I need to add an API endpoint for user preferences. Which packages should I touch, and in what order?"

> "Are there cyclic dependencies or layer violations I should know about before refactoring?"

> "What would break if I refactor `internal/server`? Show me the impact analysis."

Working across several repos? Generate the first, then add the rest with append mode — enola links them into one cross-repo graph:

> "Generate a snapshot of /path/to/go-service with append mode"

> "If I change the auth service, which other services are impacted?"

**Regenerate after major changes** so the map stays current. enola only re-parses files whose contents changed, so refreshes are fast.

---

## Supported languages

| Language   | Detected by |
|------------|-------------|
| Go         | `go.mod` |
| TypeScript | `tsconfig.json` / `package.json` with TypeScript (Next.js & monorepo aware) |
| Python     | `pyproject.toml`, `requirements.txt`, `setup.py`, … (FastAPI / Django / SQLAlchemy aware) |
| Kotlin     | `build.gradle(.kts)` with Kotlin/Android (Compose / Hilt / Room aware) |
| Swift      | `Package.swift`, `.xcodeproj`, `.xcworkspace` (SwiftUI / UIKit aware) |
| Ruby       | `Gemfile` (Rails / ActiveRecord / Packwerk aware) |
| C++        | `.cpp`/`.hpp`/… source or `CMakeLists.txt`/`Makefile` + header (header/source method merging, namespaces, templates) |
| OpenAPI    | any spec with an `openapi:` / `swagger:` key |

Framework- and platform-specific detection for each language is described in **[ARCHITECTURE.md → Supported languages](ARCHITECTURE.md#supported-languages)**.

> Python is parsed with tree-sitter and now contributes call and dependency edges to the graph, so `traverse`, `find_path`, and `impact_analysis` reach into Python code — not just modules and routes.

---

## Build from source

Prerequisites: **Go 1.25+** and a **C compiler** (for the tree-sitter bindings).

```bash
go build -o enola ./cmd/enola   # or: go install ./cmd/enola
```

To run a one-shot snapshot without starting the MCP server:

```bash
enola --generate [config_path]   # config_path defaults to mcp-arch.yaml
```

Artifacts are written to the configured `output.dir` (default `.enola/`). Configuration is documented in **[ARCHITECTURE.md → Configuration](ARCHITECTURE.md#configuration)**.

---

## Learn more

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — the concept, the fact model, the pipeline, and the full tool reference.
- **[examples/](examples/)** — ready-made per-language and multi-repo configs.

## License

Apache License 2.0 — see [`LICENSE`](LICENSE).

## Acknowledgements

enola bundles third-party components under their own licenses; see [`NOTICE`](NOTICE). Swift parsing uses the [tree-sitter-swift](https://github.com/alex-pinkus/tree-sitter-swift) grammar by Alex Pinkus (MIT), vendored under [`internal/extractors/swiftextractor/grammar/`](internal/extractors/swiftextractor/grammar/).
