package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/dejo1307/archmcp/internal/config"
	"github.com/dejo1307/archmcp/internal/engine"
	"github.com/dejo1307/archmcp/internal/facts"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server wraps the MCP server and connects it to the snapshot engine.
type Server struct {
	mcp *mcp.Server
	eng *engine.Engine
	cfg *config.Config
}

// New creates a new MCP server wired to the given engine.
func New(eng *engine.Engine, cfg *config.Config) (*Server, error) {
	s := &Server{
		eng: eng,
		cfg: cfg,
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "archmcp",
		Version: "0.1.0",
	}, nil)

	s.mcp = mcpServer
	s.registerTools()

	return s, nil
}

// Run starts the MCP server on the stdio transport.
func (s *Server) Run(ctx context.Context) error {
	log.Println("[server] starting MCP server on stdio transport")
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

// generateSnapshotArgs are the arguments for the generate_snapshot tool.
type generateSnapshotArgs struct {
	RepoPath string `json:"repo_path" jsonschema:"Path to the repository to analyze. Defaults to the configured repo path."`
}

// queryFactsArgs are the arguments for the query_facts tool.
type queryFactsArgs struct {
	Kind      string `json:"kind,omitempty" jsonschema:"Filter by fact kind: module, symbol, route, storage, or dependency"`
	File      string `json:"file,omitempty" jsonschema:"Filter by file path"`
	Name      string `json:"name,omitempty" jsonschema:"Filter by name using substring match"`
	Relation  string `json:"relation,omitempty" jsonschema:"Filter by relation kind: declares, imports, calls, implements, or depends_on"`
	Prop      string `json:"prop,omitempty" jsonschema:"Filter by property name (e.g. source, symbol_kind, exported, framework, storage_kind)"`
	PropValue string `json:"prop_value,omitempty" jsonschema:"Filter by property value (requires prop to be set)"`

	// Batch filters — OR within dimension, AND across dimensions
	Names      []string `json:"names,omitempty" jsonschema:"Filter by multiple exact names (OR). Use instead of name for batch lookups."`
	Files      []string `json:"files,omitempty" jsonschema:"Filter by multiple file paths (OR). Use instead of file for batch lookups."`
	Kinds      []string `json:"kinds,omitempty" jsonschema:"Filter by multiple kinds (OR). Use instead of kind for batch lookups."`
	FilePrefix string   `json:"file_prefix,omitempty" jsonschema:"Filter by file path prefix (e.g. internal/server to match all files in that directory)"`

	// Pagination
	Offset int `json:"offset,omitempty" jsonschema:"Number of results to skip for pagination. Default 0."`
	Limit  int `json:"limit,omitempty" jsonschema:"Maximum number of results to return (1-500). Default 100."`

	// Relation expansion
	IncludeRelated bool `json:"include_related,omitempty" jsonschema:"If true, inline the full fact data for each relation target instead of just the target name"`

	// Output format
	OutputMode string `json:"output_mode,omitempty" jsonschema:"Output format: 'full' (default JSON), 'compact' (markdown table), or 'names' (just names and files)"`
}

// enrichedFact wraps a Fact with resolved relation targets.
type enrichedFact struct {
	facts.Fact
	RelatedFacts []facts.Fact `json:"related_facts,omitempty"`
}

// queryResponse is the structured response for query_facts when advanced features are used.
type queryResponse struct {
	Facts   any  `json:"facts"`
	Total   int  `json:"total"`
	Offset  int  `json:"offset"`
	Limit   int  `json:"limit"`
	HasMore bool `json:"has_more"`
}

// renderCompact formats facts as a markdown table for minimal token usage.
func renderCompact(results []facts.Fact, total int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d results (showing %d):\n\n", total, len(results)))
	sb.WriteString("| Kind | Name | File | Line |\n")
	sb.WriteString("|------|------|------|------|\n")
	for _, f := range results {
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %d |\n", f.Kind, f.Name, f.File, f.Line))
	}
	return sb.String()
}

// renderNamesOnly returns just names and files, one per line.
func renderNamesOnly(results []facts.Fact, total int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d results (showing %d):\n\n", total, len(results)))
	for _, f := range results {
		sb.WriteString(fmt.Sprintf("%s  %s:%d\n", f.Name, f.File, f.Line))
	}
	return sb.String()
}

// registerTools adds MCP tools for snapshot generation and fact querying.
func (s *Server) registerTools() {
	// Tool: generate_snapshot
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "generate_snapshot",
		Description: "Generate an architectural snapshot of a repository. Parses source code, extracts facts, detects patterns, and produces an LLM-ready context summary.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args generateSnapshotArgs) (*mcp.CallToolResult, any, error) {
		repoPath := args.RepoPath
		if repoPath == "" {
			repoPath = s.cfg.Repo
		}

		absRepo, err := filepath.Abs(repoPath)
		if err != nil {
			return errorResult(fmt.Sprintf("invalid repo path: %v", err)), nil, nil
		}

		snapshot, err := s.eng.GenerateSnapshot(ctx, absRepo)
		if err != nil {
			return errorResult(fmt.Sprintf("snapshot generation failed: %v", err)), nil, nil
		}

		// Write artifacts to disk
		if err := s.eng.WriteArtifacts(absRepo); err != nil {
			log.Printf("[server] warning: failed to write artifacts: %v", err)
		}

		// Return summary
		summary := fmt.Sprintf(
			"Snapshot generated successfully.\n\n"+
				"- Repository: %s\n"+
				"- Facts: %d\n"+
				"- Insights: %d\n"+
				"- Artifacts: %d\n"+
				"- Duration: %s\n"+
				"- Extractors: %v\n"+
				"- Explainers: %v\n\n"+
				"Use query_facts or explore to inspect the extracted architecture.",
			snapshot.Meta.RepoPath,
			snapshot.Meta.FactCount,
			snapshot.Meta.InsightCount,
			len(snapshot.Artifacts),
			snapshot.Meta.Duration,
			snapshot.Meta.Extractors,
			snapshot.Meta.Explainers,
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: summary},
			},
		}, nil, nil
	})

	// Tool: query_facts
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "query_facts",
		Description: "Query the extracted architectural facts by kind, file, name, or relation type. Returns matching facts as JSON. Supports batch filters (names, files, kinds), file prefix matching, pagination (offset/limit), and relation expansion (include_related). For dependencies, filter with prop='source' and prop_value='internal'|'external'|'stdlib' to control noise.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args queryFactsArgs) (*mcp.CallToolResult, any, error) {
		store := s.eng.Store()
		if store.Count() == 0 {
			return errorResult("No facts available. Run generate_snapshot first."), nil, nil
		}

		opts := facts.QueryOpts{
			Kind:       args.Kind,
			Kinds:      args.Kinds,
			File:       args.File,
			Files:      args.Files,
			FilePrefix: args.FilePrefix,
			Name:       args.Name,
			Names:      args.Names,
			RelKind:    args.Relation,
			Prop:       args.Prop,
			PropValue:  args.PropValue,
			Offset:     args.Offset,
			Limit:      args.Limit,
		}

		results, total := store.QueryAdvanced(opts)

		// Compact output modes: return text instead of JSON
		switch args.OutputMode {
		case "compact":
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: renderCompact(results, total)},
				},
			}, nil, nil
		case "names":
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: renderNamesOnly(results, total)},
				},
			}, nil, nil
		}

		// Determine if advanced features are in use (triggers structured response)
		useAdvanced := args.IncludeRelated || args.Offset > 0 || args.Limit > 0 ||
			len(args.Names) > 0 || len(args.Files) > 0 || len(args.Kinds) > 0 ||
			args.FilePrefix != ""

		// Enrich with related facts if requested
		var output any
		if args.IncludeRelated {
			enriched := make([]enrichedFact, len(results))
			seen := make(map[string]struct{}) // deduplicate related facts
			for i, f := range results {
				enriched[i] = enrichedFact{Fact: f}
				for _, rel := range f.Relations {
					if _, dup := seen[rel.Target]; dup {
						continue
					}
					seen[rel.Target] = struct{}{}
					related := store.LookupByExactName(rel.Target)
					enriched[i].RelatedFacts = append(enriched[i].RelatedFacts, related...)
				}
			}
			output = enriched
		} else {
			output = results
		}

		if useAdvanced {
			limit := args.Limit
			if limit <= 0 {
				limit = 100
			}
			if limit > 500 {
				limit = 500
			}
			resp := queryResponse{
				Facts:   output,
				Total:   total,
				Offset:  args.Offset,
				Limit:   limit,
				HasMore: total > args.Offset+len(results),
			}
			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return errorResult(fmt.Sprintf("failed to marshal results: %v", err)), nil, nil
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: string(data)},
				},
			}, nil, nil
		}

		// Legacy format: raw JSON array (backwards compatible)
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal results: %v", err)), nil, nil
		}

		text := string(data)
		if total > len(results) {
			text += fmt.Sprintf("\n\n... (showing %d of %d results, refine your query or use offset/limit for pagination)", len(results), total)
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: text},
			},
		}, nil, nil
	})

	// Tool: show_symbol
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "show_symbol",
		Description: "Show source code for a symbol found in the architectural snapshot. Returns the actual implementation with surrounding context lines.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args showSymbolArgs) (*mcp.CallToolResult, any, error) {
		snapshot := s.eng.Snapshot()
		if snapshot == nil {
			return errorResult("No snapshot available. Run generate_snapshot first."), nil, nil
		}

		store := s.eng.Store()
		if store.Count() == 0 {
			return errorResult("No facts available. Run generate_snapshot first."), nil, nil
		}

		if args.Name == "" {
			return errorResult("name is required"), nil, nil
		}

		results := store.Query("symbol", "", args.Name, "")
		if len(results) == 0 {
			return errorResult(fmt.Sprintf("No symbols matching %q", args.Name)), nil, nil
		}

		contextLines := args.ContextLines
		if contextLines <= 0 {
			contextLines = 30
		}

		// Limit to 5 results
		if len(results) > 5 {
			results = results[:5]
		}

		repoPath := snapshot.Meta.RepoPath
		var sb strings.Builder

		for i, fact := range results {
			if i > 0 {
				sb.WriteString("\n---\n\n")
			}

			// Header
			sb.WriteString(fmt.Sprintf("### %s\n", fact.Name))
			sb.WriteString(fmt.Sprintf("File: %s  Line: %d\n", fact.File, fact.Line))

			// Show props summary
			if sig, ok := fact.Props["signature"].(string); ok {
				sb.WriteString(fmt.Sprintf("Signature:\n```\n%s\n```\n", sig))
			}
			if comp, ok := fact.Props["ios_component"].(string); ok {
				sb.WriteString(fmt.Sprintf("iOS Component: %s\n", comp))
			}

			sb.WriteString("\n")

			// Read source file
			absFile := filepath.Join(repoPath, fact.File)
			source, err := readSourceWindow(absFile, fact.Line, contextLines)
			if err != nil {
				sb.WriteString(fmt.Sprintf("_Could not read source: %v_\n", err))
				continue
			}

				lang := "go"
			if l, ok := fact.Props["language"].(string); ok && l != "" {
				lang = l
			}
			sb.WriteString(fmt.Sprintf("```%s\n%s\n```\n", lang, source))
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
			},
		}, nil, nil
	})

	// Tool: explore
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "explore",
		Description: "Explore a module, file, symbol, or directory in a single call. Returns a rich markdown summary with symbols, dependencies, dependents, and relations — replacing many query_facts calls with one.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args exploreArgs) (*mcp.CallToolResult, any, error) {
		store := s.eng.Store()
		if store.Count() == 0 {
			return errorResult("No facts available. Run generate_snapshot first."), nil, nil
		}

		if args.Focus == "" {
			return errorResult("focus is required"), nil, nil
		}

		depth := args.Depth
		if depth <= 0 {
			depth = 1
		}
		if depth > 2 {
			depth = 2
		}

		var sb strings.Builder

		// Try to determine focus type by matching against store indexes.
		// Priority: exact module name > exact file > symbol name substring > file prefix (directory)
		switch {
		case s.exploreModule(store, args.Focus, depth, &sb):
		case s.exploreFile(store, args.Focus, depth, &sb):
		case s.exploreSymbol(store, args.Focus, depth, &sb):
		case s.exploreDirectory(store, args.Focus, &sb):
		default:
			return errorResult(fmt.Sprintf("No facts matching focus %q. Try a module name, file path, symbol name, or directory prefix.", args.Focus)), nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
			},
		}, nil, nil
	})
}

