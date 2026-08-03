package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/enola-labs/enola/internal/linkers/vocab"
)

// Config represents the mcp-arch.yaml configuration.
type Config struct {
	Repo string `yaml:"repo"`

	// Repos is the ordered list of repositories that form a multi-repo cluster.
	// When set it supersedes Repo, and the whole cluster is indexed in one run —
	// the first repository fresh, the rest appended.
	//
	// It exists because cross-repo linking was previously reachable only from an
	// MCP session: `append` is a generate_snapshot tool parameter, and both CLIs
	// hardcoded false, so a CI job or a developer not driving an agent could only
	// ever see the single-repo subset (no service nodes, no cross-repo edges, no
	// coverage_report, no unused-routes). Naming the cluster in the config also
	// makes its composition a reviewable file rather than a property of the order
	// somebody happened to issue tool calls in.
	//
	// Entries are resolved relative to the DIRECTORY OF THIS FILE, not the working
	// directory, so a checked-in cluster config means the same thing wherever it is
	// run from. (Repo keeps its historical cwd-relative behaviour.)
	Repos []string `yaml:"repos"`

	// SourcePath is the file this config was read from, or "" when the built-in
	// defaults are in force. Not read from YAML — set by Load, and used to resolve
	// Repos entries against the config's own directory.
	SourcePath string `yaml:"-"`

	// ExtractorsExplicit reports whether the file named `extractors:` itself, as
	// opposed to inheriting Default()'s list. Not read from YAML — set by Load.
	//
	// It exists because the two cases are indistinguishable afterwards and mean
	// opposite things. An explicit list REPLACES the defaults (YAML unmarshalling of
	// a sequence overwrites the slice), so a config written before an extractor
	// existed permanently disables it — silently, since a disabled extractor is
	// simply never tried. Knowing the list was explicit lets the engine say so when
	// a disabled extractor would have detected the repository.
	ExtractorsExplicit bool `yaml:"-"`

	Ignore     []string     `yaml:"ignore"`
	TestGlobs  []string     `yaml:"test_globs"`
	Extractors []string     `yaml:"extractors"`
	Explainers []string     `yaml:"explainers"`
	Renderers  []string     `yaml:"renderers"`
	Output     OutputConfig `yaml:"output"`

	// Linking overlays the cross-repo linker's tuning vocabulary — the word lists that
	// decide which names are too generic to link on, and the numeric thresholds. It is
	// ADDITIVE over the built-in defaults (see vocab.Overlay), so fixing one false edge
	// cannot silently discard the rest.
	//
	// Changing it changes emitted facts, so it is folded into the snapshot's config
	// hash; two snapshots taken under different vocabularies are not comparable and the
	// receipt says so.
	Linking *vocab.Overlay `yaml:"linking,omitempty"`

	// Dashboard configures the localhost dashboard served alongside the MCP
	// server. Optional: the zero value keeps the built-in defaults.
	Dashboard DashboardConfig `yaml:"dashboard"`

	// History records each snapshot as a revision in an append-only architecture
	// history, so a repository has a timeline rather than only a current state and one
	// baseline. Optional; the zero value keeps the built-in defaults.
	//
	// Deliberately ABSENT from computeConfigHash: it changes what enola remembers, never
	// what it extracts. Folding it in would make every snapshot taken with history on
	// incomparable with every snapshot taken with it off — turning an experimental,
	// per-user setting into a reason to decline to grade somebody's change.
	History HistoryConfig `yaml:"history"`

	// Incremental enables per-extractor caching: an extractor's facts are reused
	// across snapshots when the files it owns (and the repo's shared config files)
	// are unchanged. Defaults to true. Set `incremental: false` to force a full
	// re-extraction every run.
	Incremental *bool `yaml:"incremental,omitempty"`

	// ChangeVerifyHint is optional extra guidance a host/wrapper can inject into the
	// change-verification surfaces — the server Instructions block and the runtime
	// post-snapshot nudge (loopHint). It lets a wrapper that registers additional
	// change-verification tools draw the agent toward them from the same
	// generate_snapshot → diff loop the OSS tools advertise. Not read from YAML; set
	// programmatically before the server is constructed. Empty by default (OSS).
	ChangeVerifyHint string `yaml:"-"`
}

