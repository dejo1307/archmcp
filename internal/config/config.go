package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the mcp-arch.yaml configuration.
type Config struct {
	Repo       string       `yaml:"repo"`
	Ignore     []string     `yaml:"ignore"`
	TestGlobs  []string     `yaml:"test_globs"`
	Extractors []string     `yaml:"extractors"`
	Explainers []string     `yaml:"explainers"`
	Renderers  []string     `yaml:"renderers"`
	Output     OutputConfig `yaml:"output"`

	// Dashboard configures the localhost dashboard served alongside the MCP
	// server. Optional: the zero value keeps the built-in defaults.
	Dashboard DashboardConfig `yaml:"dashboard"`

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
			".enola/**",
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
			Dir:              ".enola",
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

	// Ensure required defaults
	if cfg.Output.Dir == "" {
		cfg.Output.Dir = ".enola"
	}
	if cfg.Output.MaxContextTokens == 0 {
		cfg.Output.MaxContextTokens = 16000
	}

	return cfg, nil
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
