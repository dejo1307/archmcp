package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dejo1307/archmcp/internal/config"
	"github.com/dejo1307/archmcp/internal/explainers"
	"github.com/dejo1307/archmcp/internal/extractors"
	"github.com/dejo1307/archmcp/internal/facts"
	"github.com/dejo1307/archmcp/internal/renderers"
)

// Engine orchestrates the snapshot generation pipeline.
type Engine struct {
	cfg        *config.Config
	extractors *extractors.Registry
	explainers *explainers.Registry
	renderers  *renderers.Registry
	store      *facts.Store
	snapshot   *facts.Snapshot
	prevHashes map[string]string // file -> sha256 hash from previous run
}

// New creates a new Engine with the given config.
// Extractors, explainers, and renderers must be registered after creation.
func New(cfg *config.Config) (*Engine, error) {
	return &Engine{
		cfg:        cfg,
		extractors: extractors.NewRegistry(),
		explainers: explainers.NewRegistry(),
		renderers:  renderers.NewRegistry(),
		store:      facts.NewStore(),
	}, nil
}

// RegisterExtractor adds an extractor to the engine.
func (e *Engine) RegisterExtractor(ext extractors.Extractor) {
	e.extractors.Register(ext)
}

// RegisterExplainer adds an explainer to the engine.
func (e *Engine) RegisterExplainer(exp explainers.Explainer) {
	e.explainers.Register(exp)
}

// RegisterRenderer adds a renderer to the engine.
func (e *Engine) RegisterRenderer(rnd renderers.Renderer) {
	e.renderers.Register(rnd)
}

// Store returns the fact store.
func (e *Engine) Store() *facts.Store {
	return e.store
}

// Snapshot returns the last generated snapshot, or nil.
func (e *Engine) Snapshot() *facts.Snapshot {
	return e.snapshot
}

// Config returns the engine config.
func (e *Engine) Config() *config.Config {
	return e.cfg
}

// GenerateSnapshot runs the full pipeline: walk -> extract -> explain -> render.
func (e *Engine) GenerateSnapshot(ctx context.Context, repoPath string) (*facts.Snapshot, error) {
	start := time.Now()

	if repoPath == "" {
		repoPath = e.cfg.Repo
	}

	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolving repo path: %w", err)
	}

	// Load previous hashes for incremental support
	e.loadPreviousHashes(absRepo)

	// Clear previous state
	e.store.Clear()

	// 1. Walk repository and collect files
	files, err := e.walkRepo(absRepo)
	if err != nil {
		return nil, fmt.Errorf("walking repo: %w", err)
	}
	log.Printf("[engine] found %d files in %s", len(files), absRepo)

	// 2. Compute hashes and filter to changed files
	currentHashes, changedFiles := e.filterChangedFiles(absRepo, files)
	log.Printf("[engine] %d of %d files changed since last run", len(changedFiles), len(files))

	// 3. Detect and run extractors (on changed files only if we have previous data, otherwise all)
	filesToExtract := files
	if len(e.prevHashes) > 0 && len(changedFiles) < len(files) {
		filesToExtract = changedFiles
		// If no files changed, reload previous facts and skip extraction
		if len(changedFiles) == 0 {
			prevFactsPath := filepath.Join(absRepo, e.cfg.Output.Dir, "facts.jsonl")
			if err := e.store.ReadJSONLFile(prevFactsPath); err == nil {
				log.Printf("[engine] no changes detected, reloaded %d facts from cache", e.store.Count())
			} else {
				// Cache miss, extract everything
				filesToExtract = files
			}
		}
	}

	usedExtractors, err := e.runExtractors(ctx, absRepo, filesToExtract)
	if err != nil {
		return nil, fmt.Errorf("extraction: %w", err)
	}
	log.Printf("[engine] extracted %d facts using %d extractors", e.store.Count(), len(usedExtractors))

	// 4. Run explainers
	allInsights, usedExplainers, err := e.runExplainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("explanation: %w", err)
	}
	log.Printf("[engine] produced %d insights using %d explainers", len(allInsights), len(usedExplainers))

	// 5. Build file hashes for the snapshot meta
	var fileHashes []facts.FileHash
	for path, hash := range currentHashes {
		fileHashes = append(fileHashes, facts.FileHash{
			Path:    path,
			Hash:    hash,
			ModTime: fileModTime(filepath.Join(absRepo, path)),
		})
	}

	// 6. Build snapshot
	duration := time.Since(start)
	snapshot := &facts.Snapshot{
		Meta: facts.SnapshotMeta{
			RepoPath:     absRepo,
			GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
			Duration:     duration.String(),
			Extractors:   usedExtractors,
			Explainers:   usedExplainers,
			Renderers:    []string{},
			FileHashes:   fileHashes,
			FactCount:    e.store.Count(),
			InsightCount: len(allInsights),
		},
		Facts:    e.store.All(),
		Insights: allInsights,
	}

	// 7. Run renderers
	usedRenderers, err := e.runRenderers(ctx, snapshot)
	if err != nil {
		return nil, fmt.Errorf("rendering: %w", err)
	}
	snapshot.Meta.Renderers = usedRenderers
	log.Printf("[engine] produced %d artifacts using %d renderers", len(snapshot.Artifacts), len(usedRenderers))

	e.snapshot = snapshot
	log.Printf("[engine] snapshot generated in %s", duration)
	return snapshot, nil
}

