# archmcp

Give your AI agent a map of the codebase before it starts exploring.

archmcp is a local [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server that generates compact architectural snapshots of repositories. Run it once, and your AI coding agent (Claude Code, Cursor, Copilot, or any MCP-compatible tool) gets a structured overview of modules, symbols, dependencies, routes, and architectural patterns - before it reads a single file.

## What This Is (and Isn't)

**A first step, not a replacement.** archmcp is designed to run *before* your AI agent starts exploring code. It gives the agent a structural overview so it knows where to look and what connects to what. It does not replace grep, file search, code reading, or any traditional discovery tool - it makes them more effective by providing upfront context.

**Input for AI agents.** The snapshot output (modules, symbols, dependencies, architectural patterns) is structured context designed for LLM consumption. It is not a dashboard, not a visualization tool, not a documentation generator. It answers the question: *"What does this codebase look like?"* so the agent can skip the guessing phase.

**Built for multi-repo work.** When you work across multiple repositories - a Go backend, a TypeScript frontend, a Kotlin Android app, a Swift iOS app - having an architectural snapshot of each repo lets AI agents understand cross-repo structure without manually exploring every codebase from scratch.

## How It Works

archmcp runs as a stdio-based MCP server. When connected to an LLM client, it exposes pre-generated architectural summaries (resources) and on-demand snapshot generation and querying (tools).

The pipeline:

```
Repository -> File Walker -> Extractors (Go, Kotlin, TypeScript, Swift) -> Fact Store
  -> Explainers (cycles, layers) -> Insights
  -> Renderers (LLM context) -> Artifacts
  -> MCP Server (resources + tools)
```

## Quick Start

### Prerequisites

- Go 1.22+
- C compiler (for tree-sitter CGo bindings)

### Build

```bash
go build -o archmcp ./cmd/archmcp
```

Or install globally:

```bash
go install ./cmd/archmcp
```

### Connect to your MCP client

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

Run a one-shot snapshot generation without starting the MCP server:

```bash
archmcp --generate [config_path]
```

`config_path` is optional (default: `mcp-arch.yaml`). Artifacts are written to the configured `output.dir` (default `.archmcp/`).

## Developer Workflow

**Generate a snapshot first**, then lean on the architectural context in all your subsequent prompts. Regenerate when the codebase changes significantly.

### Step 1: Generate the snapshot

When you open a project in Cursor (or any MCP-compatible tool), start by asking:

> "Generate an architectural snapshot of /path/to/my/project"

This runs the full pipeline and takes milliseconds even on large repos. The LLM now has access to the architecture summary, facts, and insights through the MCP resources.

### Step 2: Use the context in your prompts

Once the snapshot exists, you do not need to reference archmcp explicitly. The LLM can read the `arch://snapshot/context` resource automatically. Just ask your questions naturally - the architectural context is there.

### Example Prompts

> "I just joined this project. Based on the architecture snapshot, give me a tour of the codebase - what are the main modules, how do they relate, and where should I start reading?"

> "I need to add a new API endpoint for user preferences. Based on the detected architecture, which packages should I touch and in what order?"

> "Are there any cyclic dependencies or layer violations I should be aware of before refactoring?"

> "Query all route facts to see every API endpoint and which files define them."

### Tips

- **Regenerate after major changes.** If you add new packages, rename modules, or restructure directories, run `generate_snapshot` again so the LLM has fresh context.
- **Use `query_facts` for precision.** When you need specifics (all interfaces, all imports from a package, all call sites of a function), `query_facts` with filters is faster than asking the LLM to grep.
- **Combine with file reading.** The snapshot tells the LLM *what exists and how it connects*. When the LLM needs actual implementation details, it will still read individual files - but now it knows exactly *which* files to read.
- **Check insights for surprises.** The cycle detector and layer analyzer often surface architectural issues that are invisible during day-to-day development.

## Supported Languages

| Language   | Extractor     | Detection          |
|------------|---------------|--------------------|
| Go         | `go/ast`      | `go.mod` present   |
| Kotlin     | regex scanner | `build.gradle.kts` or `build.gradle` with Kotlin/Android |
| TypeScript | tree-sitter   | `tsconfig.json` or `package.json` with TypeScript |
| Swift      | regex scanner | `Package.swift`, `.xcodeproj`, or `.xcworkspace` present |

Next.js route detection (App Router and Pages Router) is included in the TypeScript extractor.

The Kotlin extractor includes Android-specific awareness: it detects Jetpack Compose (`@Composable`), Hilt DI (`@HiltViewModel`, `@Module`, `@AndroidEntryPoint`), Room database (`@Entity`, `@Dao`, `@Database`), ViewModels, Repositories, Use Cases, Workers, and other Android architecture components.

The Swift extractor includes iOS-specific awareness: it detects SwiftUI views (`View`, `App`, `Scene` conformances), UIKit components (`UIViewController`, `UIView` subclasses), Combine ViewModels (`ObservableObject`, `@Observable`), architectural patterns (Repositories, Use Cases, Coordinators, Services, DI Containers), and `@MainActor` annotations.

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
  - kotlin
  - typescript
  - swift
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
| `extractors` | Enabled extractors | `["go", "kotlin", "typescript", "swift"]` |
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

## MCP Reference

### Resources

| URI | Description |
|-----|-------------|
| `arch://snapshot/context` | Compact LLM-ready architecture summary (Markdown) |
| `arch://snapshot/facts` | All extracted facts (JSONL) |
| `arch://snapshot/insights` | Architectural insights (JSON) |
| `arch://snapshot/meta` | Snapshot metadata (JSON) |

### Tools

#### `generate_snapshot`

Triggers a full snapshot generation for a repository.

**Parameters:**
- `repo_path` (string, optional): Path to the repository. Defaults to the configured repo path.

#### `query_facts`

Queries the extracted fact store with filters.

**Parameters:**
- `kind` (string, optional): Filter by fact kind (`module`, `symbol`, `route`, `storage`, `dependency`)
- `file` (string, optional): Filter by file path
- `name` (string, optional): Filter by name (substring match)
- `relation` (string, optional): Filter by relation kind (`declares`, `imports`, `calls`, `implements`, `depends_on`)

#### `explore`

Rich markdown exploration of a module, file, symbol, or directory in a single call.

**Parameters:**
- `focus` (string, required): Module name, file path, or symbol name to explore
- `depth` (integer, optional): How deep to follow relations (1=direct only, 2=include relations of relations)

#### `show_symbol`

Show source code for a symbol found in the snapshot.

**Parameters:**
- `name` (string, required): Symbol name to look up (substring match)
- `context_lines` (integer, optional): Number of source lines to show around the symbol (default 30)

## Architecture

### Fact Model

Facts are language-agnostic architectural primitives:

- **Module** - a package, directory, or logical grouping
- **Symbol** - a function, type, class, interface, variable, or constant
- **Route** - an HTTP/API route (e.g., Next.js pages)
- **Dependency** - an import/require relationship

Each fact can have **relations** to other facts: `declares`, `imports`, `calls`, `implements`, `depends_on`.

### Plugin System

Three plugin interfaces drive the pipeline:

- **Extractors** - parse source code and emit facts (e.g., Go AST, Kotlin regex scanner, Swift regex scanner, TypeScript tree-sitter)
- **Explainers** - analyze facts and produce insights (e.g., cycle detection, layer analysis)
- **Renderers** - generate output artifacts from the snapshot (e.g., LLM context markdown)

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
│   │   ├── kotlinextractor/kotlin.go # Kotlin regex extractor (Android-aware)
│   │   ├── swiftextractor/swift.go  # Swift regex extractor (iOS-aware)
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
