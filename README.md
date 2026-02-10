# archmcp - MCP Architectural Snapshot Server

A local [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server that generates compact architectural snapshots of repositories for LLM-assisted development. It eliminates repeated "grep-style" code exploration by providing a structured, language-agnostic view of your codebase.

## How It Works

archmcp runs as a stdio-based MCP server. When connected to an LLM client (such as Cursor, Claude Desktop, or any MCP-compatible tool), it exposes:

- **Resources** -- pre-generated architectural summaries the LLM can read automatically
- **Tools** -- on-demand snapshot generation and fact querying

The pipeline:

```
Repository -> File Walker -> Extractors (Go, TypeScript) -> Fact Store
  -> Explainers (cycles, layers) -> Insights
  -> Renderers (LLM context) -> Artifacts
  -> MCP Server (resources + tools)
```

## Supported Languages

| Language   | Extractor   | Detection          |
|------------|-------------|--------------------|
| Go         | `go/ast`    | `go.mod` present   |
| TypeScript | tree-sitter | `tsconfig.json` or `package.json` with TypeScript |

Next.js route detection (App Router and Pages Router) is included in the TypeScript extractor.

## Installation

### Prerequisites

- Go 1.22+
- C compiler (for tree-sitter CGo bindings)

### Build

```bash
go build -o archmcp ./cmd/archmcp
```

### Install globally

```bash
go install ./cmd/archmcp
```

## Usage

### As an MCP Server

Add to your MCP client configuration. For example, in Cursor's `mcp.json`:

```json
{
  "mcpServers": {
    "archmcp": {
      "command": "/path/to/archmcp",
      "args": ["/path/to/mcp-arch.yaml"]
    }
  }
}
```

Or if installed via `go install`:

```json
{
  "mcpServers": {
    "archmcp": {
      "command": "archmcp"
    }
  }
}
```

### Testing from the command line

You can run a one-shot snapshot generation without starting the MCP server. This is useful for testing the pipeline or running it from CI.

**Syntax:**

```bash
archmcp --generate [config_path]
```

- `config_path` is optional; default is `mcp-arch.yaml`. The repository path is taken from the config's `repo` field (default `"."`).
- Artifacts are written to the configured `output.dir` (default `.archmcp/`).

**Example:** from the repository root (e.g. the archmcp repo itself):

```bash
./archmcp --generate
```

Or with an explicit config file:

```bash
./archmcp --generate mcp-arch.yaml
```

On success, a summary is printed to stderr:

```
Snapshot complete:
  Repository:  /path/to/repo
  Facts:       N
  Insights:    M
  Artifacts:   K
  Duration:    ...
  Output:      /path/to/repo/.archmcp
```

### MCP Resources

| URI | Description |
|-----|-------------|
| `arch://snapshot/context` | Compact LLM-ready architecture summary (Markdown) |
| `arch://snapshot/facts` | All extracted facts (JSONL) |
| `arch://snapshot/insights` | Architectural insights (JSON) |
| `arch://snapshot/meta` | Snapshot metadata (JSON) |

### MCP Tools

#### `generate_snapshot`

Triggers a full snapshot generation for a repository.

**Parameters:**
- `repo_path` (string, optional): Path to the repository. Defaults to the configured repo path.

**Example usage by LLM:**
> "Generate an architectural snapshot of /home/user/myproject"

#### `query_facts`

Queries the extracted fact store with filters.

**Parameters:**
- `kind` (string, optional): Filter by fact kind (`module`, `symbol`, `route`, `storage`, `dependency`)
- `file` (string, optional): Filter by file path
- `name` (string, optional): Filter by name (substring match)
- `relation` (string, optional): Filter by relation kind (`declares`, `imports`, `calls`, `implements`, `depends_on`)

**Example usage by LLM:**
> "Query all route facts" or "Find all symbols in the handler package"

## Developer Workflow

The recommended workflow is: **generate a snapshot first**, then lean on the architectural context in all your subsequent prompts. You only need to regenerate when the codebase changes significantly.

### Step 1: Generate the snapshot

When you open a project in Cursor (or any MCP-compatible tool), start by asking:

> "Generate an architectural snapshot of /path/to/my/project"

This runs the full pipeline (extract, explain, render) and takes milliseconds even on large repos. The LLM now has access to the architecture summary, facts, and insights through the MCP resources.

### Step 2: Use the context in your prompts

Once the snapshot exists, you do not need to reference archmcp explicitly. The LLM can read the `arch://snapshot/context` resource automatically. Just ask your questions naturally -- the architectural context is there.

### Real-World Prompt Examples

**Onboarding to a new codebase:**

> "I just joined this project. Based on the architecture snapshot, give me a tour of the codebase -- what are the main modules, how do they relate, and where should I start reading?"

> "What architecture pattern does this project follow? What are the key entry points?"

**Understanding architecture before making changes:**

> "I need to change how user events are processed. Which modules are involved in the event pipeline? Show me the dependency chain."

> "Are there any cyclic dependencies or layer violations I should be aware of before refactoring?"

**Finding where to add a new feature:**

> "I need to add a new API endpoint for user preferences. Based on the detected architecture, which packages should I touch and in what order?"

> "Where would a new Kafka consumer for order events fit in this codebase?"

**Investigating dependencies and risk zones:**

> "Which modules have the highest fan-in? Those are the ones most likely to break if I change their API."

> "Query all facts with relation 'imports' to see the full dependency graph."

**Querying specific facts for targeted exploration:**

> "Query all symbol facts with name 'Handler' to find every handler in the codebase."

> "Query all route facts to see every API endpoint and which files define them."

> "Query all module facts to get a quick overview of every package."

### Tips for Getting the Most Out of archmcp

- **Regenerate after major changes.** If you add new packages, rename modules, or restructure directories, run `generate_snapshot` again so the LLM has fresh context.
- **Use `query_facts` for precision.** When you need specifics (all interfaces, all imports from a package, all call sites of a function), `query_facts` with filters is faster than asking the LLM to grep.
- **Combine with file reading.** The snapshot tells the LLM *what exists and how it connects*. When the LLM needs actual implementation details, it will still read individual files -- but now it knows exactly *which* files to read.
- **Works across languages.** If your repo has both Go and TypeScript (e.g., a Go backend with a Next.js frontend), archmcp extracts both and presents a unified architecture view.
- **Check insights for surprises.** The cycle detector and layer analyzer often surface architectural issues that are invisible during day-to-day development. Ask: "Are there any architectural insights or warnings for this project?"

## Configuration

Create a `mcp-arch.yaml` file (or pass a custom path as the first argument):

```yaml
repo: "."
ignore:
  # Dependencies and tooling
  - "vendor/**"
  - "node_modules/**"
  - ".git/**"
  - ".archmcp/**"
  # Tests
  - "**/*_test.go"
  - "**/*.test.ts"
  - "**/*.test.tsx"
  - "**/*.spec.ts"
  - "**/*.spec.tsx"
  # Next.js / build and cache
  - ".next/**"
  - "out/**"
  - ".vercel/**"
  - ".turbo/**"
  # Documentation
  - "**/*.md"
  - "**/*.mdx"
  # Config / data (YAML, JSON)
  - "**/*.yml"
  - "**/*.yaml"
  - "**/*.json"
  # CI / ops
  - "Jenkinsfile"
  - "**/Jenkinsfile"
  - "**/Jenkinsfile*"
  # Optional: Docker and env files
  - "Dockerfile"
  - "**/Dockerfile*"
  - "**/.env*"
extractors:
  - go
  - typescript
explainers:
  - cycles
  - layers
renderers:
  - llm_context
output:
  dir: ".archmcp"
  max_context_tokens: 4000
```

### Configuration Reference

| Field | Description | Default |
|-------|-------------|---------|
| `repo` | Repository root path | `"."` |
| `ignore` | Glob patterns for files/dirs to skip | vendor, node_modules, .git, tests, Next.js dirs, docs (.md, .mdx), config (yml, yaml, json), CI (e.g. Jenkinsfile), Dockerfile, .env* |
| `extractors` | Enabled extractors | `["go", "typescript"]` |
| `explainers` | Enabled explainers | `["cycles", "layers"]` |
| `renderers` | Enabled renderers | `["llm_context"]` |
| `output.dir` | Output directory for artifacts | `".archmcp"` |
| `output.max_context_tokens` | Token budget for LLM context | `4000` |

## Output Artifacts

After running `generate_snapshot`, the following files are written to the output directory (default `.archmcp/`):

| File | Description |
|------|-------------|
| `llm_context.md` | Compact architecture summary for LLM consumption |
| `facts.jsonl` | All extracted facts, one JSON object per line |
| `insights.json` | Architectural insights with confidence scores |
| `snapshot.meta.json` | Metadata including file hashes for incremental updates |

## Architecture

### Fact Model

Facts are language-agnostic architectural primitives:

- **Module** -- a package, directory, or logical grouping
- **Symbol** -- a function, type, class, interface, variable, or constant
- **Route** -- an HTTP/API route (e.g., Next.js pages)
- **Dependency** -- an import/require relationship

Each fact can have **relations** to other facts: `declares`, `imports`, `calls`, `implements`, `depends_on`.

### Plugin System

Three plugin interfaces drive the pipeline:

- **Extractors** -- parse source code and emit facts (e.g., Go AST, TypeScript tree-sitter)
- **Explainers** -- analyze facts and produce insights (e.g., cycle detection, layer analysis)
- **Renderers** -- generate output artifacts from the snapshot (e.g., LLM context markdown)

All plugins are registered in-process via Go interfaces. Future versions may support JSON-RPC subprocess isolation.

### Incremental Updates

archmcp tracks file content hashes (SHA-256) in `snapshot.meta.json`. On subsequent runs, only files that have changed are re-extracted, making repeated snapshots fast on large repositories.

## Project Structure

```
archmcp/
├── cmd/archmcp/main.go              # Entry point
├── internal/
│   ├── config/config.go             # YAML config
│   ├── engine/engine.go             # Pipeline orchestrator
│   ├── facts/
│   │   ├── model.go                 # Fact types and constants
│   │   └── store.go                 # In-memory store + JSONL I/O
│   ├── extractors/
│   │   ├── registry.go              # Extractor interface + registry
│   │   ├── goextractor/go.go        # Go AST extractor
│   │   └── tsextractor/ts.go        # TypeScript tree-sitter extractor
│   ├── explainers/
│   │   ├── registry.go              # Explainer interface + registry
│   │   ├── cycles/cycles.go         # Cyclic dependency detector
│   │   └── layers/layers.go         # Architecture pattern detector
│   ├── renderers/
│   │   ├── registry.go              # Renderer interface + registry
│   │   └── llmcontext/llm.go        # LLM context markdown renderer
│   └── server/server.go             # MCP server wiring
├── mcp-arch.yaml                    # Default config
├── go.mod
└── go.sum
```

## License

MIT
