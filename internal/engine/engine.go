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
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/explainers"
	"github.com/enola-labs/enola/internal/extractors"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/linkers/crossrepo"
	"github.com/enola-labs/enola/internal/renderers"
	"github.com/enola-labs/enola/internal/version"
	"github.com/enola-labs/enola/pkg/plugin"
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
	files, testFiles, skips, err := e.walkRepo(absRepo)
	if err != nil {
		return nil, fmt.Errorf("walking repo: %w", err)
	}
	tWalk = time.Since(tStage)
	log.Printf("[engine] found %d files (%d test files, %d skipped) in %s", len(files), len(testFiles), skips.count, absRepo)

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
	usedExtractors, parseErrs, err := e.runExtractors(ctx, absRepo, files, currentHashes, cache)
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
	// Reference-only extraction over test/spec files. Runs every snapshot (not
	// cached with the main extractors) and adds only KindTestRef facts, so a
	// production symbol exercised solely by a test is not mis-reported as dead.
	e.runTestRefExtractors(ctx, absRepo, testFiles)

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
	e.resolvePyGRPCClientRoutes()
	e.linkCrossRepo()
	e.flagUnmatchedRoutes()
	e.bindGRPCHandlers()
	e.bindHTTPHandlers()
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

	// 6. Build snapshot.
	//
	// Receipt fields: the snapshot ID is a content fingerprint over the same
	// byte-stable serialization that becomes facts.jsonl, so it is stable across
	// reruns on identical inputs and keys snapshot equivalence. The extraction
	// -quality fields (files seen/parsed/skipped, parse errors, coverage) let a
	// consumer judge how complete this extraction was before trusting it.
	duration := time.Since(start)
	ignoreGlobHash := computeIgnoreGlobHash(e.cfg)
	configHash := computeConfigHash(e.cfg)
	// In append mode this run's facts were path-prefixed with the repo label, so
	// match the walked files against that same prefix to count parsing coverage.
	parsedPrefix := ""
	if appendMode {
		parsedPrefix = repoLabel + "/"
	}
	var factsBuf bytes.Buffer
	if err := e.store.WriteJSONL(&factsBuf); err != nil {
		return nil, fmt.Errorf("serializing facts for snapshot id: %w", err)
	}
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

			EnolaVersion: version.Version,
			SnapshotID:   computeSnapshotID(factsBuf.Bytes(), version.Version, configHash),
			Git:          gitInfo(absRepo),
			ConfigHash:   configHash,

			FilesSeen:         len(files),
			FilesParsed:       e.store.CountFilesWithFacts(files, parsedPrefix),
			FilesSkipped:      skips.count,
			DirsSkipped:       skips.dirCount,
			SkippedSample:     skips.sample,
			IgnoreGlobHash:    ignoreGlobHash,
			ParseErrors:       len(parseErrs),
			ParseErrorSample:  capParseErrors(parseErrs),
			HeuristicInsights: countHeuristicInsights(allInsights),
			Coverage:          coverageSummary(e.store),
		},
		// FactsRef aliases the store's slice rather than copying it: this snapshot
		// is the live one (e.snapshot) and its Facts are only ever read (renderers,
		// diff, query_insights), so a second full copy of every fact would just
		// double steady-state RSS for a large repo. Baselines, which must stay
		// immutable as the store regenerates, still use the copying All().
		Facts:    e.store.FactsRef(),
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

	// Generation allocates large transient buffers (per-file fact slices, the
	// pre-dedup fact list, parser scratch) that the GC frees but Go's scavenger
	// returns to the OS only lazily. For a long-running server that loads a big
	// repo and then idles, hand that memory back now so idle RSS settles at the
	// live set instead of the extraction peak. Once per load, so the cost is
	// negligible. The MemStats line reports the retained footprint for visibility.
	debug.FreeOSMemory()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	log.Printf("[engine] memory after snapshot: heap=%d MiB sys=%d MiB (%d facts)",
		ms.HeapAlloc>>20, ms.Sys>>20, snapshot.Meta.FactCount)

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

