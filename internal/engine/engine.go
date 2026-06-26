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
	"sync"
	"time"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/explainers"
	"github.com/enola-labs/enola/internal/extractors"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/linkers/crossrepo"
	"github.com/enola-labs/enola/internal/renderers"
)

// Engine orchestrates the snapshot generation pipeline.
type Engine struct {
	mu         sync.Mutex // serializes GenerateSnapshot calls
	cfg        *config.Config
	extractors *extractors.Registry
	explainers *explainers.Registry
	renderers  *renderers.Registry
	store      *facts.Store
	snapshot   *facts.Snapshot
	repoPaths  map[string]string // repo label -> absolute path (populated in append mode)

	// persistCache controls whether the per-extractor cache is written back to
	// disk after a snapshot. The read path is always active when caching is
	// enabled; one-shot --explain sets this false so it never touches .enola.
	persistCache bool
}

// New creates a new Engine with the given config.
// Extractors, explainers, and renderers must be registered after creation.
func New(cfg *config.Config) (*Engine, error) {
	return &Engine{
		cfg:          cfg,
		extractors:   extractors.NewRegistry(),
		explainers:   explainers.NewRegistry(),
		renderers:    renderers.NewRegistry(),
		store:        facts.NewStore(),
		persistCache: true,
	}, nil
}

// SetPersistCache controls whether the per-extractor cache is written to disk
// after a snapshot. One-shot --explain disables this so it leaves .enola
// untouched, while still reusing a cache a prior --generate may have written.
func (e *Engine) SetPersistCache(persist bool) { e.persistCache = persist }

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

// SetRepoPaths sets the repo label -> absolute path mapping (used in tests).
func (e *Engine) SetRepoPaths(paths map[string]string) {
	e.repoPaths = paths
}

// SetSnapshot sets the snapshot (used in tests).
func (e *Engine) SetSnapshot(snap *facts.Snapshot) {
	e.snapshot = snap
}

// RepoPaths returns the repo label -> absolute path mapping (populated in append mode).
func (e *Engine) RepoPaths() map[string]string {
	if e.repoPaths == nil {
		return nil
	}
	cp := make(map[string]string, len(e.repoPaths))
	for k, v := range e.repoPaths {
		cp[k] = v
	}
	return cp
}

// ResolveFactFile returns the absolute filesystem path for a fact's File field.
// In multi-repo mode, it strips the repo-label prefix and joins with the
// corresponding repo root. In single-repo mode it falls back to the snapshot's
// RepoPath.
func (e *Engine) ResolveFactFile(f *facts.Fact) string {
	// Multi-repo: if the fact has a Repo label that maps to a known path,
	// strip the repo prefix from f.File and join with the absolute root.
	if f.Repo != "" && e.repoPaths != nil {
		if absRoot, ok := e.repoPaths[f.Repo]; ok {
			rel := strings.TrimPrefix(f.File, f.Repo+"/")
			return filepath.Join(absRoot, rel)
		}
	}

	// Single-repo fallback.
	if e.snapshot != nil {
		return filepath.Join(e.snapshot.Meta.RepoPath, f.File)
	}
	return f.File
}