// IncrementalEnabled reports whether per-extractor caching is on (the default).
func (c *Config) IncrementalEnabled() bool {
	return c.Incremental == nil || *c.Incremental
}

// DashboardConfig controls the localhost dashboard.
//
// Port is the fixed "shared URL" port every server competes for, so there is one
// address worth bookmarking even though each server also listens on an ephemeral
// port of its own. Zero means the built-in default; a negative value turns the
// shared URL off, leaving only the ephemeral port. The ENOLA_DASHBOARD_PORT
// environment variable overrides it.
type DashboardConfig struct {
	Port int `yaml:"port"`
}

// HistoryConfig controls the architecture history — the append-only record of the
// revisions a repository has passed through.
//
// Recording is ON by default, which is a deliberate reversal of how a feature like this
// usually ships. The questions a history exists to answer — when did this coupling
// appear, which revision introduced this cycle — are questions about the PAST, so
// opt-in guarantees that the first time anybody wants one, there is nothing to read.
// Reconstructing it afterwards means re-snapshotting the repository commit by commit,
// which is by far the most expensive thing in this feature. The data has to already be
// there.
//
// What makes that affordable is that a revision is a ~450-byte line, the working-revision
// ring bounds what an agent loop can produce, and the default location is outside the
// repository — so the cost of being wrong about this default is a few megabytes in a
// directory enola already owns, not a dirty git status or a surprise in someone's tree.
//
// It stays honest about enola's central rule (docs/SNAPSHOTS.md: everything enola writes
// is derivable, and nothing that judges the present may read an accumulated file) because
// that rule is enforced by tests rather than by leaving the feature switched off —
// TestVerdictPathsDoNotReadTheHistory and
// TestHistory_DeletingItChangesNothingAboutThePresent.
type HistoryConfig struct {
	// Enabled turns recording on. Nil (absent) means on; set it to false to stop
	// recording without removing what is already there.
	Enabled *bool `yaml:"enabled,omitempty"`

	// Dir overrides where the history is kept. Empty means outside the repository, under
	// ~/.enola/graphs/<workspace>/history — see history.Root for why that is the default.
	// A relative path resolves against the repository; set it when the history should
	// travel with the checkout (to commit it, or to publish it from CI).
	Dir string `yaml:"dir,omitempty"`

	// WorkingKeep caps how many unanchored revisions (dirty tree, or no git at all) are
	// kept per base commit. Zero means the built-in default; negative keeps every one.
	WorkingKeep int `yaml:"working_keep,omitempty"`
}

// HistoryEnabled reports whether snapshots are recorded as history (the default).
func (c *Config) HistoryEnabled() bool {
	return c.History.Enabled == nil || *c.History.Enabled
}

// OutputConfig controls where and how output artifacts are generated.
type OutputConfig struct {
	Dir              string `yaml:"dir"`
	MaxContextTokens int    `yaml:"max_context_tokens"`
}

