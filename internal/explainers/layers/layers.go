package layers

import (
	"context"
	"fmt"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// LayerExplainer detects architectural patterns and layer violations.
type LayerExplainer struct{}

// New creates a new LayerExplainer.
func New() *LayerExplainer {
	return &LayerExplainer{}
}

func (e *LayerExplainer) Name() string {
	return "layers"
}

// layerDef defines how we detect architectural layers from module path patterns.
type layerDef struct {
	Name     string
	Patterns []string
	Level    int // Lower level = inner/domain, higher = outer/infra
}

// Predefined layer patterns for common architectures.
var (
	// Hexagonal / Clean Architecture layers.
	// Order matters: more specific patterns (application, adapter) are checked before
	// broader ones (domain) to avoid misclassification of paths like Domain/UseCases.
	hexagonalLayers = []layerDef{
		{Name: "application", Patterns: []string{"application", "usecase", "usecases"}, Level: 1},
		{Name: "port", Patterns: []string{"port", "ports", "interface", "interfaces"}, Level: 1},
		{Name: "adapter", Patterns: []string{"adapter", "adapters", "infrastructure", "infra", "gateway", "network"}, Level: 2},
		{Name: "repository", Patterns: []string{"repository", "repositories", "repo", "repos", "store", "storage", "persistence", "db", "database"}, Level: 2},
		{Name: "presentation", Patterns: []string{"presentation", "ui", "view", "views", "screen", "screens", "page", "pages"}, Level: 3},
		{Name: "handler", Patterns: []string{"handler", "handlers", "controller", "controllers", "api", "http", "grpc", "rest"}, Level: 3},
		{Name: "domain", Patterns: []string{"domain", "entity", "entities", "model", "models", "core"}, Level: 0},
	}

	// Next.js layers
	nextjsLayers = []layerDef{
		{Name: "pages", Patterns: []string{"pages", "app"}, Level: 3},
		{Name: "components", Patterns: []string{"components", "ui"}, Level: 2},
		{Name: "hooks", Patterns: []string{"hooks"}, Level: 1},
		{Name: "lib", Patterns: []string{"lib", "utils", "helpers"}, Level: 0},
		{Name: "api", Patterns: []string{"api"}, Level: 3},
		{Name: "services", Patterns: []string{"services"}, Level: 1},
		{Name: "types", Patterns: []string{"types"}, Level: 0},
	}

	// Go standard project layout
	goStdLayers = []layerDef{
		{Name: "cmd", Patterns: []string{"cmd"}, Level: 3},
		{Name: "internal", Patterns: []string{"internal"}, Level: 1},
		{Name: "pkg", Patterns: []string{"pkg"}, Level: 0},
		{Name: "api", Patterns: []string{"api"}, Level: 2},
	}

	// Ruby on Rails MVC layout
	railsLayers = []layerDef{
		{Name: "model", Patterns: []string{"model", "models"}, Level: 0},
		{Name: "controller", Patterns: []string{"controller", "controllers"}, Level: 3},
		{Name: "view", Patterns: []string{"view", "views"}, Level: 3},
		{Name: "helper", Patterns: []string{"helper", "helpers"}, Level: 2},
		{Name: "mailer", Patterns: []string{"mailer", "mailers"}, Level: 2},
		{Name: "job", Patterns: []string{"job", "jobs", "worker", "workers"}, Level: 1},
		{Name: "service", Patterns: []string{"service", "services"}, Level: 1},
	}

	// Android clean architecture / MVVM layout
	androidLayers = []layerDef{
		{Name: "domain", Patterns: []string{"domain"}, Level: 0},
		{Name: "data", Patterns: []string{"data", "repository", "repositories"}, Level: 1},
		{Name: "ui", Patterns: []string{"ui", "presentation", "view", "views", "screen", "screens"}, Level: 3},
		{Name: "di", Patterns: []string{"di", "injection"}, Level: 2},
		{Name: "designsystem", Patterns: []string{"designsystem"}, Level: 3},
	}

	// iOS clean architecture / MVVM layout
	iosLayers = []layerDef{
		{Name: "domain", Patterns: []string{"domain"}, Level: 0},
		{Name: "data", Patterns: []string{"data", "repository", "repositories"}, Level: 1},
		{Name: "ui", Patterns: []string{"ui", "presentation", "view", "views", "screen", "screens", "components"}, Level: 3},
		{Name: "designsystem", Patterns: []string{"designsystem"}, Level: 3},
	}

	// Spring layered architecture layout
	springLayers = []layerDef{
		{Name: "controller", Patterns: []string{"controller", "controllers", "rest", "web"}, Level: 3},
		{Name: "service", Patterns: []string{"service", "services"}, Level: 1},
		{Name: "repository", Patterns: []string{"repository", "repositories", "dao", "daos"}, Level: 1},
		{Name: "entity", Patterns: []string{"entity", "entities", "model", "models", "domain"}, Level: 0},
		{Name: "dto", Patterns: []string{"dto", "dtos"}, Level: 2},
		{Name: "config", Patterns: []string{"config", "configuration"}, Level: 2},
	}

	// Django layout
	djangoLayers = []layerDef{
		{Name: "models", Patterns: []string{"model", "models"}, Level: 0},
		{Name: "views", Patterns: []string{"view", "views"}, Level: 3},
		{Name: "serializers", Patterns: []string{"serializer", "serializers"}, Level: 2},
		{Name: "urls", Patterns: []string{"url", "urls"}, Level: 3},
		{Name: "admin", Patterns: []string{"admin"}, Level: 3},
		{Name: "forms", Patterns: []string{"form", "forms"}, Level: 2},
	}
)

// patternDef defines an architecture pattern together with the signals that gate
// its detection. Without gating, generic directory names (api, app, ui, lib,
// model, ...) cause patterns to match repos of the wrong language/framework.
type patternDef struct {
	name   string
	layers []layerDef

	// languages, if non-empty, requires the repo's dominant language to be one
	// of these for the pattern to be considered.
	languages []string
	// frameworks, if non-empty, requires at least one of these frameworks to be
	// present in the facts for the pattern to be considered.
	frameworks []string
	// signatureLayers, if non-empty, requires at least minSignatureLayers of the
	// matched layers to be distinctive ones from this set (so a pattern built
	// only from generic names — or from a single stray directory — does not
	// qualify).
	signatureLayers    []string
	minSignatureLayers int
}

// patternDefs lists all known architecture patterns. Order does not affect the
// outcome; bestPattern selects by specificity then confidence.
var patternDefs = []patternDef{
	// Framework-gated patterns (most specific).
	{name: "nextjs", layers: nextjsLayers, frameworks: []string{"nextjs"}},
	{name: "rails-mvc", layers: railsLayers, frameworks: []string{"rails"}},
	{name: "android-clean", layers: androidLayers, frameworks: []string{"android"}},
	{name: "ios-clean", layers: iosLayers, frameworks: []string{"swiftui", "uikit"}},
	{name: "spring-layered", layers: springLayers, frameworks: []string{"spring"}},
	{name: "django", layers: djangoLayers, frameworks: []string{"django"}},

	// Language-gated patterns.
	{name: "go-standard", layers: goStdLayers, languages: []string{"go"}},

	// Language-agnostic patterns, gated on distinctive signature layers. Require
	// at least two distinct ports-and-adapters layers so a single stray
	// directory (e.g. one "infrastructure" test folder) does not trigger it.
	{name: "hexagonal", layers: hexagonalLayers, signatureLayers: []string{"application", "port", "adapter"}, minSignatureLayers: 2},
}

// specificity ranks how targeted a pattern's gating is. Higher wins ties: a
// framework-specific pattern is preferred over a language-gated one, which is
// preferred over a generic (signature-only) pattern.
func (d patternDef) specificity() int {
	switch {
	case len(d.frameworks) > 0:
		return 2
	case len(d.languages) > 0:
		return 1
	default:
		return 0
	}
}

// gateOK reports whether the pattern's language/framework requirements are met.
func (d patternDef) gateOK(lang string, frameworks map[string]bool) bool {
	if len(d.languages) > 0 {
		matched := false
		for _, l := range d.languages {
			if l == lang {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(d.frameworks) > 0 {
		matched := false
		for _, f := range d.frameworks {
			if frameworks[f] {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// archPattern represents a detected architecture pattern with its confidence.
type archPattern struct {
	Name        string
	Confidence  float64
	Specificity int // from patternDef.specificity(); used to break ties in bestPattern
	Layers      map[string]*layerDef
	Modules     map[string]string // module -> layer name
}

// Explain analyzes the fact store and detects architectural patterns.
func (e *LayerExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	modules := store.ByKind(facts.KindModule)
	if len(modules) == 0 {
		return nil, nil
	}

	// Derive language/framework signals used to gate pattern detection.
	lang := dominantLanguage(modules)
	frameworks := presentFrameworks(store)

	// Detect which architecture patterns match
	patterns := e.detectPatterns(modules, lang, frameworks)

	var insights []facts.Insight

	// Report detected architecture pattern
	if best := e.bestPattern(patterns); best != nil {
		evidence := make([]facts.Evidence, 0)
		for mod, layer := range best.Modules {
			evidence = append(evidence, facts.Evidence{
				Fact:   mod,
				Detail: fmt.Sprintf("module %q maps to layer %q", mod, layer),
			})
		}

		insights = append(insights, facts.Insight{
			Title:       fmt.Sprintf("Architecture pattern: %s", best.Name),
			Description: fmt.Sprintf("Detected %s architecture pattern with %.0f%% confidence. Found %d layers with %d classified modules.", best.Name, best.Confidence*100, len(best.Layers), len(best.Modules)),
			Confidence:  best.Confidence,
			Evidence:    evidence,
			Actions: []string{
				"Ensure new code follows the detected layer structure",
				"Review cross-layer dependencies for violations",
			},
		})

		// Detect layer violations
		violations := e.detectViolations(store, best)
		insights = append(insights, violations...)
	}

	return insights, nil
}

func (e *LayerExplainer) detectPatterns(modules []facts.Fact, lang string, frameworks map[string]bool) []*archPattern {
	var patterns []*archPattern

	for di := range patternDefs {
		def := patternDefs[di]

		// Skip patterns whose language/framework gate is not satisfied. This is
		// what stops e.g. a Python or Ruby repo from matching the generic
		// "nextjs" directory names, or a plain OOP repo from matching nextjs.
		if !def.gateOK(lang, frameworks) {
			continue
		}

		pattern := &archPattern{
			Name:        def.name,
			Specificity: def.specificity(),
			Layers:      make(map[string]*layerDef),
			Modules:     make(map[string]string),
		}

		matchCount := 0
		for _, mod := range modules {
			for i, layer := range def.layers {
				if matchesLayer(mod.Name, layer.Patterns) {
					pattern.Layers[layer.Name] = &def.layers[i]
					pattern.Modules[mod.Name] = layer.Name
					matchCount++
					break
				}
			}
		}

		if matchCount == 0 || len(modules) == 0 {
			continue
		}

		// Require enough distinctive signature layers when the pattern declares
		// them, so a match built only from generic names (e.g. just model + ui)
		// or from a single stray directory does not qualify.
		if len(def.signatureLayers) > 0 &&
			countSignatureLayers(pattern, def.signatureLayers) < def.minSignatureLayers {
			continue
		}

		// Confidence based on how many modules are classified
		coverage := float64(matchCount) / float64(len(modules))
		// Also factor in how many distinct layers are matched
		layerCoverage := float64(len(pattern.Layers)) / float64(len(def.layers))

		pattern.Confidence = (coverage*0.6 + layerCoverage*0.4)
		if pattern.Confidence > 1.0 {
			pattern.Confidence = 1.0
		}

		// Minimum threshold
		if pattern.Confidence >= 0.2 && len(pattern.Layers) >= 2 {
			patterns = append(patterns, pattern)
		}
	}

	return patterns
}

// countSignatureLayers returns how many of the given distinctive layer names the
// detected pattern matched.
func countSignatureLayers(pattern *archPattern, signature []string) int {
	n := 0
	for _, name := range signature {
		if _, ok := pattern.Layers[name]; ok {
			n++
		}
	}
	return n
}

func (e *LayerExplainer) bestPattern(patterns []*archPattern) *archPattern {
	if len(patterns) == 0 {
		return nil
	}

	best := patterns[0]
	for _, p := range patterns[1:] {
		// Prefer the more specific pattern (framework > language > generic);
		// break ties by confidence.
		if p.Specificity > best.Specificity ||
			(p.Specificity == best.Specificity && p.Confidence > best.Confidence) {
			best = p
		}
	}
	return best
}

// dominantLanguage returns the most common language across module facts, using
// the Props["language"] attribute every extractor sets. Ties break
// alphabetically for deterministic output.
func dominantLanguage(modules []facts.Fact) string {
	counts := make(map[string]int)
	for _, m := range modules {
		if lang, ok := m.Props["language"].(string); ok && lang != "" {
			counts[lang]++
		}
	}
	best := ""
	bestN := 0
	for lang, n := range counts {
		if n > bestN || (n == bestN && lang < best) {
			best, bestN = lang, n
		}
	}
	return best
}

// presentFrameworks collects the set of frameworks present anywhere in the fact
// store, using the Props["framework"] attribute extractors set on routes,
// symbols and modules (e.g. nextjs, rails, django, android, spring).
func presentFrameworks(store *facts.Store) map[string]bool {
	out := make(map[string]bool)
	for _, f := range store.All() {
		if fw, ok := f.Props["framework"].(string); ok && fw != "" {
			out[fw] = true
		}
	}
	return out
}

// detectViolations checks for layer boundary violations (inner layer importing outer layer).
func (e *LayerExplainer) detectViolations(store *facts.Store, pattern *archPattern) []facts.Insight {
	var insights []facts.Insight

	deps := store.ByKind(facts.KindDependency)
	for _, dep := range deps {
		sourceModule := fileDir(dep.File)
		sourceLayer, sourceOK := pattern.Modules[sourceModule]
		if !sourceOK {
			continue
		}

		for _, rel := range dep.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}

			targetLayer, targetOK := pattern.Modules[rel.Target]
			if !targetOK {
				continue
			}

			// Check if source layer level is lower than target
			sourceDef := pattern.Layers[sourceLayer]
			targetDef := pattern.Layers[targetLayer]

			if sourceDef != nil && targetDef != nil && sourceDef.Level < targetDef.Level {
				insights = append(insights, facts.Insight{
					Title: fmt.Sprintf("Layer violation: %s -> %s", sourceLayer, targetLayer),
					Description: fmt.Sprintf(
						"Module %q (layer: %s, level %d) imports module %q (layer: %s, level %d). "+
							"Inner layers should not depend on outer layers.",
						sourceModule, sourceLayer, sourceDef.Level,
						rel.Target, targetLayer, targetDef.Level,
					),
					Confidence: 0.8,
					Evidence: []facts.Evidence{
						{File: dep.File, Detail: fmt.Sprintf("import of %s", rel.Target)},
					},
					Actions: []string{
						"Introduce an interface/port in the inner layer",
						"Move shared types to a common package",
						"Invert the dependency using dependency injection",
					},
				})
			}
		}
	}

	return insights
}

// matchesLayer checks if a module path contains any of the given patterns.
func matchesLayer(modulePath string, patterns []string) bool {
	parts := strings.Split(strings.ToLower(modulePath), "/")
	for _, part := range parts {
		for _, pattern := range patterns {
			if part == pattern {
				return true
			}
		}
	}
	return false
}

func fileDir(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}