// GenerateSnapshot runs the full pipeline: walk -> extract -> explain -> render.
// When appendMode is true the existing store is preserved and new facts are
// added with file paths prefixed by the repo basename, enabling multi-repo queries.
func (e *Engine) GenerateSnapshot(ctx context.Context, repoPath string, appendMode bool) (*facts.Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	start := time.Now()

	if repoPath == "" {
		repoPath = e.cfg.Repo
	}

	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolving repo path: %w", err)
	}

	repoLabel := filepath.Base(absRepo)

	if appendMode {
		// Track repo label -> absolute path for multi-repo resolution.
		if e.repoPaths == nil {
			e.repoPaths = make(map[string]string)
		}
		e.repoPaths[repoLabel] = absRepo

		// Retroactively tag facts from a prior single-repo snapshot so they
		// are filterable by repo alongside the newly appended facts.
		if e.snapshot != nil && e.store.Count() > 0 {
			prevLabel := filepath.Base(e.snapshot.Meta.RepoPath)
			if _, alreadyTracked := e.repoPaths[prevLabel]; !alreadyTracked {
				tagged := e.store.TagUntagged(prevLabel, prevLabel+"/")
				if tagged > 0 {
					e.repoPaths[prevLabel] = e.snapshot.Meta.RepoPath
					log.Printf("[engine] retroactively tagged %d existing facts with repo label %q", tagged, prevLabel)
				}
			}
		}
	} else {
		// Clear previous state (default single-repo behaviour).
		e.store.Clear()
		e.repoPaths = nil
	}

	// Per-stage timing breakdown (logged at the end). Snapshotting is
	// extraction-dominated, so this makes it obvious where time goes.
	var tWalk, tHash, tExtract, tLink, tGraph, tExplain, tRender time.Duration

	// 1. Walk repository and collect files
	tStage := time.Now()
	files, err := e.walkRepo(absRepo)
	if err != nil {
		return nil, fmt.Errorf("walking repo: %w", err)
	}
	tWalk = time.Since(tStage)
	log.Printf("[engine] found %d files in %s", len(files), absRepo)

	// 2. Compute file hashes (for snapshot metadata)
	tStage = time.Now()
	currentHashes := e.computeFileHashes(absRepo, files)
	tHash = time.Since(tStage)

	// 3. Detect and run extractors (with optional per-extractor caching).
	tStage = time.Now()
	var cache *extractorCache
	cachePath := extractorCachePath(filepath.Join(absRepo, e.cfg.Output.Dir))
	if e.cfg.IncrementalEnabled() {
		cache = loadExtractorCache(cachePath)
	}
	preCount := e.store.Count()
	usedExtractors, err := e.runExtractors(ctx, absRepo, files, currentHashes, cache)
	if err != nil {
		return nil, fmt.Errorf("extraction: %w", err)
	}
	if cache != nil {
		log.Printf("[engine] extractor cache: %d reused", cache.hits)
		if e.persistCache {
			if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
				log.Printf("[engine] could not create cache dir: %v", err)
			} else if err := cache.save(cachePath); err != nil {
				log.Printf("[engine] could not write extractor cache: %v", err)
			}
		}
	}
	tExtract = time.Since(tStage)
	newCount := e.store.Count()
	log.Printf("[engine] extracted %d facts using %d extractors", newCount, len(usedExtractors))

	// Always set Repo on newly extracted facts so the repo filter works
	// even in single-repo mode.
	e.store.SetRepoRange(preCount, repoLabel)

	// In append mode, additionally prefix file paths so facts from
	// different repos are distinguishable by file path.
	if appendMode {
		e.store.TagRange(preCount, repoLabel, repoLabel+"/")
		log.Printf("[engine] prefixed %d facts with repo label %q", newCount-preCount, repoLabel)
	}

	// 3b. Link repos into a cross-repo "graph of graphs": derive service-level
	// nodes and consumer→provider edges from HTTP route role matching and
	// import/shared-lib references. Recomputed from scratch each run (prior
	// synthetic facts are dropped first) so it stays idempotent across appends.
	tStage = time.Now()
	e.linkCrossRepo()
	e.flagUnmatchedRoutes()
	tLink = time.Since(tStage)

	// 3c. Build graph index for traversal queries
	tStage = time.Now()
	e.store.BuildGraph()
	tGraph = time.Since(tStage)
	log.Printf("[engine] built graph index (%d nodes, %d edges)", e.store.Graph().NodeCount(), e.store.Graph().EdgeCount())

	// 4. Run explainers
	tStage = time.Now()
	allInsights, usedExplainers, err := e.runExplainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("explanation: %w", err)
	}
	tExplain = time.Since(tStage)
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
	tStage = time.Now()
	usedRenderers, err := e.runRenderers(ctx, snapshot)
	if err != nil {
		return nil, fmt.Errorf("rendering: %w", err)
	}
	tRender = time.Since(tStage)
	snapshot.Meta.Renderers = usedRenderers
	log.Printf("[engine] produced %d artifacts using %d renderers", len(snapshot.Artifacts), len(usedRenderers))

	e.snapshot = snapshot
	log.Printf("[engine] snapshot generated in %s", duration)
	log.Printf("[engine] timings: walk=%s hash=%s extract=%s link=%s graph=%s explain=%s render=%s",
		tWalk.Round(time.Millisecond), tHash.Round(time.Millisecond), tExtract.Round(time.Millisecond),
		tLink.Round(time.Millisecond), tGraph.Round(time.Millisecond), tExplain.Round(time.Millisecond),
		tRender.Round(time.Millisecond))
	return snapshot, nil
}

// linkCrossRepo drops any previously-synthesized cross-repo facts and recomputes
// them over the full fact set, adding service nodes and consumer→provider edges.
// It is a no-op for single-repo snapshots (no cross-repo matches exist).
func (e *Engine) linkCrossRepo() {
	e.store.RemoveWhere(func(f facts.Fact) bool {
		if f.Props == nil {
			return false
		}
		return f.Props["synthetic"] == crossrepo.SyntheticMarker
	})

	links := crossrepo.ComputeLinks(e.store.All())
	if len(links) == 0 {
		return
	}
	e.store.Add(links...)

	services, edges := 0, 0
	for _, f := range links {
		switch f.Kind {
		case facts.KindService:
			services++
		case facts.KindDependency:
			edges++
		}
	}
	log.Printf("[engine] cross-repo links: %d service nodes, %d dependency edges", services, edges)
}