// Default returns a Config with sensible defaults.
func Default() *Config {
	return &Config{
		Repo: ".",
		Ignore: []string{
			"vendor/**",
			"node_modules/**",
			".git/**",
			// Go reserves testdata/ for fixtures — the toolchain never compiles it.
			// Fixture repos are whole miniature codebases (clients, servers, routes),
			// so indexing them injects their call sites into the HOST repo's graph:
			// enola's own testdata/repos/** supplied all 9 of its detected outbound
			// HTTP call sites, manufacturing a coverage gap for a service that makes
			// no outbound calls at all. Any-depth form, and safe for fixture-driven
			// tests: those root each scan INSIDE the fixture (testdata/repos/<f>/<sub>),
			// so the scanned-relative paths contain no testdata/ segment to match.
			// Keep in sync with the bundled mcp-arch.yaml ignore list.
			"**/testdata/**",
			"**/*_test.go",
			"**/*.test.ts",
			"**/*.test.tsx",
			"**/*.spec.ts",
			"**/*.spec.tsx",
			// Ruby, unlike Go and TS, has no co-located test convention: RSpec
			// requires spec/, Minitest defaults to test/. Demand the directory as
			// well as the filename — a bare "**/*_test.rb" also swallows production
			// code that merely ends in the token (a job named cache_warmup_ab_test.rb),
			// deleting it from the graph.
			// Keep in sync with TestGlobs below: a file that stops being a test must
			// stop being ignored, or it is dropped without being recovered.
			"**/spec/**/*_spec.rb",
			"**/test/**/*_test.rb",
			// Python. Test files here are not merely noise to be filtered later: a
			// pytest fixture that assembles a throwaway app
			// (app.include_router(get_cognify_router(), prefix="/cognify")) is a
			// route-mount fact, and the repo-wide prefix fixpoint (v133) folds every
			// mount it can see. A test-only prefix therefore REWRITES production
			// routes — a real corpus gained six phantom endpoints its service never
			// served, which then matched a client call and mis-attributed a
			// cross-repo edge's evidence. Python test files were also ~35% of that
			// repo's total facts.
			//
			// conftest.py is reserved by pytest outright, and the test_ prefix is its
			// discovery convention (a production module so named would itself be
			// collected as a test), so both are safe at any depth. A whole tests/ tree
			// goes too, since fixtures and factories carry no test_ prefix.
			// Deliberately NOT a bare "**/*_test.py": that repeats the Ruby hazard
			// above of swallowing production code that merely ends in the token, and
			// inside a tests/ tree it is already covered.
			// Keep in sync with TestGlobs below: a file that stops being a test must
			// stop being ignored, or it is dropped without being recovered.
			"**/conftest.py",
			"**/test_*.py",
			"**/tests/**/*.py",
			"**/test/**/*.py",
			// enola's own output. This is the DEFAULT location only; the glob for the
			// configured one is derived in Normalize, which is what makes a custom
			// output.dir safe. The literal stays because a repository that used the
			// default before changing it still has artifacts here, and indexing its own
			// history is exactly the failure the derived glob exists to prevent.
			defaultOutputDir + "/**",
			// Build / cache artifacts. These are generated output (often transpiled
			// JS, e.g. Next.js .next/) and must never be indexed as source — doing so
			// pollutes query_facts with thousands of spurious facts. The **/<dir>/**
			// form matches the directory at ANY depth (e.g. Gradle/Android emit
			// data/build/kspCaches/...), not just at the repo root.
			".next/**",
			"dist/**",
			"**/build/**",
			"out/**",
			".vercel/**",
			".turbo/**",
			"coverage/**",
			".nuxt/**",
			".svelte-kit/**",
			"**/__pycache__/**",
			// Python virtual environments, installed dependencies, and tool caches.
			// A repo-local .venv/venv holds the entire dependency tree (thousands of
			// third-party .py files); indexing it is never wanted and dominates
			// snapshot time. site-packages is the definitive catch for any oddly-named
			// env (.tox/.nox/conda/direnv all nest one). Any-depth (**/x/**) form so
			// monorepo sub-project venvs are pruned too.
			"**/.venv/**",
			"**/venv/**",
			"**/site-packages/**",
			"**/.tox/**",
			"**/.nox/**",
			"**/.eggs/**",
			"**/.mypy_cache/**",
			"**/.pytest_cache/**",
			"**/.ruff_cache/**",
			"**/Pods/**",
			"**/.gradle/**",
			"**/target/**",
			// Minified / bundled JS by name. The extractor also detects minified
			// content heuristically (very long lines), but these globs cheaply skip
			// the common named cases before a file is ever read. Keep in sync with
			// the bundled mcp-arch.yaml ignore list.
			"**/*.min.js",
			"**/*.bundle.js",
		},
		// TestGlobs identify test/spec files. They stay ignored for normal indexing
		// (still listed in Ignore above) — production architecture facts must not
		// include test symbols — but the engine collects them separately for
		// reference-only extraction so the dead-code detector can see that a
		// production symbol is exercised by a test and not mis-report it as dead.
		// A glob here without an extractor implementing plugin.TestRefExtractor is a
		// no-op (engine.runTestRefExtractors skips non-implementers), so extend this
		// list only alongside the matching extractor. Go's and TypeScript's dotted
		// suffixes are correct: the toolchain/convention reserves *_test.go and
		// *.test.ts(x)/*.spec.ts(x) for tests, so — unlike Ruby's _test.rb (v97) — no
		// production file can collide with them.
		TestGlobs: []string{
			"**/*_test.go",
			"**/*.test.ts", "**/*.test.tsx", "**/*.spec.ts", "**/*.spec.tsx",
			"**/spec/**/*_spec.rb", "**/test/**/*_test.rb",
			// Python has no TestRefExtractor yet, so these four are a deliberate
			// no-op today: runTestRefExtractors skips non-implementers. They are
			// listed anyway because Ignore above requires the two lists to agree —
			// a file ignored here but absent from TestGlobs is dropped with no way
			// to recover it — and so that implementing PythonExtractor.ExtractTestRefs
			// switches the signal on without a second config change.
			// Until then, a Python symbol called only from a test reads as dead:
			// expect dead-code false positives on Python repos to rise.
			"**/conftest.py", "**/test_*.py",
			"**/tests/**/*.py", "**/test/**/*.py",
		},
		Extractors: []string{"cpp", "go", "grpc", "java", "kotlin", "openapi", "php", "python", "typescript", "swift", "ruby", "rust"},
		Explainers: []string{"cycles", "layers", "crossrepo", "coverage", "unused-routes", "god-class", "hotspots", "dependency-depth", "exported-surface", "complexity-outliers"},
		Renderers:  []string{"llm_context"},
		Output: OutputConfig{
			Dir:              defaultOutputDir,
			MaxContextTokens: 16000,
		},
	}
}

