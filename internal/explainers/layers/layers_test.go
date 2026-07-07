package layers

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// --- helpers ---

func makeModules(names ...string) []facts.Fact {
	var ff []facts.Fact
	for _, n := range names {
		ff = append(ff, facts.Fact{Kind: facts.KindModule, Name: n})
	}
	return ff
}

// makeModulesLang builds module facts that carry a language prop, matching what
// every extractor emits in production.
func makeModulesLang(lang string, names ...string) []facts.Fact {
	var ff []facts.Fact
	for _, n := range names {
		ff = append(ff, facts.Fact{
			Kind:  facts.KindModule,
			Name:  n,
			Props: map[string]any{"language": lang},
		})
	}
	return ff
}

// frameworkFact returns a non-module fact carrying a framework prop, mimicking
// the route/symbol facts extractors emit (e.g. framework=nextjs).
func frameworkFact(fw string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: "marker", Props: map[string]any{"framework": fw}}
}

func makeStore(modules []string, deps map[string][]string) *facts.Store {
	s := facts.NewStore()
	for _, m := range modules {
		s.Add(facts.Fact{Kind: facts.KindModule, Name: m})
	}
	for src, targets := range deps {
		for _, tgt := range targets {
			s.Add(facts.Fact{
				Kind: facts.KindDependency,
				File: src + "/file.go",
				Relations: []facts.Relation{
					{Kind: facts.RelImports, Target: tgt},
				},
			})
		}
	}
	return s
}

