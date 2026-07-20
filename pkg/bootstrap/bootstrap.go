package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/explainers/complexity"
	"github.com/enola-labs/enola/internal/explainers/coverage"
	crossrepoexp "github.com/enola-labs/enola/internal/explainers/crossrepo"
	"github.com/enola-labs/enola/internal/explainers/cycles"
	"github.com/enola-labs/enola/internal/explainers/depth"
	"github.com/enola-labs/enola/internal/explainers/godclass"
	"github.com/enola-labs/enola/internal/explainers/hotspots"
	"github.com/enola-labs/enola/internal/explainers/layers"
	"github.com/enola-labs/enola/internal/explainers/surface"
	"github.com/enola-labs/enola/internal/explainers/unusedroutes"
	"github.com/enola-labs/enola/internal/extractors/cppextractor"
	"github.com/enola-labs/enola/internal/extractors/goextractor"
	"github.com/enola-labs/enola/internal/extractors/grpcextractor"
	"github.com/enola-labs/enola/internal/extractors/javaextractor"
	"github.com/enola-labs/enola/internal/extractors/kotlinextractor"
	"github.com/enola-labs/enola/internal/extractors/openapiextractor"
	"github.com/enola-labs/enola/internal/extractors/phpextractor"
	"github.com/enola-labs/enola/internal/extractors/pythonextractor"
	"github.com/enola-labs/enola/internal/extractors/rubyextractor"
	"github.com/enola-labs/enola/internal/extractors/rustextractor"
	"github.com/enola-labs/enola/internal/extractors/swiftextractor"
	"github.com/enola-labs/enola/internal/extractors/tsextractor"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/renderers/llmcontext"
	"github.com/enola-labs/enola/internal/server"
	"github.com/enola-labs/enola/pkg/plugin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Engine wraps the internal engine with a public interface for
// extension by enterprise or third-party code.
type Engine struct {
	eng *engine.Engine
}

// Store returns the underlying fact store.
func (e *Engine) Store() *facts.Store {
	return e.eng.Store()
}

// Snapshot returns the last generated snapshot, or nil.
func (e *Engine) Snapshot() *facts.Snapshot {
	return e.eng.Snapshot()
}

// ActiveRepo returns the absolute repo path of the currently loaded snapshot,
// or "" if none is loaded. Used to attribute tool usage to the repo a call
// actually operated on.
func (e *Engine) ActiveRepo() string {
	if snap := e.eng.Snapshot(); snap != nil {
		return snap.Meta.RepoPath
	}
	return ""
}

// SetSnapshot sets the snapshot (used when auto-loading from disk).
func (e *Engine) SetSnapshot(snap *facts.Snapshot) {
	e.eng.SetSnapshot(snap)
}

// RestoreFromDir restores a persisted snapshot into the engine (see engine.RestoreFromDir).
func (e *Engine) RestoreFromDir(dir string, repoPaths map[string]string, singleRepoLabel string) error {
	return e.eng.RestoreFromDir(dir, repoPaths, singleRepoLabel)
}

// SetPersistCache controls whether the per-extractor cache is written to disk.
func (e *Engine) SetPersistCache(persist bool) {
	e.eng.SetPersistCache(persist)
}

// ResolveFactFile returns the absolute filesystem path for a fact's File field.
func (e *Engine) ResolveFactFile(f *facts.Fact) string {
	return e.eng.ResolveFactFile(f)
}

// RepoPaths returns the repo label -> absolute path mapping.
func (e *Engine) RepoPaths() map[string]string {
	return e.eng.RepoPaths()
}

// Config returns the engine config.
func (e *Engine) Config() *config.Config {
	return e.eng.Config()
}

// GenerateSnapshot runs the full pipeline: walk -> extract -> explain -> render.
func (e *Engine) GenerateSnapshot(ctx context.Context, repoPath string, appendMode bool) (*facts.Snapshot, error) {
	return e.eng.GenerateSnapshot(ctx, repoPath, appendMode)
}

// WriteArtifacts writes all snapshot artifacts to the output directory.
func (e *Engine) WriteArtifacts(repoPath string) error {
	return e.eng.WriteArtifacts(repoPath)
}

// WriteGlobalReceipt refreshes the graph-wide receipt at ~/.enola/receipt.json.
func (e *Engine) WriteGlobalReceipt() error {
	return e.eng.WriteGlobalReceipt()
}

// SetBaseline pins the current on-disk snapshot as the diff baseline.
func (e *Engine) SetBaseline(repoPath string) error {
	return e.eng.SetBaseline(repoPath)
}

// OutputDir returns the absolute .enola output directory for repoPath.
func (e *Engine) OutputDir(repoPath string) string {
	return e.eng.OutputDir(repoPath)
}