// exploreArgs are the arguments for the explore tool.
type exploreArgs struct {
	Focus string `json:"focus" jsonschema:"required,Module name, file path, or symbol name to explore"`
	Depth int    `json:"depth,omitempty" jsonschema:"How deep to follow relations (1=direct only, 2=include relations of relations). Default 1, max 2."`
}

// exploreModule renders a module exploration if the focus matches a module name.
func (s *Server) exploreModule(store *facts.Store, focus string, depth int, sb *strings.Builder) bool {
	modules := store.LookupByExactName(focus)
	// Filter to only module-kind facts
	var mod *facts.Fact
	for i := range modules {
		if modules[i].Kind == facts.KindModule {
			mod = &modules[i]
			break
		}
	}
	if mod == nil {
		return false
	}

	sb.WriteString(fmt.Sprintf("# Module: %s\n\n", mod.Name))

	// Props summary
	if lang, ok := mod.Props["language"].(string); ok {
		sb.WriteString(fmt.Sprintf("- Language: %s\n", lang))
	}
	if pkg, ok := mod.Props["package"].(string); ok {
		sb.WriteString(fmt.Sprintf("- Package: %s\n", pkg))
	}
	sb.WriteString("\n")

	// Find symbols declared in this module (symbols whose "declares" relation targets this module)
	declaredSymbols := store.ReverseLookup(mod.Name, facts.RelDeclares)
	if len(declaredSymbols) > 0 {
		sb.WriteString(fmt.Sprintf("## Symbols (%d)\n\n", len(declaredSymbols)))
		sb.WriteString("| Name | Kind | File | Line | Exported |\n")
		sb.WriteString("|------|------|------|------|----------|\n")
		for _, sym := range declaredSymbols {
			symKind, _ := sym.Props["symbol_kind"].(string)
			exported := "no"
			if exp, ok := sym.Props["exported"].(bool); ok && exp {
				exported = "yes"
			}
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %s |\n",
				sym.Name, symKind, sym.File, sym.Line, exported))
		}
		sb.WriteString("\n")
	}

	// Dependencies: facts with kind=dependency whose file starts with the module path
	deps, _ := store.QueryAdvanced(facts.QueryOpts{Kind: facts.KindDependency, FilePrefix: mod.Name + "/"})
	if len(deps) > 0 {
		sb.WriteString(fmt.Sprintf("## Dependencies (%d)\n\n", len(deps)))
		for _, dep := range deps {
			for _, r := range dep.Relations {
				if r.Kind == facts.RelImports {
					sb.WriteString(fmt.Sprintf("- %s\n", r.Target))
				}
			}
		}
		sb.WriteString("\n")
	}

	// Reverse dependencies: who imports this module
	dependents := store.ReverseLookup(mod.Name, facts.RelImports)
	if len(dependents) > 0 {
		sb.WriteString(fmt.Sprintf("## Dependents (%d)\n\n", len(dependents)))
		for _, dep := range dependents {
			sb.WriteString(fmt.Sprintf("- %s\n", dep.Name))
		}
		sb.WriteString("\n")
	}

	// If depth=2, show key symbol relations
	if depth >= 2 && len(declaredSymbols) > 0 {
		sb.WriteString("## Symbol Relations\n\n")
		limit := len(declaredSymbols)
		if limit > 20 {
			limit = 20
		}
		for _, sym := range declaredSymbols[:limit] {
			if len(sym.Relations) <= 1 {
				continue // skip symbols with only a "declares" relation
			}
			sb.WriteString(fmt.Sprintf("**%s**\n", sym.Name))
			for _, r := range sym.Relations {
				if r.Kind == facts.RelDeclares {
					continue
				}
				sb.WriteString(fmt.Sprintf("  - %s → %s\n", r.Kind, r.Target))
			}
			sb.WriteString("\n")
		}
	}

	return true
}

