package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/explain"
)

func main() {
	log.SetOutput(os.Stderr)

	ctx := context.Background()

	generateMode := false
	explainMode := false
	cfgPath := "mcp-arch.yaml"
	explainRepo := "" // optional positional repo path for --explain

	for _, arg := range os.Args[1:] {
		switch arg {
		case "--generate":
			generateMode = true
		case "--explain":
			explainMode = true
		default:
			// In --explain mode the positional argument is the repository path;
			// otherwise it is the config file path.
			if explainMode {
				explainRepo = arg
			} else {
				cfgPath = arg
			}
		}
	}

	eng, cfg, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: cfgPath,
	})
	if err != nil {
		log.Fatalf("failed to create engine: %v", err)
	}

	if explainMode {
		runExplain(ctx, eng, cfg, explainRepo)
		os.Exit(0)
	}

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

	bootstrap.AutoLoadSnapshot(eng, cfg)

	srv, err := bootstrap.NewServer(eng, cfg)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// runExplain indexes the given repository (defaulting to the configured repo)
// and prints a human-readable statistical summary to stdout.
func runExplain(ctx context.Context, eng *bootstrap.Engine, cfg *config.Config, repoArg string) {
	repo := repoArg
	if repo == "" {
		repo = cfg.Repo
	}
	repoPath, err := filepath.Abs(repo)
	if err != nil {
		log.Fatalf("failed to resolve repo path: %v", err)
	}

	fmt.Fprintf(os.Stderr, "Analyzing %s …\n", repoPath)
	// --explain is a read-only, no-artifacts mode: reuse a cache if one exists,
	// but never write to .enola.
	eng.SetPersistCache(false)
	if _, err := eng.GenerateSnapshot(ctx, repoPath, false); err != nil {
		log.Fatalf("snapshot generation failed: %v", err)
	}

	report := explain.Compute(eng)
	fmt.Print(report.Render())
}