// walkRepo collects all files in the repo, applying ignore patterns.
func (e *Engine) walkRepo(repoPath string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(repoPath, path)
		if err != nil {
			return err
		}

		// Skip ignored paths
		if e.isIgnored(relPath, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if !d.IsDir() {
			files = append(files, relPath)
		}
		return nil
	})
	return files, err
}

// isIgnored checks whether a path matches any ignore pattern.
func (e *Engine) isIgnored(relPath string, isDir bool) bool {
	// Normalize to forward slashes for matching
	relPath = filepath.ToSlash(relPath)

	for _, pattern := range e.cfg.Ignore {
		// Handle directory-only patterns
		if strings.HasSuffix(pattern, "/**") {
			dirPrefix := strings.TrimSuffix(pattern, "/**")
			if relPath == dirPrefix || strings.HasPrefix(relPath, dirPrefix+"/") {
				return true
			}
		}

		// Standard glob match
		matched, err := filepath.Match(pattern, relPath)
		if err == nil && matched {
			return true
		}

		// Also try matching just the filename for patterns like **/*.go
		if strings.HasPrefix(pattern, "**/") {
			subPattern := strings.TrimPrefix(pattern, "**/")
			matched, err = filepath.Match(subPattern, filepath.Base(relPath))
			if err == nil && matched {
				return true
			}
			// Also try the full relative path
			matched, err = filepath.Match(subPattern, relPath)
			if err == nil && matched {
				return true
			}
		}
	}
	return false
}

// runExtractors detects applicable extractors and runs them.
func (e *Engine) runExtractors(ctx context.Context, repoPath string, files []string) ([]string, error) {
	var usedNames []string

	for _, ext := range e.extractors.All() {
		if !e.cfg.IsExtractorEnabled(ext.Name()) {
			continue
		}

		detected, err := ext.Detect(repoPath)
		if err != nil {
			log.Printf("[engine] extractor %s detect error: %v", ext.Name(), err)
			continue
		}
		if !detected {
			log.Printf("[engine] extractor %s: not detected", ext.Name())
			continue
		}

		log.Printf("[engine] running extractor: %s", ext.Name())
		extracted, err := ext.Extract(ctx, repoPath, files)
		if err != nil {
			log.Printf("[engine] extractor %s error: %v", ext.Name(), err)
			continue
		}

		e.store.Add(extracted...)
		usedNames = append(usedNames, ext.Name())
		log.Printf("[engine] extractor %s: emitted %d facts", ext.Name(), len(extracted))
	}

	return usedNames, nil
}

// runExplainers runs all enabled explainers.
func (e *Engine) runExplainers(ctx context.Context) ([]facts.Insight, []string, error) {
	var allInsights []facts.Insight
	var usedNames []string

	for _, exp := range e.explainers.All() {
		if !e.cfg.IsExplainerEnabled(exp.Name()) {
			continue
		}

		log.Printf("[engine] running explainer: %s", exp.Name())
		insights, err := exp.Explain(ctx, e.store)
		if err != nil {
			log.Printf("[engine] explainer %s error: %v", exp.Name(), err)
			continue
		}

		allInsights = append(allInsights, insights...)
		usedNames = append(usedNames, exp.Name())
		log.Printf("[engine] explainer %s: produced %d insights", exp.Name(), len(insights))
	}

	return allInsights, usedNames, nil
}

// runRenderers runs all enabled renderers.
func (e *Engine) runRenderers(ctx context.Context, snapshot *facts.Snapshot) ([]string, error) {
	var usedNames []string

	for _, rnd := range e.renderers.All() {
		if !e.cfg.IsRendererEnabled(rnd.Name()) {
			continue
		}

		log.Printf("[engine] running renderer: %s", rnd.Name())
		artifacts, err := rnd.Render(ctx, snapshot)
		if err != nil {
			log.Printf("[engine] renderer %s error: %v", rnd.Name(), err)
			continue
		}

		snapshot.Artifacts = append(snapshot.Artifacts, artifacts...)
		usedNames = append(usedNames, rnd.Name())
	}

	return usedNames, nil
}