// flagUnmatchedRoutes marks each server route fact that no loaded client route
// resolves to with an "unmatched_by_clients" prop, and clears the prop on every
// other route, so the flag is recomputed idempotently on each (re-)link. The
// signal is only meaningful with 2+ repos loaded; for a single-repo snapshot the
// key set is empty and this pass simply clears any stale flags. Routes carrying
// the prop are the candidates the unused-routes explainer summarizes and that
// query_facts(kind=route, prop=unmatched_by_clients, prop_value=true) returns.
func (e *Engine) flagUnmatchedRoutes() {
	keys := crossrepo.UnmatchedServerRouteKeys(e.store.All())
	flagged := 0
	e.store.UpdateWhere(func(f *facts.Fact) {
		if f.Kind != facts.KindRoute {
			return
		}
		// A client-role route is a call site, never a served endpoint; never flag
		// it, even if it shares an identity with an unused server route.
		if f.Props != nil && f.Props["role"] == "client" {
			delete(f.Props, "unmatched_by_clients")
			return
		}
		if keys[crossrepo.RouteIdentity(*f)] {
			if f.Props == nil {
				f.Props = map[string]any{}
			}
			f.Props["unmatched_by_clients"] = true
			flagged++
			return
		}
		if f.Props != nil {
			delete(f.Props, "unmatched_by_clients")
		}
	})
	if flagged > 0 {
		log.Printf("[engine] flagged %d server route(s) unused by loaded clients", flagged)
	}
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
		// "**/<seg>/**" — ignore a directory named <seg> at ANY depth (and
		// everything under it). The literal-prefix branch below cannot handle this
		// because the leading "**/" is not a real path component; match by checking
		// whether any path segment equals <seg>. Also covers the top-level case.
		if strings.HasPrefix(pattern, "**/") && strings.HasSuffix(pattern, "/**") {
			seg := strings.TrimSuffix(strings.TrimPrefix(pattern, "**/"), "/**")
			if seg != "" && !strings.Contains(seg, "/") {
				for _, part := range strings.Split(relPath, "/") {
					if part == seg {
						return true
					}
				}
			}
		}

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

// runExtractors detects applicable extractors and runs them. When cache is
// non-nil, extractors implementing plugin.FileOwner have their facts reused
// whenever the files they depend on are unchanged since the last snapshot.
func (e *Engine) runExtractors(ctx context.Context, repoPath string, files []string, hashes map[string]string, cache *extractorCache) ([]string, error) {
	var usedNames []string

	var keys map[string]string
	if cache != nil {
		keys = computeExtractorKeys(e.extractors.All(), files, hashes)
	}

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

		// Reuse cached facts when this extractor's inputs are unchanged.
		if cache != nil {
			if key, ok := keys[ext.Name()]; ok {
				if cached, hit := cache.get(key); hit {
					e.store.Add(cached...)
					usedNames = append(usedNames, ext.Name())
					log.Printf("[engine] extractor %s: reused %d cached facts", ext.Name(), len(cached))
					continue
				}
			}
		}

		log.Printf("[engine] running extractor: %s", ext.Name())
		tExt := time.Now()
		extracted, err := ext.Extract(ctx, repoPath, files)
		if err != nil {
			log.Printf("[engine] extractor %s error: %v", ext.Name(), err)
			continue
		}

		// Cache the raw (pre-tagging) facts before the engine mutates them.
		if cache != nil {
			if key, ok := keys[ext.Name()]; ok {
				cache.put(key, extracted)
			}
		}

		e.store.Add(extracted...)
		usedNames = append(usedNames, ext.Name())
		log.Printf("[engine] extractor %s: emitted %d facts in %s", ext.Name(), len(extracted), time.Since(tExt).Round(time.Millisecond))
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

// computeFileHashes computes SHA-256 hashes for all files (used in snapshot
// metadata). This stays sequential on purpose: hashing is I/O-bound and already
// fast (~0.25s on Airflow), and parallelizing it measurably regressed — many
// concurrent random reads contend worse than the sequential reads the OS
// prefetches. The extraction parsing, not hashing, is the bottleneck worth
// parallelizing.
func (e *Engine) computeFileHashes(repoPath string, files []string) map[string]string {
	hashes := make(map[string]string, len(files))
	for _, relFile := range files {
		absFile := filepath.Join(repoPath, relFile)
		data, err := os.ReadFile(absFile)
		if err != nil {
			continue
		}
		h := sha256.Sum256(data)
		hashes[relFile] = hex.EncodeToString(h[:])
	}
	return hashes
}

// fileModTime returns the modification time of a file as an RFC3339 string.
func fileModTime(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339)
}