// LoadSnapshotDir reads a persisted snapshot (facts.jsonl + insights.json +
// snapshot.meta.json) from dir into an in-memory Snapshot for diffing.
func LoadSnapshotDir(dir string) (*facts.Snapshot, error) {
	return engine.LoadSnapshotDir(dir)
}

// GetArtifact returns the content of a named artifact.
func (e *Engine) GetArtifact(name string) ([]byte, error) {
	return e.eng.GetArtifact(name)
}

// RegisterExtractor adds an extractor to the engine.
func (e *Engine) RegisterExtractor(ext plugin.Extractor) {
	e.eng.RegisterExtractor(ext)
}

// RegisterExplainer adds an explainer to the engine.
func (e *Engine) RegisterExplainer(exp plugin.Explainer) {
	e.eng.RegisterExplainer(exp)
}

// RegisterRenderer adds a renderer to the engine.
func (e *Engine) RegisterRenderer(rnd plugin.Renderer) {
	e.eng.RegisterRenderer(rnd)
}

// Server wraps the MCP server with a public interface.
type Server struct {
	srv *server.Server
}

// Run starts the MCP server on the stdio transport.
func (s *Server) Run(ctx context.Context) error {
	return s.srv.Run(ctx)
}

// SetToolCallback sets a callback invoked each time a tool is called. The
// callback receives the tool name and the absolute repo path the call operated
// on.
func (s *Server) SetToolCallback(cb func(tool, repo string)) {
	s.srv.SetToolCallback(cb)
}

// StartTime returns the time the server started (zero value if Run() hasn't been called).
func (s *Server) StartTime() time.Time {
	return s.srv.GetStartTime()
}

// MCP returns the underlying MCP server so enterprise code can register
// additional (license-gated) tools before calling Run.
func (s *Server) MCP() *mcp.Server {
	return s.srv.MCPServer()
}

// Options controls bootstrap behavior.
type Options struct {
	// ConfigPath is the path to the YAML config file. Default: "mcp-arch.yaml".
	ConfigPath string
}

// NewEngine creates an Engine with all OSS plugins registered.
// Use the returned Engine's methods to add additional (enterprise) plugins
// before starting the server or generating snapshots.
func NewEngine(opts Options) (*Engine, *config.Config, error) {
	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = "mcp-arch.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil && !filepath.IsAbs(cfgPath) {
		if exePath, exErr := os.Executable(); exErr == nil {
			exeDir := filepath.Dir(exePath)
			cfg, err = config.Load(filepath.Join(exeDir, cfgPath))
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v, using defaults\n", err)
		cfg = config.Default()
	}

	eng, err := engine.New(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create engine: %w", err)
	}

	// Register all OSS extractors
	eng.RegisterExtractor(cppextractor.New())
	eng.RegisterExtractor(goextractor.New())
	eng.RegisterExtractor(grpcextractor.New())
	eng.RegisterExtractor(javaextractor.New())
	eng.RegisterExtractor(kotlinextractor.New())
	eng.RegisterExtractor(openapiextractor.New())
	eng.RegisterExtractor(phpextractor.New())
	eng.RegisterExtractor(pythonextractor.New())
	eng.RegisterExtractor(tsextractor.New())
	eng.RegisterExtractor(swiftextractor.New())
	eng.RegisterExtractor(rubyextractor.New())
	eng.RegisterExtractor(rustextractor.New())

	// Register all OSS explainers
	eng.RegisterExplainer(cycles.New())
	eng.RegisterExplainer(layers.New())
	eng.RegisterExplainer(crossrepoexp.New())
	eng.RegisterExplainer(coverage.New())
	eng.RegisterExplainer(unusedroutes.New())
	eng.RegisterExplainer(godclass.New())
	eng.RegisterExplainer(hotspots.New())
	eng.RegisterExplainer(depth.New())
	eng.RegisterExplainer(surface.New())
	eng.RegisterExplainer(complexity.New())

	// Register all OSS renderers
	eng.RegisterRenderer(llmcontext.New(cfg.Output.MaxContextTokens))

	return &Engine{eng: eng}, cfg, nil
}

// NewServer creates an MCP server wired to the given Engine.
func NewServer(eng *Engine, cfg *config.Config) (*Server, error) {
	srv, err := server.New(eng.eng, cfg)
	if err != nil {
		return nil, err
	}
	return &Server{srv: srv}, nil
}