// WriteArtifacts writes all snapshot artifacts to the output directory,
// including facts.jsonl, insights.json, and snapshot.meta.json.
func (e *Engine) WriteArtifacts(repoPath string) error {
	if e.snapshot == nil {
		return fmt.Errorf("no snapshot generated")
	}

	outDir := filepath.Join(repoPath, e.cfg.Output.Dir)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	// Write renderer artifacts (e.g. llm_context.md)
	for _, a := range e.snapshot.Artifacts {
		path := filepath.Join(outDir, a.Name)
		if err := os.WriteFile(path, a.Content, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", a.Name, err)
		}
		log.Printf("[engine] wrote %s (%d bytes)", path, len(a.Content))
	}

	// Write facts.jsonl
	factsPath := filepath.Join(outDir, "facts.jsonl")
	if err := e.store.WriteJSONLFile(factsPath); err != nil {
		return fmt.Errorf("writing facts.jsonl: %w", err)
	}
	log.Printf("[engine] wrote %s", factsPath)

	// Write insights.json
	insightsJSON, err := json.MarshalIndent(e.snapshot.Insights, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling insights: %w", err)
	}
	insightsPath := filepath.Join(outDir, "insights.json")
	if err := os.WriteFile(insightsPath, insightsJSON, 0o644); err != nil {
		return fmt.Errorf("writing insights.json: %w", err)
	}
	log.Printf("[engine] wrote %s (%d bytes)", insightsPath, len(insightsJSON))

	// Write snapshot.meta.json
	metaJSON, err := json.MarshalIndent(e.snapshot.Meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling meta: %w", err)
	}
	metaPath := filepath.Join(outDir, "snapshot.meta.json")
	if err := os.WriteFile(metaPath, metaJSON, 0o644); err != nil {
		return fmt.Errorf("writing snapshot.meta.json: %w", err)
	}
	log.Printf("[engine] wrote %s (%d bytes)", metaPath, len(metaJSON))

	return nil
}

// GetArtifact returns the content of a named artifact, or the generated JSONL/JSON files.
func (e *Engine) GetArtifact(name string) ([]byte, error) {
	if e.snapshot == nil {
		return nil, fmt.Errorf("no snapshot generated")
	}

	switch name {
	case "facts.jsonl":
		var buf bytes.Buffer
		if err := e.store.WriteJSONL(&buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case "insights.json":
		return json.MarshalIndent(e.snapshot.Insights, "", "  ")
	case "snapshot.meta.json":
		return json.MarshalIndent(e.snapshot.Meta, "", "  ")
	default:
		for _, a := range e.snapshot.Artifacts {
			if a.Name == name {
				return a.Content, nil
			}
		}
		return nil, fmt.Errorf("artifact %q not found", name)
	}
}

// loadPreviousHashes reads file hashes from the previous snapshot.meta.json.
func (e *Engine) loadPreviousHashes(repoPath string) {
	metaPath := filepath.Join(repoPath, e.cfg.Output.Dir, "snapshot.meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		e.prevHashes = nil
		return
	}

	var meta facts.SnapshotMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		e.prevHashes = nil
		return
	}

	e.prevHashes = make(map[string]string, len(meta.FileHashes))
	for _, fh := range meta.FileHashes {
		e.prevHashes[fh.Path] = fh.Hash
	}
	log.Printf("[engine] loaded %d file hashes from previous snapshot", len(e.prevHashes))
}

// filterChangedFiles computes SHA-256 hashes for all files and returns
// the current hash map and the list of files that have changed since the previous run.
func (e *Engine) filterChangedFiles(repoPath string, files []string) (map[string]string, []string) {
	currentHashes := make(map[string]string, len(files))
	var changed []string

	for _, relFile := range files {
		absFile := filepath.Join(repoPath, relFile)
		data, err := os.ReadFile(absFile)
		if err != nil {
			// Can't hash, treat as changed
			changed = append(changed, relFile)
			continue
		}

		h := sha256.Sum256(data)
		hash := hex.EncodeToString(h[:])
		currentHashes[relFile] = hash

		if prevHash, ok := e.prevHashes[relFile]; !ok || prevHash != hash {
			changed = append(changed, relFile)
		}
	}

	return currentHashes, changed
}

// fileModTime returns the modification time of a file as an RFC3339 string.
func fileModTime(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339)
}