// flagUnmatchedRoutes marks each route fact with its cross-repo resolution verdict,
// recomputed idempotently on each (re-)link: a server route no loaded client calls
// gets "unmatched_by_clients" (the unused-routes candidates); a client call site
// that resolves to no loaded server route gets "unmatched_by_server" plus an
// "unmatched_reason" (one of crossrepo's Reason* constants: no_method | generic_path |
// method_mismatch | path_unknown) — the queryable counterpart to the aggregate
// coverage counts. Both signals are only meaningful
// with 2+ repos loaded; for a single-repo snapshot the key sets are empty and this
// pass simply clears any stale flags. Surfaced via
// query_facts(kind=route, prop=unmatched_by_clients|unmatched_by_server).
func (e *Engine) flagUnmatchedRoutes() {
	serverKeys := crossrepo.UnmatchedServerRouteKeys(e.store.All())
	clientKeys := crossrepo.UnmatchedClientRouteKeys(e.store.All())
	flaggedServer, flaggedClient := 0, 0
	e.store.UpdateWhere(func(f *facts.Fact) {
		if f.Kind != facts.KindRoute {
			return
		}
		// A client-role route is a call site, never a served endpoint: it carries the
		// reverse (unmatched_by_server) verdict, never unmatched_by_clients.
		if f.Props != nil && f.Props["role"] == "client" {
			delete(f.Props, "unmatched_by_clients")
			if reason, ok := clientKeys[crossrepo.RouteIdentity(*f)]; ok {
				f.Props["unmatched_by_server"] = true
				f.Props["unmatched_reason"] = reason
				flaggedClient++
			} else {
				delete(f.Props, "unmatched_by_server")
				delete(f.Props, "unmatched_reason")
			}
			return
		}
		if serverKeys[crossrepo.RouteIdentity(*f)] {
			if f.Props == nil {
				f.Props = map[string]any{}
			}
			f.Props["unmatched_by_clients"] = true
			flaggedServer++
			return
		}
		if f.Props != nil {
			delete(f.Props, "unmatched_by_clients")
		}
	})
	if flaggedServer > 0 || flaggedClient > 0 {
		log.Printf("[engine] flagged %d server route(s) unused by clients, %d client call(s) unresolved to a server", flaggedServer, flaggedClient)
	}
}

// walkSkips is a lightweight tally of what the ignore globs dropped, kept so a
// snapshot receipt can report how much of the tree was excluded (and a sample of
// what) without retaining every skipped path.
//
// Files and directories are tallied separately because they cost differently to
// know. An ignored directory is pruned whole — the walker never descends, so its
// contents are counted nowhere. Counting them would mean walking node_modules/
// purely to size it, a stat per file for a number no architecture graph wants.
// One pruned directory is one architecturally meaningful fact; its 55,041 files
// are not.
type walkSkips struct {
	count    int      // ignored FILES the walker visited
	dirCount int      // ignored DIRECTORIES pruned; their contents are never visited
	sample   []string // capped sample of both, each annotated with the glob that matched
}

// record appends "<path> (glob: <pattern>)" until the sample is full. Directories
// arrive with a trailing slash, which is what distinguishes a pruned subtree from
// a single dropped file in the receipt.
func (s *walkSkips) record(path, pattern string) {
	if len(s.sample) < skippedSampleCap {
		s.sample = append(s.sample, path+" (glob: "+pattern+")")
	}
}

// skippedSampleCap bounds the number of skipped paths retained for the receipt.
const skippedSampleCap = 20