// Load reads a configuration file from the given path.
// Missing fields are filled with defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	// Whether the key was PRESENT cannot be recovered from cfg afterwards: an
	// absent `extractors:` leaves Default()'s list in place, and a list identical
	// to it unmarshals to the same value. A second pass with a pointer field is
	// the only way to tell "inherited the defaults" from "chose exactly these".
	var probe struct {
		Extractors *[]string `yaml:"extractors"`
	}
	if err := yaml.Unmarshal(data, &probe); err == nil {
		cfg.ExtractorsExplicit = probe.Extractors != nil
	}
	// Recorded so Repos entries resolve against the config's own directory; see
	// RepoPaths.
	if abs, err := filepath.Abs(path); err == nil {
		cfg.SourcePath = abs
	} else {
		cfg.SourcePath = path
	}

	if err := cfg.Normalize(); err != nil {
		return nil, fmt.Errorf("in config %s: %w", path, err)
	}
	return cfg, nil
}

// defaultOutputDir is where artifacts go unless output.dir says otherwise. It is
// also present as a literal in Default().Ignore, and deliberately so — see Normalize.
const defaultOutputDir = ".enola"

// Normalize fills in required defaults, validates them, and derives the settings
// that follow from other settings. Idempotent, and called both by Load and by the
// engine, so a config built in code gets the same treatment as one read from a file.
//
// Its real job is the output directory. `.enola/**` used to be a hard-coded literal
// in the default ignore list, sitting between `.next/**` and `dist/**` as though it
// were another build-artifact glob, and agreeing with Output.Dir only by coincidence.
// Point output.dir anywhere else and the next snapshot walked the previous one's
// artifacts — facts.jsonl, insights.json, llm_context.md, plus the previous/ rotation
// from run 2 onward — so an unchanged tree produced a different snapshot every run.
// Reproducibility is the property the baseline diff rests on, and this broke it for a
// reason that has nothing to do with enola's determinism, on a setting users are
// invited to change, with a symptom that points nowhere near the cause.
//
// The literal `.enola/**` stays in Default().Ignore as well: a repository that once
// used the default and later changed it must not start indexing its own history.
func (c *Config) Normalize() error {
	if c.Output.Dir == "" {
		c.Output.Dir = defaultOutputDir
	}
	if c.Output.MaxContextTokens == 0 {
		c.Output.MaxContextTokens = 16000
	}

	dir, err := cleanOutputDir(c.Output.Dir)
	if err != nil {
		return err
	}
	c.Output.Dir = dir

	glob := dir + "/**"
	if !contains(c.Ignore, glob) {
		c.Ignore = append(c.Ignore, glob)
	}
	return nil
}

