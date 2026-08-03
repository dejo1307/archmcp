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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/explainers"
	"github.com/enola-labs/enola/internal/extractors"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/linkers/binders"
	"github.com/enola-labs/enola/internal/linkers/crossrepo"
	"github.com/enola-labs/enola/internal/renderers"
	"github.com/enola-labs/enola/internal/version"
	"github.com/enola-labs/enola/pkg/plugin"
)

// snapshotBundle is the immutable, reader-visible snapshot state. GenerateSnapshot
// builds a brand-new store off to the side and publishes a fresh bundle in a single
// atomic swap; once published, none of its fields are ever mutated again. Readers
// (Store/Snapshot/RepoPaths/ResolveFactFile) Load the current bundle lock-free and
// use it for as long as they like — a concurrent regeneration builds a different
// store and swaps the pointer, leaving the bundle an in-flight reader holds intact.
type snapshotBundle struct {
	store     *facts.Store      // immutable once published
	snapshot  *facts.Snapshot   // may be nil (no snapshot generated yet)
	repoPaths map[string]string // repo label -> absolute path; immutable once published, may be nil
}

// Engine orchestrates the snapshot generation pipeline.
type Engine struct {
	mu         sync.Mutex // serializes GenerateSnapshot calls and guards the build-scratch store
	cfg        *config.Config
	extractors *extractors.Registry
	explainers *explainers.Registry
	renderers  *renderers.Registry
	binders    *binders.Registry

	// store is BUILD SCRATCH: it is reassigned to a fresh store at the top of each
	// GenerateSnapshot and read only by the pipeline helpers, all under mu. It is
	// NOT the reader-visible store — that lives in `current` and is only swapped in
	// atomically once the build is complete. Never expose e.store to readers.
	store *facts.Store

	// current holds the published, immutable {store, snapshot, repoPaths} bundle.
	// Readers Load it lock-free; GenerateSnapshot Stores a new bundle to publish.
	current atomic.Pointer[snapshotBundle]

	// persistCache controls whether the per-extractor cache is written back to
	// disk after a snapshot. The read path is always active when caching is
	// enabled; one-shot --explain sets this false so it never touches .enola.
	persistCache bool
}

// New creates a new Engine with the given config.
// Extractors, explainers, and renderers must be registered after creation.
func New(cfg *config.Config) (*Engine, error) {
	// Normalize here as well as in config.Load: a config assembled in code — by a
	// test, by a wrapper, by anything that did not read a file — must get the same
	// derived ignore glob for its output directory, or it indexes its own artifacts.
	// Idempotent, so the file path pays nothing for it.
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}

	// The build-scratch store and the initial published store are the same empty
	// store, so AutoLoadSnapshot (which mutates Store() in place before serving)
	// and the first generate both start from a consistent, non-nil bundle.
	st := facts.NewStore()
	e := &Engine{
		cfg:          cfg,
		extractors:   extractors.NewRegistry(),
		explainers:   explainers.NewRegistry(),
		renderers:    renderers.NewRegistry(),
		binders:      binders.NewRegistry(),
		store:        st,
		persistCache: true,
	}
	e.current.Store(&snapshotBundle{store: st})
	return e, nil
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

// RegisterBinder adds a binder to the engine. Binders run in the link stage, each in
// the stage it declares; see plugin.Binder.
func (e *Engine) RegisterBinder(b plugin.Binder) {
	e.binders.Register(b)
}

// Store returns the published fact store. The returned store is immutable for as
// long as the caller uses it: a concurrent regeneration builds a different store
// and swaps the published bundle, so it never mutates the store handed out here.
func (e *Engine) Store() *facts.Store {
	return e.current.Load().store
}

// Snapshot returns the last published snapshot, or nil. Lock-free: the returned
// snapshot is immutable once published.
func (e *Engine) Snapshot() *facts.Snapshot {
	return e.current.Load().snapshot
}

// Config returns the engine config.
func (e *Engine) Config() *config.Config {
	return e.cfg
}

// SetRepoPaths sets the repo label -> absolute path mapping (used in tests). It
// republishes the bundle preserving the current store and snapshot.
func (e *Engine) SetRepoPaths(paths map[string]string) {
	b := e.current.Load()
	e.current.Store(&snapshotBundle{store: b.store, snapshot: b.snapshot, repoPaths: paths})
}

