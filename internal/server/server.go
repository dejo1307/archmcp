package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"

	"github.com/dejo1307/archmcp/internal/config"
	"github.com/dejo1307/archmcp/internal/engine"
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
	s.registerResources()
	s.registerTools()

	return s, nil
}

// Run starts the MCP server on the stdio transport.
func (s *Server) Run(ctx context.Context) error {
	log.Println("[server] starting MCP server on stdio transport")
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

// registerResources adds MCP resources for snapshot artifacts.
func (s *Server) registerResources() {
	// Resource: architecture context (the main LLM summary)
	s.mcp.AddResource(&mcp.Resource{
		URI:         "arch://snapshot/context",
		Name:        "Architecture Context",
		Description: "Compact LLM-ready architecture summary of the repository",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		content, err := s.eng.GetArtifact("llm_context.md")
		if err != nil {
			return nil, fmt.Errorf("no snapshot available: %w (run generate_snapshot first)", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: req.Params.URI, Text: string(content), MIMEType: "text/markdown"},
			},
		}, nil
	})

	// Resource: facts
	s.mcp.AddResource(&mcp.Resource{
		URI:         "arch://snapshot/facts",
		Name:        "Architecture Facts",
		Description: "All extracted architectural facts in JSONL format",
		MIMEType:    "application/jsonl",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		content, err := s.eng.GetArtifact("facts.jsonl")
		if err != nil {
			return nil, fmt.Errorf("no snapshot available: %w (run generate_snapshot first)", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: req.Params.URI, Text: string(content), MIMEType: "application/jsonl"},
			},
		}, nil
	})

	// Resource: insights
	s.mcp.AddResource(&mcp.Resource{
		URI:         "arch://snapshot/insights",
		Name:        "Architecture Insights",
		Description: "Architectural insights and analysis results",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		content, err := s.eng.GetArtifact("insights.json")
		if err != nil {
			return nil, fmt.Errorf("no snapshot available: %w (run generate_snapshot first)", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: req.Params.URI, Text: string(content), MIMEType: "application/json"},
			},
		}, nil
	})

	// Resource: meta
	s.mcp.AddResource(&mcp.Resource{
		URI:         "arch://snapshot/meta",
		Name:        "Snapshot Metadata",
		Description: "Metadata about the last snapshot generation",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		content, err := s.eng.GetArtifact("snapshot.meta.json")
		if err != nil {
			return nil, fmt.Errorf("no snapshot available: %w (run generate_snapshot first)", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{URI: req.Params.URI, Text: string(content), MIMEType: "application/json"},
			},
		}, nil
	})
}

// generateSnapshotArgs are the arguments for the generate_snapshot tool.
type generateSnapshotArgs struct {
	RepoPath string `json:"repo_path" jsonschema:"Path to the repository to analyze. Defaults to the configured repo path."`
}

// queryFactsArgs are the arguments for the query_facts tool.
type queryFactsArgs struct {
	Kind     string `json:"kind,omitempty" jsonschema:"Filter by fact kind: module, symbol, route, storage, or dependency"`
	File     string `json:"file,omitempty" jsonschema:"Filter by file path"`
	Name     string `json:"name,omitempty" jsonschema:"Filter by name using substring match"`
	Relation string `json:"relation,omitempty" jsonschema:"Filter by relation kind: declares, imports, calls, implements, or depends_on"`
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
				"Use the arch://snapshot/context resource to read the LLM-ready summary.",
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
		Description: "Query the extracted architectural facts by kind, file, name, or relation type. Returns matching facts as JSON.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args queryFactsArgs) (*mcp.CallToolResult, any, error) {
		store := s.eng.Store()
		if store.Count() == 0 {
			return errorResult("No facts available. Run generate_snapshot first."), nil, nil
		}

		results := store.Query(args.Kind, args.File, args.Name, args.Relation)

		// Limit output
		truncated := false
		if len(results) > 100 {
			results = results[:100]
			truncated = true
		}

		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal results: %v", err)), nil, nil
		}

		text := string(data)
		if truncated {
			text += fmt.Sprintf("\n\n... (showing 100 of %d results, refine your query)", store.Count())
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: text},
			},
		}, nil, nil
	})
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		IsError: true,
	}
}