// walkRepo collects all files in the repo, applying ignore patterns. It returns
// the indexable source files, separately the test/spec files matched by
// TestGlobs (excluded from normal indexing but collected for reference-only
// extraction — see runTestRefExtractors), and a tally of what the ignore globs
// dropped — ignored files, and pruned directories — for the snapshot receipt.
func (e *Engine) walkRepo(repoPath string) (files, testFiles []string, skips walkSkips, err error) {
	err = filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(repoPath, path)
		if err != nil {
			return err
		}

		// Skip ignored paths
		if pattern, ok := e.ignoreMatch(relPath); ok {
			if d.IsDir() {
				// enola's own output directory is not part of the source tree.
				// Counting it would make dirs_skipped differ between a repo's
				// first-ever snapshot and every one after it, for no signal.
				if relPath != e.cfg.Output.Dir {
					skips.dirCount++
					skips.record(filepath.ToSlash(relPath)+"/", pattern)
				}
				return filepath.SkipDir
			}
			// An ignored FILE that is a test/spec is not indexed as production
			// source, but is collected for reference-only extraction so a
			// production symbol exercised only by a test does not look dead.
			if e.matchesTestGlob(relPath) {
				testFiles = append(testFiles, relPath)
			}
			skips.count++
			skips.record(filepath.ToSlash(relPath), pattern)
			return nil
		}

		if !d.IsDir() {
			files = append(files, relPath)
		}
		return nil
	})
	return files, testFiles, skips, err
}

// matchesTestGlob reports whether a repo-relative path matches any TestGlob.
func (e *Engine) matchesTestGlob(relPath string) bool {
	return matchAnyGlob(filepath.ToSlash(relPath), e.cfg.TestGlobs)
}

// matchAnyGlob and matchGlob are thin aliases onto the shared matcher, which now
// lives in internal/facts alongside the other path predicates (IsTestPath) so that
// out-of-module consumers can reach it through pkg/facts. The enterprise performance
// analyzer's ENOLA_PERF_EXCLUDE globs documented `**` support that path.Match cannot
// give them; rather than grow a second glob implementation, it uses this one.
func matchAnyGlob(relPath string, patterns []string) bool {
	return facts.MatchAnyGlob(relPath, patterns)
}

func matchGlob(relPath string, patterns []string) (string, bool) {
	return facts.MatchGlob(relPath, patterns)
}

// isIgnored checks whether a path matches any ignore pattern. isDir is unused: the
// patterns discriminate on shape, not on file type, and a directory that matches is
// pruned by the caller.
func (e *Engine) isIgnored(relPath string, isDir bool) bool {
	return matchAnyGlob(filepath.ToSlash(relPath), e.cfg.Ignore)
}

// ignoreMatch reports whether a path is ignored, and by which pattern. The walker
// needs the pattern to record it in the receipt's skipped sample.
func (e *Engine) ignoreMatch(relPath string) (string, bool) {
	return matchGlob(filepath.ToSlash(relPath), e.cfg.Ignore)
}

// runExtractors detects applicable extractors and runs them. When cache is
// non-nil, extractors implementing plugin.FileOwner have their facts reused
// whenever the files they depend on are unchanged since the last snapshot.
func (e *Engine) runExtractors(ctx context.Context, repoPath string, files []string, hashes map[string]string, cache *extractorCache) ([]string, []facts.ParseError, error) {
	var usedNames []string
	var parseErrs []facts.ParseError

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
			parseErrs = append(parseErrs, facts.ParseError{Extractor: ext.Name(), Msg: "detect: " + err.Error()})
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
			parseErrs = append(parseErrs, facts.ParseError{Extractor: ext.Name(), Msg: err.Error()})
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

	return usedNames, parseErrs, nil
}