// SetSnapshot sets the snapshot (used in tests, and by AutoLoadSnapshot at
// startup). It republishes the bundle preserving the current store and repoPaths.
func (e *Engine) SetSnapshot(snap *facts.Snapshot) {
	b := e.current.Load()
	e.current.Store(&snapshotBundle{store: b.store, snapshot: snap, repoPaths: b.repoPaths})
}

// RestoreFromDir rebuilds and publishes the snapshot bundle from a persisted
// snapshot directory (facts.jsonl plus, when present, insights.json and
// snapshot.meta.json) WITHOUT re-running any extractor. It is the restart-restore
// counterpart to GenerateSnapshot: it reads facts into a brand-new store off to the
// side, builds the graph, then publishes a fresh bundle in one atomic swap — so no
// reader can ever observe a half-built store.
//
// repoPaths is the graph's label -> absolute-path map (one entry for a single-repo
// graph). When singleRepoLabel is non-empty, any untagged facts are labeled with it
// (a single-repo facts.jsonl carries no repo label); a multi-repo facts.jsonl is
// already tagged, so pass an empty singleRepoLabel to preserve the baked-in labels.
//
// Missing insights/meta are tolerated (a partial restore still serves facts); a
// missing facts.jsonl is an error. It MUST run single-threaded at startup, before
// Server.Run begins serving — like SetSnapshot, it publishes a new bundle.
func (e *Engine) RestoreFromDir(dir string, repoPaths map[string]string, singleRepoLabel string) error {
	factsPath := filepath.Join(dir, "facts.jsonl")
	if _, err := os.Stat(factsPath); err != nil {
		return fmt.Errorf("no snapshot at %s: %w", dir, err)
	}

	work := facts.NewStore()
	if err := work.ReadJSONLFile(factsPath); err != nil {
		return fmt.Errorf("reading facts from %s: %w", factsPath, err)
	}
	if singleRepoLabel != "" {
		// Tags only facts whose Repo is empty, so a pre-tagged file is left intact.
		work.SetRepoRange(0, singleRepoLabel)
	}
	work.BuildGraph()

	// Default the primary repo path from the dir; snapshot.meta.json (loaded below)
	// overrides it when present.
	snap := &facts.Snapshot{Meta: facts.SnapshotMeta{RepoPath: filepath.Dir(dir)}}
	if data, err := os.ReadFile(filepath.Join(dir, "insights.json")); err == nil {
		var ins []facts.Insight
		if err := json.Unmarshal(data, &ins); err == nil {
			snap.Insights = ins
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "snapshot.meta.json")); err == nil {
		var meta facts.SnapshotMeta
		if err := json.Unmarshal(data, &meta); err == nil {
			snap.Meta = meta
		}
	}
	// FactsRef aliases the store's slice (never copied): `work` is published here and
	// then never mutated again, so a reader iterating snap.Facts sees a frozen array.
	snap.Facts = work.FactsRef()

	e.current.Store(&snapshotBundle{store: work, snapshot: snap, repoPaths: repoPaths})
	log.Printf("[engine] restored %d facts, %d insights from %s", work.Count(), len(snap.Insights), dir)
	return nil
}

// RepoPaths returns a copy of the repo label -> absolute path mapping (populated in
// append mode). Lock-free bundle load; the copy lets callers retain it safely.
func (e *Engine) RepoPaths() map[string]string {
	b := e.current.Load()
	if b.repoPaths == nil {
		return nil
	}
	cp := make(map[string]string, len(b.repoPaths))
	for k, v := range b.repoPaths {
		cp[k] = v
	}
	return cp
}