// exploreFile renders a file exploration if the focus matches an exact file path.
func (s *Server) exploreFile(store *facts.Store, focus string, depth int, sb *strings.Builder) bool {
	fileFacts := store.ByFile(focus)
	if len(fileFacts) == 0 {
		return false
	}

	sb.WriteString(fmt.Sprintf("# File: %s\n\n", focus))
	sb.WriteString(fmt.Sprintf("Total facts: %d\n\n", len(fileFacts)))

	// Group by kind
	byKind := make(map[string][]facts.Fact)
	for _, f := range fileFacts {
		byKind[f.Kind] = append(byKind[f.Kind], f)
	}

	for _, kind := range []string{facts.KindModule, facts.KindSymbol, facts.KindDependency, facts.KindRoute, facts.KindStorage} {
		ff := byKind[kind]
		if len(ff) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("## %ss (%d)\n\n", capitalize(kind), len(ff)))
		for _, f := range ff {
			sb.WriteString(fmt.Sprintf("- **%s**", f.Name))
			if f.Line > 0 {
				sb.WriteString(fmt.Sprintf(" (line %d)", f.Line))
			}
			if sk, ok := f.Props["symbol_kind"].(string); ok {
				sb.WriteString(fmt.Sprintf(" [%s]", sk))
			}
			sb.WriteString("\n")
			if depth >= 2 {
				for _, r := range f.Relations {
					sb.WriteString(fmt.Sprintf("  - %s → %s\n", r.Kind, r.Target))
				}
			}
		}
		sb.WriteString("\n")
	}

	return true
}

