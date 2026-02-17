package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/dejo1307/archmcp/internal/config"
	"github.com/dejo1307/archmcp/internal/engine"
	"github.com/dejo1307/archmcp/internal/explainers/cycles"
	"github.com/dejo1307/archmcp/internal/explainers/layers"
	"github.com/dejo1307/archmcp/internal/extractors/goextractor"
	"github.com/dejo1307/archmcp/internal/extractors/kotlinextractor"
	"github.com/dejo1307/archmcp/internal/extractors/rubyextractor"
	"github.com/dejo1307/archmcp/internal/extractors/swiftextractor"
	"github.com/dejo1307/archmcp/internal/extractors/tsextractor"
	"github.com/dejo1307/archmcp/internal/renderers/llmcontext"
	"github.com/dejo1307/archmcp/internal/server"
)

func main() {
	// Ensure log output goes to stderr, never stdout (MCP uses stdout for JSON-RPC)
	log.SetOutput(os.Stderr)

	ctx := context.Background()

	// Check for --generate flag
	generateMode := false
	cfgPath := "mcp-arch.yaml"
	for _, arg := range os.Args[1:] {
		if arg == "--generate" {
			generateMode = true
		} else {
			cfgPath = arg
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		// If config file doesn't exist, use defaults
		fmt.Fprintf(os.Stderr, "warning: %v, using defaults\n", err)
		cfg = config.Default()
	}

	eng, err := engine.New(cfg)
	if err != nil {
		log.Fatalf("failed to create engine: %v", err)
	}

	// Register extractors
	eng.RegisterExtractor(goextractor.New())
	eng.RegisterExtractor(kotlinextractor.New())
	eng.RegisterExtractor(tsextractor.New())
	eng.RegisterExtractor(swiftextractor.New())
	eng.RegisterExtractor(rubyextractor.New())

	// Register explainers
	eng.RegisterExplainer(cycles.New())
	eng.RegisterExplainer(layers.New())

	// Register renderers
	eng.RegisterRenderer(llmcontext.New(cfg.Output.MaxContextTokens))

	// One-shot generation mode
	if generateMode {
		repoPath, err := filepath.Abs(cfg.Repo)
		if err != nil {
			log.Fatalf("failed to resolve repo path: %v", err)
		}

		snapshot, err := eng.GenerateSnapshot(ctx, repoPath, false)
		if err != nil {
			log.Fatalf("snapshot generation failed: %v", err)
		}

		if err := eng.WriteArtifacts(repoPath); err != nil {
			log.Fatalf("failed to write artifacts: %v", err)
		}

		fmt.Fprintf(os.Stderr, "\nSnapshot complete:\n")
		fmt.Fprintf(os.Stderr, "  Repository:  %s\n", snapshot.Meta.RepoPath)
		fmt.Fprintf(os.Stderr, "  Facts:       %d\n", snapshot.Meta.FactCount)
		fmt.Fprintf(os.Stderr, "  Insights:    %d\n", snapshot.Meta.InsightCount)
		fmt.Fprintf(os.Stderr, "  Artifacts:   %d\n", len(snapshot.Artifacts))
		fmt.Fprintf(os.Stderr, "  Duration:    %s\n", snapshot.Meta.Duration)
		fmt.Fprintf(os.Stderr, "  Output:      %s\n", filepath.Join(repoPath, cfg.Output.Dir))
		os.Exit(0)
	}

	// MCP server mode (default)
	srv, err := server.New(eng, cfg)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