// AutoLoadSnapshot restores an existing snapshot from disk if available, so queries
// (and the enterprise tools) work immediately after a restart WITHOUT a
// generate_snapshot call.
//
// It prefers the graph-wide registry at ~/.enola/receipt.json: that lists every
// repo currently in the graph and their paths, so a restart restores the WHOLE
// multi-repo graph — not just cfg.Repo — with no extractor runs. If that is
// unavailable it falls back to a single-repo restore of cfg.Repo. Either way it
// restores facts + insights + the snapshot meta (incl. generated_at, which the
// freshness check needs), unlike the old facts-only load.
//
// It publishes a fresh bundle via engine.RestoreFromDir. This is safe ONLY because
// it runs single-threaded at startup, before the MCP server begins serving tool
// calls — no reader can observe a half-built store. Callers must keep it strictly
// before Server.Run.
func AutoLoadSnapshot(eng *Engine, cfg *config.Config) {
	// Preferred path: reload the full multi-repo graph from the global registry.
	if gr, err := engine.LoadGlobalReceipt(); err == nil && len(gr.Repos) > 0 {
		if restoreFromGlobalReceipt(eng, cfg, gr) {
			return
		}
		log.Printf("[bootstrap] global receipt present but multi-repo restore incomplete; falling back to single-repo")
	}

	// Fallback: single-repo restore of cfg.Repo.
	repoPath, err := filepath.Abs(cfg.Repo)
	if err != nil {
		return
	}
	dir := filepath.Join(repoPath, cfg.Output.Dir)
	if _, err := os.Stat(filepath.Join(dir, "facts.jsonl")); err != nil {
		return // nothing on disk; start empty
	}
	label := filepath.Base(repoPath)
	if err := eng.RestoreFromDir(dir, map[string]string{label: repoPath}, label); err != nil {
		log.Printf("[bootstrap] warning: failed to restore snapshot from %s: %v", dir, err)
		return
	}
	log.Printf("[bootstrap] restored single-repo snapshot for %s", label)
}

// restoreFromGlobalReceipt reloads the complete multi-repo graph named by the
// global receipt. In append/multi-repo mode WriteArtifacts writes the ENTIRE
// in-memory store to each repo's .enola, so the most-recently-generated repo dir
// holds every repo's facts; that dir is loaded once (facts are already tagged with
// their repo labels). Returns false if it cannot find a complete snapshot dir, so
// the caller can fall back. repoPaths comes from the receipt so multi-repo file
// resolution works after restore.
func restoreFromGlobalReceipt(eng *Engine, cfg *config.Config, gr *facts.GraphReceipt) bool {
	repoPaths := make(map[string]string, len(gr.Repos))
	for _, r := range gr.Repos {
		if r.Path != "" {
			repoPaths[r.Label] = r.Path
		}
	}
	if len(repoPaths) == 0 {
		return false
	}

	completeDir, ok := newestSnapshotDir(gr.Repos, cfg.Output.Dir)
	if !ok {
		return false
	}

	// Single-repo graph: the file may be untagged, so pass its label; a genuine
	// multi-repo file is already tagged and SetRepoRange leaves it untouched.
	singleLabel := ""
	if len(repoPaths) == 1 {
		for l := range repoPaths {
			singleLabel = l
		}
	}

	if err := eng.RestoreFromDir(completeDir, repoPaths, singleLabel); err != nil {
		log.Printf("[bootstrap] warning: multi-repo restore from %s failed: %v", completeDir, err)
		return false
	}
	if got := eng.Store().Count(); gr.FactCount > 0 && got != gr.FactCount {
		log.Printf("[bootstrap] note: restored %d facts but global receipt records %d (partial restore from %s)", got, gr.FactCount, completeDir)
	}
	log.Printf("[bootstrap] restored graph of %d repo(s) from %s", len(repoPaths), completeDir)
	return true
}

// newestSnapshotDir picks the repo snapshot directory with the newest generated_at
// among the graph's repos — the one whose facts.jsonl holds the complete store.
// Returns false when no repo dir has a readable snapshot timestamp.
func newestSnapshotDir(repos []facts.GraphRepoEntry, outDir string) (string, bool) {
	var bestDir string
	var bestTS time.Time
	found := false
	for _, r := range repos {
		if r.Path == "" {
			continue
		}
		dir := filepath.Join(r.Path, outDir)
		ts, ok := snapshotGeneratedAt(dir)
		if !ok {
			continue
		}
		if !found || ts.After(bestTS) {
			bestTS, bestDir, found = ts, dir, true
		}
	}
	return bestDir, found
}

// snapshotGeneratedAt reads the generated_at timestamp from a snapshot dir,
// preferring snapshot.meta.json and falling back to receipt.json.
func snapshotGeneratedAt(dir string) (time.Time, bool) {
	for _, name := range []string{"snapshot.meta.json", "receipt.json"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var m struct {
			GeneratedAt string `json:"generated_at"`
		}
		if err := json.Unmarshal(data, &m); err != nil || m.GeneratedAt == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, m.GeneratedAt); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