func findPattern(patterns []*archPattern, name string) *archPattern {
	for _, p := range patterns {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func fwSet(names ...string) map[string]bool {
	m := make(map[string]bool)
	for _, n := range names {
		m[n] = true
	}
	return m
}

// --- Unit tests ---

func TestMatchesLayer(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		patterns []string
		want     bool
	}{
		{"middle segment matches", "src/domain/user", []string{"domain"}, true},
		{"exact segment not substring", "src/domain_helper", []string{"domain"}, false},
		{"case insensitive", "Domain/UseCases", []string{"domain"}, true},
		{"no match", "src/foo/bar", []string{"domain"}, false},
		{"first segment matches", "cmd/server", []string{"cmd"}, true},
		{"last segment matches", "src/api", []string{"api"}, true},
		{"multiple patterns", "src/models", []string{"model", "models"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesLayer(tt.path, tt.patterns)
			if got != tt.want {
				t.Errorf("matchesLayer(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestDominantLanguage(t *testing.T) {
	tests := []struct {
		name    string
		modules []facts.Fact
		want    string
	}{
		{"empty", nil, ""},
		{"single", makeModulesLang("go", "cmd", "pkg"), "go"},
		{
			"majority wins",
			append(makeModulesLang("python", "a", "b", "c"), makeModulesLang("typescript", "d")...),
			"python",
		},
		{
			"tie breaks alphabetically",
			append(makeModulesLang("ruby", "a"), makeModulesLang("go", "b")...),
			"go",
		},
		{"no language prop", makeModules("a", "b"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dominantLanguage(tt.modules); got != tt.want {
				t.Errorf("dominantLanguage = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPresentFrameworks(t *testing.T) {
	s := facts.NewStore()
	s.Add(frameworkFact("nextjs"))
	s.Add(frameworkFact("react"))
	s.Add(facts.Fact{Kind: facts.KindModule, Name: "x"}) // no framework prop

	fw := presentFrameworks(s)
	if !fw["nextjs"] || !fw["react"] {
		t.Errorf("presentFrameworks = %v, want nextjs and react", fw)
	}
	if len(fw) != 2 {
		t.Errorf("presentFrameworks size = %d, want 2", len(fw))
	}
}

// --- detection: positive cases ---

func TestDetectPatterns_Hexagonal(t *testing.T) {
	modules := makeModules("domain/entity", "application/usecase", "adapter/rest", "presentation/views")
	e := New()
	patterns := e.detectPatterns(modules, "", nil)

	hexPattern := findPattern(patterns, "hexagonal")
	if hexPattern == nil {
		t.Fatal("expected hexagonal pattern to be detected")
	}

	// 4/4 modules classified, 4/7 layers matched
	// confidence = (classified/total)*0.6 + (matched/total)*0.4 ≈ 0.828
	classified, totalModules := 4.0, 4.0
	matched, totalLayers := 4.0, 7.0
	expectedConf := (classified/totalModules)*0.6 + (matched/totalLayers)*0.4
	if math.Abs(hexPattern.Confidence-expectedConf) > 0.01 {
		t.Errorf("confidence = %f, want ≈ %f", hexPattern.Confidence, expectedConf)
	}

	if len(hexPattern.Layers) != 4 {
		t.Errorf("matched layers = %d, want 4", len(hexPattern.Layers))
	}
}

func TestDetectPatterns_NextJS(t *testing.T) {
	modules := makeModulesLang("typescript", "app", "components", "hooks", "lib")
	e := New()
	patterns := e.detectPatterns(modules, "typescript", fwSet("nextjs"))

	if findPattern(patterns, "nextjs") == nil {
		t.Error("expected nextjs pattern to be detected when framework=nextjs is present")
	}
}

func TestDetectPatterns_GoStandard(t *testing.T) {
	modules := makeModulesLang("go", "cmd/server", "internal/auth", "pkg/utils")
	e := New()
	patterns := e.detectPatterns(modules, "go", nil)

	if findPattern(patterns, "go-standard") == nil {
		t.Error("expected go-standard pattern to be detected")
	}
}

func TestDetectPatterns_RailsMVC(t *testing.T) {
	modules := makeModulesLang("ruby", "app/models", "app/controllers", "app/views", "app/helpers")
	e := New()
	patterns := e.detectPatterns(modules, "ruby", fwSet("rails"))

	if findPattern(patterns, "rails-mvc") == nil {
		t.Error("expected rails-mvc pattern to be detected")
	}
}

func TestDetectPatterns_AndroidClean(t *testing.T) {
	modules := makeModulesLang("kotlin", "domain", "data", "ui", "designsystem")
	e := New()
	patterns := e.detectPatterns(modules, "kotlin", fwSet("android"))

	if findPattern(patterns, "android-clean") == nil {
		t.Error("expected android-clean pattern to be detected")
	}
}

func TestDetectPatterns_IOSClean(t *testing.T) {
	modules := makeModulesLang("swift", "Domain/UseCases", "Data/Network", "Screens", "DesignSystem")
	e := New()
	patterns := e.detectPatterns(modules, "swift", fwSet("swiftui"))

	best := e.bestPattern(patterns)
	if best == nil || best.Name != "ios-clean" {
		t.Errorf("expected ios-clean to win, got %v", best)
	}
}

func TestDetectPatterns_SpringLayered(t *testing.T) {
	modules := makeModulesLang("java", "controller", "service", "repository", "entity", "config")
	e := New()
	patterns := e.detectPatterns(modules, "java", fwSet("spring"))

	best := e.bestPattern(patterns)
	if best == nil || best.Name != "spring-layered" {
		t.Errorf("expected spring-layered to win, got %v", best)
	}
}

func TestDetectPatterns_Django(t *testing.T) {
	modules := makeModulesLang("python", "models", "views", "serializers", "urls", "admin")
	e := New()
	patterns := e.detectPatterns(modules, "python", fwSet("django"))

	if findPattern(patterns, "django") == nil {
		t.Error("expected django pattern to be detected")
	}
}

// --- detection: regression cases (the reported bugs) ---

func TestDetectPatterns_PythonNotNextJS(t *testing.T) {
	// Airflow-like Python repo: generic dirs but no nextjs framework signal.
	modules := makeModulesLang("python", "api", "app", "utils", "lib", "services")
	e := New()
	patterns := e.detectPatterns(modules, "python", nil)

	if p := findPattern(patterns, "nextjs"); p != nil {
		t.Errorf("python repo should not be detected as nextjs (got confidence %f)", p.Confidence)
	}
}

func TestDetectPatterns_RailsNotNextJS(t *testing.T) {
	// Discourse-like repo: JS-heavy Rails app, but framework is rails not nextjs.
	modules := makeModulesLang("ruby", "app", "lib", "api", "services", "components")
	e := New()
	patterns := e.detectPatterns(modules, "ruby", fwSet("rails"))

	if p := findPattern(patterns, "nextjs"); p != nil {
		t.Errorf("rails repo should not be detected as nextjs (got confidence %f)", p.Confidence)
	}
}

func TestDetectPatterns_GenericOOPNotHexagonal(t *testing.T) {
	// Swift/Kotlin-style repo with only generic dirs (no ports/adapters/usecases).
	modules := makeModulesLang("swift", "model", "view", "ui", "networking")
	e := New()
	patterns := e.detectPatterns(modules, "swift", fwSet("swiftui"))

	if p := findPattern(patterns, "hexagonal"); p != nil {
		t.Errorf("generic OOP repo should not be detected as hexagonal (got confidence %f)", p.Confidence)
	}
}

func TestDetectPatterns_SingleSignatureNotHexagonal(t *testing.T) {
	// Airflow-like: one stray "infrastructure" directory (a single adapter
	// signature layer) is not enough to call a repo hexagonal.
	modules := makeModulesLang("python", "api", "core", "utils", "infrastructure")
	e := New()
	patterns := e.detectPatterns(modules, "python", nil)

	if findPattern(patterns, "hexagonal") != nil {
		t.Error("a single signature layer should not trigger hexagonal")
	}
}

func TestDetectPatterns_GoNotNextJS(t *testing.T) {
	// A Go repo with an "api" dir must not match nextjs (no nextjs framework, wrong language).
	modules := makeModulesLang("go", "cmd", "internal", "pkg", "api")
	e := New()
	patterns := e.detectPatterns(modules, "go", nil)

	if findPattern(patterns, "nextjs") != nil {
		t.Error("go repo should not be detected as nextjs")
	}
	if findPattern(patterns, "go-standard") == nil {
		t.Error("go repo should still be detected as go-standard")
	}
}

func TestDetectPatterns_BelowThreshold(t *testing.T) {
	// Only 1 module matches 1 layer out of many unrelated modules
	modules := makeModules("domain", "foo", "bar", "baz", "qux", "quux", "corge", "grault", "garply", "waldo")
	e := New()
	patterns := e.detectPatterns(modules, "", nil)

	// "domain" is not a hexagonal signature layer, and coverage is below the
	// 0.2 threshold anyway, so hexagonal must not be reported.
	if findPattern(patterns, "hexagonal") != nil {
		t.Error("hexagonal should not be detected from a single generic module")
	}
}

// --- detection: gating helpers ---

func TestGateOK(t *testing.T) {
	tests := []struct {
		name string
		def  patternDef
		lang string
		fw   map[string]bool
		want bool
	}{
		{"no gate", patternDef{}, "anything", nil, true},
		{"language match", patternDef{languages: []string{"go"}}, "go", nil, true},
		{"language mismatch", patternDef{languages: []string{"go"}}, "python", nil, false},
		{"framework present", patternDef{frameworks: []string{"nextjs"}}, "", fwSet("nextjs"), true},
		{"framework absent", patternDef{frameworks: []string{"nextjs"}}, "", fwSet("rails"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.def.gateOK(tt.lang, tt.fw); got != tt.want {
				t.Errorf("gateOK = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- bestPattern ---

func TestBestPattern(t *testing.T) {
	e := New()

	patterns := []*archPattern{
		{Name: "a", Confidence: 0.5},
		{Name: "b", Confidence: 0.8},
		{Name: "c", Confidence: 0.3},
	}

	best := e.bestPattern(patterns)
	if best.Name != "b" {
		t.Errorf("bestPattern = %s, want b (highest confidence)", best.Name)
	}
}

func TestBestPattern_PrefersSpecificity(t *testing.T) {
	e := New()

	// A generic pattern with high confidence should lose to a more specific
	// (framework-gated) pattern even at lower confidence.
	patterns := []*archPattern{
		{Name: "hexagonal", Confidence: 0.9, Specificity: 0},
		{Name: "rails-mvc", Confidence: 0.5, Specificity: 2},
	}

	best := e.bestPattern(patterns)
	if best.Name != "rails-mvc" {
		t.Errorf("bestPattern = %s, want rails-mvc (more specific)", best.Name)
	}
}

func TestBestPattern_Empty(t *testing.T) {
	e := New()
	if got := e.bestPattern(nil); got != nil {
		t.Errorf("bestPattern(nil) = %v, want nil", got)
	}
}

// --- violations & Explain integration ---

func TestDetectViolations_InnerImportsOuter(t *testing.T) {
	// Include an adapter module so the hexagonal signature requirement is met.
	store := makeStore(
		[]string{"domain/entity", "presentation/views", "adapter/rest", "application/svc"},
		map[string][]string{
			"domain/entity": {"presentation/views"},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// Should find at least one layer violation
	hasViolation := false
	for _, insight := range insights {
		if insight.Title != "" && insight.Confidence == 0.8 {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Error("expected a layer violation insight when domain imports presentation")
	}
}

func TestDetectViolations_OuterImportsInner(t *testing.T) {
	store := makeStore(
		[]string{"domain/entity", "presentation/views", "adapter/rest", "application/svc"},
		map[string][]string{
			"presentation/views": {"domain/entity"},
		},
	)

	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	// No violation — outer importing inner is correct
	for _, insight := range insights {
		if insight.Confidence == 0.8 {
			t.Errorf("unexpected layer violation: %s", insight.Title)
		}
	}
}

// addDep adds a single import dependency fact from a specific file.
func addDep(s *facts.Store, file, target string) {
	s.Add(facts.Fact{Kind: facts.KindDependency, File: file,
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: target}}})
}

func countViolations(insights []facts.Insight) int {
	n := 0
	for _, in := range insights {
		if in.Confidence == 0.8 {
			n++
		}
	}
	return n
}

// TestDetectViolations_Dedup: two files in the same source module importing the
// same outer module produce ONE violation, not two — so the renderer's
// title-prefix count isn't inflated.
func TestDetectViolations_Dedup(t *testing.T) {
	s := facts.NewStore()
	for _, m := range []string{"domain/entity", "presentation/views", "adapter/rest", "application/svc"} {
		s.Add(facts.Fact{Kind: facts.KindModule, Name: m})
	}
	addDep(s, "domain/entity/a.go", "presentation/views")
	addDep(s, "domain/entity/b.go", "presentation/views") // same (source module, target) pair

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if got := countViolations(insights); got != 1 {
		t.Errorf("duplicate import across two files should yield 1 violation, got %d", got)
	}
}

// TestDetectViolations_LevelEqualNoViolation: an import between two same-level
// layers (application -> port, both level 1) is not a violation.
func TestDetectViolations_LevelEqualNoViolation(t *testing.T) {
	s := facts.NewStore()
	for _, m := range []string{"application/svc", "port/iface", "adapter/rest", "domain/entity"} {
		s.Add(facts.Fact{Kind: facts.KindModule, Name: m})
	}
	addDep(s, "application/svc/a.go", "port/iface")

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if got := countViolations(insights); got != 0 {
		t.Errorf("same-level import should not be a violation, got %d", got)
	}
}

// TestDetectViolations_RelativeImportResolved guards the fix where a JS/TS-style
// relative import target ("../pages") is resolved against the source module
// before matching a layer — previously the raw target never matched, so the
// violation was missed.
func TestDetectViolations_RelativeImportResolved(t *testing.T) {
	s := facts.NewStore()
	s.Add(makeModulesLang("typescript", "pages", "components", "lib", "hooks")...)
	s.Add(frameworkFact("nextjs"))
	// lib (level 0) imports "../pages" (level 3) via a relative path -> violation.
	addDep(s, "lib/util.ts", "../pages")

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	found := false
	for _, in := range insights {
		if in.Confidence == 0.8 && strings.Contains(in.Title, "lib -> pages") {
			found = true
		}
	}
	if !found {
		t.Errorf("relative import lib -> ../pages should resolve to a lib->pages violation; got %v", func() []string {
			out := make([]string, len(insights))
			for i, in := range insights {
				out[i] = in.Title
			}
			return out
		}())
	}
}

// TestExplain_PatternEvidenceDeterministic: the architecture-pattern insight's
// evidence used to be built by ranging a map, so its order churned run to run.
func TestExplain_PatternEvidenceDeterministic(t *testing.T) {
	store := makeStore(
		[]string{"domain/entity", "presentation/views", "adapter/rest", "application/svc"},
		map[string][]string{},
	)
	render := func() string {
		insights, err := New().Explain(context.Background(), store)
		if err != nil {
			t.Fatalf("Explain: %v", err)
		}
		var b strings.Builder
		for _, ev := range insights[0].Evidence { // insights[0] is the architecture pattern
			b.WriteString(ev.Fact)
			b.WriteByte(',')
		}
		return b.String()
	}
	want := render()
	for i := 0; i < 50; i++ {
		if got := render(); got != want {
			t.Fatalf("non-deterministic pattern evidence on iteration %d:\nwant %q\ngot  %q", i, want, got)
		}
	}
}

func TestExplain_NoModules(t *testing.T) {
	store := facts.NewStore()
	e := New()
	insights, err := e.Explain(context.Background(), store)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for empty store, got %d", len(insights))
	}
}

func TestExplain_RailsDetectedNotNextJS(t *testing.T) {
	// End-to-end through Explain: a Rails repo (framework=rails) yields a
	// rails-mvc architecture insight, never nextjs.
	s := facts.NewStore()
	s.Add(makeModulesLang("ruby", "app/models", "app/controllers", "app/views")...)
	s.Add(frameworkFact("rails"))

	e := New()
	insights, err := e.Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	var arch string
	for _, in := range insights {
		const prefix = "Architecture pattern: "
		if len(in.Title) > len(prefix) && in.Title[:len(prefix)] == prefix {
			arch = in.Title[len(prefix):]
		}
	}
	if arch != "rails-mvc" {
		t.Errorf("architecture insight = %q, want rails-mvc", arch)
	}
}