// runTestRefExtractors runs reference-only extraction over the test/spec files
// for every enabled, detected extractor that implements plugin.TestRefExtractor.
// It scopes each extractor to the test files it owns and adds the resulting
// KindTestRef facts to the store. Errors are logged, not fatal.
func (e *Engine) runTestRefExtractors(ctx context.Context, repoPath string, testFiles []string) {
	if len(testFiles) == 0 {
		return
	}
	for _, ext := range e.extractors.All() {
		if !e.cfg.IsExtractorEnabled(ext.Name()) {
			continue
		}
		tr, ok := ext.(plugin.TestRefExtractor)
		if !ok {
			continue
		}
		if detected, err := ext.Detect(repoPath); err != nil || !detected {
			continue
		}
		owned := testFiles
		if fo, ok := ext.(plugin.FileOwner); ok {
			owned = owned[:0:0]
			for _, f := range testFiles {
				if fo.OwnsFile(f) {
					owned = append(owned, f)
				}
			}
		}
		if len(owned) == 0 {
			continue
		}
		refFacts, err := tr.ExtractTestRefs(ctx, repoPath, owned)
		if err != nil {
			log.Printf("[engine] extractor %s test-ref error: %v", ext.Name(), err)
			continue
		}
		e.store.Add(refFacts...)
		log.Printf("[engine] extractor %s: emitted %d test-ref facts from %d files", ext.Name(), len(refFacts), len(owned))
	}
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

		// Tag each insight with its producing explainer so clients can fetch and
		// filter findings by source (e.g. query_insights(explainer="unused-routes"))
		// without every explainer having to set the field itself.
		for i := range insights {
			insights[i].Source = exp.Name()
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

	// Rotate the prior snapshot into previous/ before overwriting, so diff_snapshot
	// can compare against the immediately-preceding run with no explicit pin. The
	// pinned baseline/ (SetBaseline) is left untouched here.
	if err := rotatePrevious(outDir); err != nil {
		log.Printf("[engine] warning: could not rotate previous snapshot: %v", err)
	}

	// Hash every written artifact so the receipt records the exact output bytes
	// (the verifiable counterpart to the per-input-file FileHashes). meta.json and
	// receipt.json themselves are not hashed — they carry these hashes.
	outputHashes := make(map[string]string)

	// Write renderer artifacts (e.g. llm_context.md)
	for _, a := range e.snapshot.Artifacts {
		path := filepath.Join(outDir, a.Name)
		if err := os.WriteFile(path, a.Content, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", a.Name, err)
		}
		outputHashes[a.Name] = hashBytes(a.Content)
		log.Printf("[engine] wrote %s (%d bytes)", path, len(a.Content))
	}

	// Write facts.jsonl (serialize to a buffer first so we can hash the exact bytes)
	var factsBuf bytes.Buffer
	if err := e.store.WriteJSONL(&factsBuf); err != nil {
		return fmt.Errorf("serializing facts.jsonl: %w", err)
	}
	factsPath := filepath.Join(outDir, "facts.jsonl")
	if err := os.WriteFile(factsPath, factsBuf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing facts.jsonl: %w", err)
	}
	outputHashes["facts.jsonl"] = hashBytes(factsBuf.Bytes())
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
	outputHashes["insights.json"] = hashBytes(insightsJSON)
	log.Printf("[engine] wrote %s (%d bytes)", insightsPath, len(insightsJSON))

	// Record the output hashes on the snapshot meta before serializing it.
	e.snapshot.Meta.OutputHashes = outputHashes

	// Write snapshot.meta.json (the internal superset, incl. per-file hashes)
	metaJSON, err := json.MarshalIndent(e.snapshot.Meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling meta: %w", err)
	}
	metaPath := filepath.Join(outDir, "snapshot.meta.json")
	if err := os.WriteFile(metaPath, metaJSON, 0o644); err != nil {
		return fmt.Errorf("writing snapshot.meta.json: %w", err)
	}
	log.Printf("[engine] wrote %s (%d bytes)", metaPath, len(metaJSON))

	// Write receipt.json (the compact provenance + quality manifest)
	receiptJSON, err := json.MarshalIndent(e.snapshot.Meta.Receipt(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling receipt: %w", err)
	}
	receiptPath := filepath.Join(outDir, "receipt.json")
	if err := os.WriteFile(receiptPath, receiptJSON, 0o644); err != nil {
		return fmt.Errorf("writing receipt.json: %w", err)
	}
	log.Printf("[engine] wrote %s (%d bytes)", receiptPath, len(receiptJSON))

	return nil
}

// hashBytes returns the "sha256:"-prefixed digest of b, used for output-artifact
// digests in the receipt (matching every other receipt hash's notation).
func hashBytes(b []byte) string {
	return sha256Prefixed(b)
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
	case "receipt.json":
		return json.MarshalIndent(e.snapshot.Meta.Receipt(), "", "  ")
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