// exploreSymbol renders a symbol exploration if the focus matches symbol names via substring.
func (s *Server) exploreSymbol(store *facts.Store, focus string, depth int, sb *strings.Builder) bool {
	results := store.Query(facts.KindSymbol, "", focus, "")
	if len(results) == 0 {
		return false
	}

	if len(results) > 10 {
		results = results[:10]
	}

	sb.WriteString(fmt.Sprintf("# Symbol: %s\n\n", focus))

	for i, sym := range results {
		if i > 0 {
			sb.WriteString("---\n\n")
		}

		sb.WriteString(fmt.Sprintf("## %s\n\n", sym.Name))
		sb.WriteString(fmt.Sprintf("- File: %s\n", sym.File))
		sb.WriteString(fmt.Sprintf("- Line: %d\n", sym.Line))
		if sk, ok := sym.Props["symbol_kind"].(string); ok {
			sb.WriteString(fmt.Sprintf("- Kind: %s\n", sk))
		}
		if lang, ok := sym.Props["language"].(string); ok {
			sb.WriteString(fmt.Sprintf("- Language: %s\n", lang))
		}
		if exp, ok := sym.Props["exported"].(bool); ok {
			sb.WriteString(fmt.Sprintf("- Exported: %v\n", exp))
		}
		sb.WriteString("\n")

		// Relations
		if len(sym.Relations) > 0 {
			sb.WriteString("### Relations\n\n")
			for _, r := range sym.Relations {
				sb.WriteString(fmt.Sprintf("- %s → %s\n", r.Kind, r.Target))
			}
			sb.WriteString("\n")
		}

		// Resolve relation targets (depth >= 1)
		if depth >= 1 && len(sym.Relations) > 0 {
			sb.WriteString("### Related Facts\n\n")
			seen := make(map[string]struct{})
			for _, r := range sym.Relations {
				if _, dup := seen[r.Target]; dup {
					continue
				}
				seen[r.Target] = struct{}{}
				related := store.LookupByExactName(r.Target)
				for _, rf := range related {
					sb.WriteString(fmt.Sprintf("- **%s** (%s) — %s", rf.Name, rf.Kind, rf.File))
					if rf.Line > 0 {
						sb.WriteString(fmt.Sprintf(":%d", rf.Line))
					}
					sb.WriteString("\n")
				}
			}
			sb.WriteString("\n")
		}

		// Reverse relations: who calls/imports/depends on this symbol
		callers := store.ReverseLookup(sym.Name, "")
		if len(callers) > 0 {
			sb.WriteString("### Referenced By\n\n")
			limit := len(callers)
			if limit > 20 {
				limit = 20
			}
			for _, c := range callers[:limit] {
				for _, r := range c.Relations {
					if r.Target == sym.Name {
						sb.WriteString(fmt.Sprintf("- %s (%s)\n", c.Name, r.Kind))
						break
					}
				}
			}
			if len(callers) > 20 {
				sb.WriteString(fmt.Sprintf("- ... and %d more\n", len(callers)-20))
			}
			sb.WriteString("\n")
		}
	}

	return true
}