// ResolveFactFile returns the absolute filesystem path for a fact's File field.
// In multi-repo mode, it strips the repo-label prefix and joins with the
// corresponding repo root. In single-repo mode it falls back to the snapshot's
// RepoPath.
func (e *Engine) ResolveFactFile(f *facts.Fact) string {
	// Load the bundle once so repoPaths and snapshot are read as a consistent pair.
	b := e.current.Load()

	// Multi-repo: if the fact has a Repo label that maps to a known path,
	// strip the repo prefix from f.File and join with the absolute root.
	if f.Repo != "" && b.repoPaths != nil {
		if absRoot, ok := b.repoPaths[f.Repo]; ok {
			rel := strings.TrimPrefix(f.File, f.Repo+"/")
			return filepath.Join(absRoot, rel)
		}
	}

	// Single-repo fallback.
	if b.snapshot != nil {
		return filepath.Join(b.snapshot.Meta.RepoPath, f.File)
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

	// Build into a BRAND-NEW store off to the side. The currently-published bundle
	// (prev) is never mutated, so any in-flight reader keeps iterating a consistent,
	// frozen snapshot while we regenerate. We publish the new store atomically at the
	// end (see e.current.Store below), which is the single linearization point.
	prev := e.current.Load()
	work := facts.NewStore()
	var workRepoPaths map[string]string

	if appendMode {
		// Carry prior repos forward. All() returns an independent COPY, so mutating
		// `work` (TagUntagged/TagRange/SetRepoRange) can never touch prev.store, which
		// stays published and readable until we swap. This is the transient ~1x
		// fact-set memory cost of lock-free reads in append mode.
		workRepoPaths = make(map[string]string, len(prev.repoPaths)+1)
		for k, v := range prev.repoPaths {
			workRepoPaths[k] = v
		}
		if prev.store != nil {
			work.Add(prev.store.All()...)
		}

		// Track repo label -> absolute path for multi-repo resolution.
		workRepoPaths[repoLabel] = absRepo

		// Retroactively tag facts from a prior single-repo snapshot so they
		// are filterable by repo alongside the newly appended facts.
		if prev.snapshot != nil && work.Count() > 0 {
			prevLabel := filepath.Base(prev.snapshot.Meta.RepoPath)
			if _, alreadyTracked := workRepoPaths[prevLabel]; !alreadyTracked {
				tagged := work.TagUntagged(prevLabel, prevLabel+"/")
				if tagged > 0 {
					workRepoPaths[prevLabel] = prev.snapshot.Meta.RepoPath
					log.Printf("[engine] retroactively tagged %d existing facts with repo label %q", tagged, prevLabel)
				}
			}
		}
	}
	// Non-append: `work` stays empty and workRepoPaths stays nil — the prior bundle
	// is left intact for in-flight readers until the swap (no in-place Clear()).

	// Point the build-scratch store at `work` so the pipeline helpers (which read
	// e.store under e.mu) operate on the new store with no signature changes.
	e.store = work

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
	usedExtractors, shadowedExtractors, parseErrs, err := e.runExtractors(ctx, absRepo, files, currentHashes, cache)
	if err != nil {
		return nil, fmt.Errorf("extraction: %w", err)
	}
	e.reportShadowed(absRepo, shadowedExtractors)
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
	e.runTestRefExtractors(ctx, absRepo, testFiles, files)

	tExtract = time.Since(tStage)
	newCount := e.store.Count()
	log.Printf("[engine] extracted %d facts using %d extractors", newCount, len(usedExtractors))

	// Flag facts from codegen output before the file paths are repo-prefixed below,
	// while f.File is still resolvable against repoPath.
	e.markGeneratedFacts(absRepo, preCount)

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
	e.runBinders(ctx, plugin.StagePreLink)
	e.linkCrossRepo(workRepoPaths)
	e.runBinders(ctx, plugin.StagePostLink)
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
			Git:          gitInfo(absRepo, e.cfg.Output.Dir),
			ConfigHash:   configHash,

			FilesSeen:          len(files),
			FilesParsed:        e.store.CountFilesWithFacts(files, parsedPrefix),
			SourceBytes:        e.store.SourceBytesWithFacts(files, parsedPrefix, absRepo),
			FilesSkipped:       skips.count,
			DirsSkipped:        skips.dirCount,
			SkippedSample:      skips.sample,
			IgnoreGlobHash:     ignoreGlobHash,
			ShadowedExtractors: shadowedExtractors,
			ParseErrors:        len(parseErrs),
			ParseErrorSample:   capParseErrors(parseErrs),
			HeuristicInsights:  countHeuristicInsights(allInsights),
			Coverage:           coverageSummary(e.store),
		},
		// FactsRef aliases the store's slice rather than copying it. This is safe:
		// `work` is published (below) and then NEVER mutated again — the next
		// regeneration builds a different store — so a reader iterating snapshot.Facts
		// iterates a frozen backing array. Copying every fact would just double
		// steady-state RSS for a large repo. Baselines, which must stay immutable as
		// the store regenerates, still use the copying All().
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

	// Publish atomically. This single Store() is the linearization point: before it
	// readers see the prior bundle, after it the new one — never a half-built store.
	e.current.Store(&snapshotBundle{store: work, snapshot: snapshot, repoPaths: workRepoPaths})
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

// runBinders runs every registered binder declaring the given stage, over the
// build-scratch store.
//
// Order within a stage is registration order, but carries no meaning: the plugin.Binder
// contract requires each binder to be independent of whether its stage-mates have run,
// precisely so this loop can stay a loop. A binder that needs to observe another's output
// is in the wrong stage.
//
// A binder error is logged and the run continues, matching how a failing explainer is
// handled: a binder resolves edges that enrich the graph, so losing one degrades the
// snapshot but does not invalidate the facts already extracted. Failing the whole
// snapshot would trade a partial graph for none at all.
func (e *Engine) runBinders(ctx context.Context, stage plugin.BindStage) {
	for _, b := range e.binders.Stage(stage) {
		if err := b.Bind(ctx, e.store); err != nil {
			log.Printf("[engine] binder %s (%s) error: %v", b.Name(), stage, err)
		}
	}
}

// sourceReaderFor builds the crossrepo.SourceReader used to verify shared type names
// against the code behind them. It mirrors ResolveFactFile's repo-prefix-strip-and-join,
// but reads from the passed-in map rather than the published bundle (see linkCrossRepo).
// Returns nil when no repo paths are known, which turns verification off rather than
// silently comparing nothing. Files are read at most once per snapshot.
func sourceReaderFor(repoPaths map[string]string) crossrepo.SourceReader {
	if len(repoPaths) == 0 {
		return nil
	}
	cache := map[string]string{}
	missing := map[string]bool{}
	return func(f facts.Fact) (string, bool) {
		if f.File == "" || f.Repo == "" {
			return "", false
		}
		if text, ok := cache[f.File]; ok {
			return text, true
		}
		if missing[f.File] {
			return "", false
		}
		root, ok := repoPaths[f.Repo]
		if !ok {
			missing[f.File] = true
			return "", false
		}
		abs := filepath.Join(root, strings.TrimPrefix(f.File, f.Repo+"/"))
		data, err := os.ReadFile(abs)
		if err != nil {
			missing[f.File] = true
			return "", false
		}
		cache[f.File] = string(data)
		return cache[f.File], true
	}
}

// linkCrossRepo drops any previously-synthesized cross-repo facts and recomputes
// them over the full fact set, adding service nodes and consumer→provider edges.
// It is a no-op for single-repo snapshots (no cross-repo matches exist).
//
// repoPaths must be the IN-FLIGHT label -> absolute path map, not the published
// bundle's: this runs mid-snapshot, before the new bundle is stored, so
// ResolveFactFile would not yet know the repo currently being appended. It is used to
// read source for shared-symbol verification; a nil or incomplete map degrades that
// check to name-only matching rather than failing.
func (e *Engine) linkCrossRepo(repoPaths map[string]string) {
	e.store.RemoveWhere(func(f facts.Fact) bool {
		if f.Props == nil {
			return false
		}
		return f.Props["synthetic"] == crossrepo.SyntheticMarker
	})

	links := crossrepo.ComputeLinks(e.store.All(), sourceReaderFor(repoPaths))
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
//
// It also reports the SHADOWED extractors: those registered and applicable to this
// repository, but excluded by an explicit `extractors:` list. See reportShadowed.
func (e *Engine) runExtractors(ctx context.Context, repoPath string, files []string, hashes map[string]string, cache *extractorCache) ([]string, []string, []facts.ParseError, error) {
	var usedNames []string
	var shadowed []string
	var parseErrs []facts.ParseError

	var keys map[string]string
	if cache != nil {
		keys = computeExtractorKeys(e.extractors.All(), files, hashes)
	}

	for _, ext := range e.extractors.All() {
		if !e.cfg.IsExtractorEnabled(ext.Name()) {
			// Disabled by config. Detect anyway when the list was written by hand,
			// so the one case worth a warning — a language present in the repo that
			// this config will not extract — is named rather than left as an absence.
			// Detect is a cheap file-presence probe, and only disabled extractors
			// reach here, so this costs nothing on a config that enables everything.
			if e.cfg.ExtractorsExplicit {
				if detected, err := ext.Detect(repoPath); err == nil && detected {
					shadowed = append(shadowed, ext.Name())
				}
			}
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

	return usedNames, shadowed, parseErrs, nil
}

// reportShadowed warns about extractors that would have contributed facts but were
// excluded by an explicit `extractors:` list.
//
// An explicit list replaces the defaults rather than extending them, so a config
// written before an extractor existed disables it permanently — and a disabled
// extractor is not merely quiet, it never appears in the log at all. The failure
// looks exactly like a repository with nothing to find: a 780-file Rust repo
// reported 0 facts, no error, no mention of Rust. This is the line that names it.
func (e *Engine) reportShadowed(repoPath string, shadowed []string) {
	if len(shadowed) == 0 {
		return
	}
	sort.Strings(shadowed)
	where := "your config"
	if e.cfg.SourcePath != "" {
		where = e.cfg.SourcePath
	}
	log.Printf("[engine] warning: extractor(s) %s detected %s but are absent from the extractors: list in %s — they contribute no facts. An explicit extractors: list REPLACES the built-in defaults; add them to index these languages.",
		strings.Join(shadowed, ", "), repoPath, where)
}

// runTestRefExtractors runs reference-only extraction over the test/spec files
// for every enabled, detected extractor that implements plugin.TestRefExtractor.
// It scopes each extractor to the test files it owns and adds the resulting
// KindTestRef facts to the store. Errors are logged, not fatal.
//
// prodFiles is the same non-test file list handed to runExtractors, forwarded so an
// extractor whose reference resolution depends on which production modules exist
// (Python — see plugin.TestRefExtractor) does not have to re-walk the repo and
// re-implement the ignore globs to find out.
func (e *Engine) runTestRefExtractors(ctx context.Context, repoPath string, testFiles, prodFiles []string) {
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
		// prodFiles is passed whole, unscoped by FileOwner: an extractor that needs
		// it uses it to decide whether a referenced module EXISTS, and the answer
		// must not depend on which extractor owns the file.
		refFacts, err := tr.ExtractTestRefs(ctx, repoPath, owned, prodFiles)
		if err != nil {
			log.Printf("[engine] extractor %s test-ref error: %v", ext.Name(), err)
			continue
		}
		e.store.Add(refFacts...)
		log.Printf("[engine] extractor %s: emitted %d test-ref facts from %d files", ext.Name(), len(refFacts), len(owned))
	}
}

// runAnnotators lets enabled explainers write derived values back onto the facts
// before any of them run Explain.
//
// Two orderings matter and neither is incidental. It runs AFTER the graph is built, so
// whole-graph derivations (afferent/efferent coupling) are computable at all; and it runs
// BEFORE every Explain rather than interleaved per explainer, so one explainer's insights
// can never depend on whether another happened to be registered ahead of it — which would
// make the snapshot depend on registration order.
//
// An annotator failure is logged and skipped, exactly as an explainer failure is: a
// missing derived prop costs a diff some detail, and refusing to produce a snapshot over
// it would cost the caller everything else in the graph.
func (e *Engine) runAnnotators(ctx context.Context) {
	for _, exp := range e.explainers.All() {
		ann, ok := exp.(plugin.Annotator)
		if !ok || !e.cfg.IsExplainerEnabled(exp.Name()) {
			continue
		}
		log.Printf("[engine] running annotator: %s", exp.Name())
		if err := ann.Annotate(ctx, e.store); err != nil {
			log.Printf("[engine] annotator %s error: %v", exp.Name(), err)
		}
	}
}

// runExplainers runs all enabled explainers.
func (e *Engine) runExplainers(ctx context.Context) ([]facts.Insight, []string, error) {
	var allInsights []facts.Insight
	var usedNames []string

	e.runAnnotators(ctx)

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
	// Load the published bundle once and read only from it. The bundle is immutable,
	// so serializing facts/insights/meta here is race-free even if another generate
	// publishes a newer bundle meanwhile.
	b := e.current.Load()
	if b.snapshot == nil {
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
	for _, a := range b.snapshot.Artifacts {
		path := filepath.Join(outDir, a.Name)
		if err := os.WriteFile(path, a.Content, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", a.Name, err)
		}
		outputHashes[a.Name] = hashBytes(a.Content)
		log.Printf("[engine] wrote %s (%d bytes)", path, len(a.Content))
	}

	// Write facts.jsonl (serialize to a buffer first so we can hash the exact bytes)
	var factsBuf bytes.Buffer
	if err := b.store.WriteJSONL(&factsBuf); err != nil {
		return fmt.Errorf("serializing facts.jsonl: %w", err)
	}
	factsPath := filepath.Join(outDir, "facts.jsonl")
	if err := os.WriteFile(factsPath, factsBuf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing facts.jsonl: %w", err)
	}
	outputHashes["facts.jsonl"] = hashBytes(factsBuf.Bytes())
	log.Printf("[engine] wrote %s", factsPath)

	// Write insights.json. A nil slice marshals to `null`, not `[]`, so a repository
	// with no findings produced a document that breaks any consumer iterating the
	// parsed value without a nil check — on exactly the repositories least likely to
	// be used while testing a consumer.
	insights := b.snapshot.Insights
	if insights == nil {
		insights = []facts.Insight{}
	}
	insightsJSON, err := json.MarshalIndent(insights, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling insights: %w", err)
	}
	insightsPath := filepath.Join(outDir, "insights.json")
	if err := os.WriteFile(insightsPath, insightsJSON, 0o644); err != nil {
		return fmt.Errorf("writing insights.json: %w", err)
	}
	outputHashes["insights.json"] = hashBytes(insightsJSON)
	log.Printf("[engine] wrote %s (%d bytes)", insightsPath, len(insightsJSON))

	// Record the output hashes on a COPY of the meta rather than mutating the
	// published (shared, immutable) snapshot in place. SnapshotMeta is a value type,
	// so this copy shares its slices but overrides only OutputHashes.
	meta := b.snapshot.Meta
	meta.OutputHashes = outputHashes

	// Write snapshot.meta.json (the internal superset, incl. per-file hashes)
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling meta: %w", err)
	}
	metaPath := filepath.Join(outDir, "snapshot.meta.json")
	if err := os.WriteFile(metaPath, metaJSON, 0o644); err != nil {
		return fmt.Errorf("writing snapshot.meta.json: %w", err)
	}
	log.Printf("[engine] wrote %s (%d bytes)", metaPath, len(metaJSON))

	// Write receipt.json (the compact provenance + quality manifest)
	receiptJSON, err := json.MarshalIndent(meta.Receipt(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling receipt: %w", err)
	}
	receiptPath := filepath.Join(outDir, "receipt.json")
	if err := os.WriteFile(receiptPath, receiptJSON, 0o644); err != nil {
		return fmt.Errorf("writing receipt.json: %w", err)
	}
	log.Printf("[engine] wrote %s (%d bytes)", receiptPath, len(receiptJSON))

	// Republish so in-memory receipt tools (snapshot_receipt/compare_receipts) reflect
	// the output hashes, WITHOUT mutating the snapshot we just read. Only publish if
	// the bundle hasn't been superseded by a concurrent generate in the meantime —
	// generate-vs-generate is serialized (e.mu) and the server serializes the whole
	// generate handler (genMu), so under normal use this always succeeds; the guard
	// just avoids clobbering a newer snapshot in any unexpected interleaving.
	if e.current.Load() == b {
		snapCopy := *b.snapshot
		snapCopy.Meta = meta
		e.current.CompareAndSwap(b, &snapshotBundle{store: b.store, snapshot: &snapCopy, repoPaths: b.repoPaths})
	}

	return nil
}

// hashBytes returns the "sha256:"-prefixed digest of b, used for output-artifact
// digests in the receipt (matching every other receipt hash's notation).
func hashBytes(b []byte) string {
	return sha256Prefixed(b)
}

// GetArtifact returns the content of a named artifact, or the generated JSONL/JSON files.
func (e *Engine) GetArtifact(name string) ([]byte, error) {
	b := e.current.Load()
	if b.snapshot == nil {
		return nil, fmt.Errorf("no snapshot generated")
	}

	switch name {
	case "facts.jsonl":
		var buf bytes.Buffer
		if err := b.store.WriteJSONL(&buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case "insights.json":
		return json.MarshalIndent(b.snapshot.Insights, "", "  ")
	case "snapshot.meta.json":
		return json.MarshalIndent(b.snapshot.Meta, "", "  ")
	case "receipt.json":
		return json.MarshalIndent(b.snapshot.Meta.Receipt(), "", "  ")
	default:
		for _, a := range b.snapshot.Artifacts {
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