// cleanOutputDir validates output.dir and returns it in slash form.
//
// It must be a subdirectory of the repository, because that is the only thing the
// rest of the code can express: Output.Dir is JOINED to the repository path in half a
// dozen places, so an absolute path silently produced a directory nested inside the
// repo (/repo/private/tmp/.../out) rather than the location asked for, and an ignore
// glob derived from it could not describe the artifacts either. An error naming the
// constraint beats a path that looks accepted and means something else.
func cleanOutputDir(dir string) (string, error) {
	if filepath.IsAbs(dir) {
		return "", fmt.Errorf("output.dir %q must be a path inside the repository, not an absolute one "+
			"(it is joined to the repository path, so an absolute value would nest the whole path inside the repo)", dir)
	}
	clean := filepath.ToSlash(filepath.Clean(dir))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("output.dir %q must name a subdirectory of the repository", dir)
	}
	return clean, nil
}

// RepoPaths returns the absolute repository paths this run covers, in the order
// they should be indexed: the first fresh, the rest appended.
//
// This is the single definition of "which repositories does this config describe",
// so the CLI, --explain and any wrapper agree. Two resolution rules, and the
// difference is deliberate:
//
//   - Repos entries resolve against the config file's own directory, because a
//     cluster config is meant to be checked in and to mean the same thing wherever
//     it is run from. With no config file (built-in defaults) there is no such
//     directory, so they fall back to the working directory.
//   - Repo resolves against the working directory, unchanged, because that is what
//     `repo: "."` has always meant.
//
// Returns exactly one path when Repos is empty, so a single-repo caller needs no
// special case.
func (c *Config) RepoPaths() ([]string, error) {
	if len(c.Repos) == 0 {
		abs, err := filepath.Abs(c.Repo)
		if err != nil {
			return nil, fmt.Errorf("resolving repo %q: %w", c.Repo, err)
		}
		return []string{abs}, nil
	}

	base := ""
	if c.SourcePath != "" {
		base = filepath.Dir(c.SourcePath)
	}
	out := make([]string, 0, len(c.Repos))
	seen := map[string]bool{}
	for _, r := range c.Repos {
		// Trimmed before the emptiness check: a whitespace-only entry is a YAML
		// slip, and filepath.Abs would happily turn it into the working directory.
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		p := r
		if !filepath.IsAbs(p) && base != "" {
			p = filepath.Join(base, p)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("resolving repos entry %q: %w", r, err)
		}
		// A repository listed twice would be indexed twice, the second pass
		// appending a duplicate of every fact it already contributed.
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("repos is set but contains no usable paths")
	}
	return out, nil
}

// IsExtractorEnabled returns true if the named extractor is enabled.
func (c *Config) IsExtractorEnabled(name string) bool {
	return contains(c.Extractors, name)
}

// IsExplainerEnabled returns true if the named explainer is enabled.
func (c *Config) IsExplainerEnabled(name string) bool {
	return contains(c.Explainers, name)
}

// IsRendererEnabled returns true if the named renderer is enabled.
func (c *Config) IsRendererEnabled(name string) bool {
	return contains(c.Renderers, name)
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// LinkingVocab resolves the effective cross-repo linking vocabulary: the built-in
// defaults with any `linking:` overlay applied. It returns an error for an invalid
// threshold rather than clamping — see vocab.Apply.
func (c *Config) LinkingVocab() (*vocab.Set, error) {
	return vocab.Apply(c.Linking)
}