// exploreDirectory renders a directory summary if the focus matches a file prefix.
func (s *Server) exploreDirectory(store *facts.Store, focus string, sb *strings.Builder) bool {
	prefix := focus
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	dirFacts, total := store.QueryAdvanced(facts.QueryOpts{FilePrefix: prefix, Limit: 500})
	if total == 0 {
		return false
	}

	sb.WriteString(fmt.Sprintf("# Directory: %s\n\n", focus))
	sb.WriteString(fmt.Sprintf("Total facts: %d\n\n", total))

	// Count by kind
	kindCount := make(map[string]int)
	files := make(map[string]struct{})
	for _, f := range dirFacts {
		kindCount[f.Kind]++
		if f.File != "" {
			files[f.File] = struct{}{}
		}
	}

	sb.WriteString("## Summary\n\n")
	sb.WriteString(fmt.Sprintf("- Files: %d\n", len(files)))
	for _, kind := range []string{facts.KindModule, facts.KindSymbol, facts.KindDependency, facts.KindRoute, facts.KindStorage} {
		if c, ok := kindCount[kind]; ok {
			sb.WriteString(fmt.Sprintf("- %ss: %d\n", capitalize(kind), c))
		}
	}
	sb.WriteString("\n")

	// List modules
	var modules []facts.Fact
	var symbols []facts.Fact
	for _, f := range dirFacts {
		switch f.Kind {
		case facts.KindModule:
			modules = append(modules, f)
		case facts.KindSymbol:
			symbols = append(symbols, f)
		}
	}

	if len(modules) > 0 {
		sb.WriteString(fmt.Sprintf("## Modules (%d)\n\n", len(modules)))
		for _, m := range modules {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Name))
		}
		sb.WriteString("\n")
	}

	if len(symbols) > 0 {
		sb.WriteString(fmt.Sprintf("## Key Symbols (showing up to 30)\n\n"))
		limit := len(symbols)
		if limit > 30 {
			limit = 30
		}
		sb.WriteString("| Name | Kind | File | Line |\n")
		sb.WriteString("|------|------|------|------|\n")
		for _, sym := range symbols[:limit] {
			symKind, _ := sym.Props["symbol_kind"].(string)
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %d |\n",
				sym.Name, symKind, sym.File, sym.Line))
		}
		if len(symbols) > 30 {
			sb.WriteString(fmt.Sprintf("\n... and %d more symbols\n", len(symbols)-30))
		}
		sb.WriteString("\n")
	}

	return true
}

// showSymbolArgs are the arguments for the show_symbol tool.
type showSymbolArgs struct {
	Name         string `json:"name" jsonschema:"required,Symbol name to look up (substring match)"`
	ContextLines int    `json:"context_lines,omitempty" jsonschema:"Number of source lines to show around the symbol (default 30)"`
}

// readSourceWindow reads lines from a file centered around the given line number.
func readSourceWindow(absFile string, centerLine, contextLines int) (string, error) {
	data, err := os.ReadFile(absFile)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(data), "\n")
	startLine := centerLine - contextLines/2
	if startLine < 1 {
		startLine = 1
	}
	endLine := centerLine + contextLines/2
	if endLine > len(lines) {
		endLine = len(lines)
	}

	var sb strings.Builder
	for i := startLine; i <= endLine; i++ {
		sb.WriteString(fmt.Sprintf("%4d│ %s\n", i, lines[i-1]))
	}
	return sb.String(), nil
}

// capitalize returns s with its first letter uppercased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		IsError: true,
	}
}
