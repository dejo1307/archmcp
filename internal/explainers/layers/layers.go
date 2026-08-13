package layers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/explainers/common"
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

	// Ember (Octane) app layout. The ordering expresses the real smells: a
	// service or model importing a component, or anything importing a route,
	// runs against the resolver's direction of flow. Peers are collapsed to one
	// tier per the Rails/Go precedent — routes, controllers and route templates
	// are all delivery; components, helpers and modifiers are all rendering
	// (a modifier importing a service is normal, a service importing a
	// component is not); services and the ember-data quartet are all domain.
	emberLayers = []layerDef{
		{Name: "route", Patterns: []string{"routes"}, Level: 3},
		{Name: "controller", Patterns: []string{"controllers"}, Level: 3},
		{Name: "template", Patterns: []string{"templates"}, Level: 3},
		{Name: "component", Patterns: []string{"components"}, Level: 2},
		{Name: "helper", Patterns: []string{"helpers"}, Level: 2},
		{Name: "modifier", Patterns: []string{"modifiers"}, Level: 2},
		{Name: "service", Patterns: []string{"services"}, Level: 1},
		{Name: "model", Patterns: []string{"models"}, Level: 1},
		{Name: "adapter", Patterns: []string{"adapters"}, Level: 1},
		{Name: "serializer", Patterns: []string{"serializers"}, Level: 1},
		{Name: "transform", Patterns: []string{"transforms"}, Level: 1},
		// `lib` is deliberately NOT a util pattern. In Ember Octane utilities live in
		// `app/utils/`; `lib/` holds in-repo ADDONS, which are whole packages with their
		// own app trees and no business at the bottom of a dependency order. Claiming
		// the bare segment was also the single largest source of false layer violations
		// in the corpus: discourse is a Rails backend beside an Ember frontend, the
		// Ember pattern wins the repo, and Ruby's `lib/` — where a large Rails app keeps
		// most of its domain code — became level 0. 397 of discourse's 426 reported
		// violations were `util -> …` edges out of a Ruby lib directory calling the
		// models and services it is supposed to call.
		{Name: "util", Patterns: []string{"utils", "constants"}, Level: 0},
	}

	// Go standard project layout.
	//
	// `internal` and `pkg` are not layers. They are a VISIBILITY distinction the
	// compiler enforces — code under internal/ may only be imported from within the
	// module — and either may hold any layer. pkg/ wrapping internal/ is the standard
	// way to publish an API over a private implementation, and internal/ importing a
	// contract published in pkg/ is how a plugin interface gets implemented. Ranking
	// them made the explainer report both as violations on essentially every Go
	// repository that has both directories, including enola itself.
	//
	// So they are collapsed to one tier, exactly as the Rails layout below collapses
	// its domain directories and for the same reason: a distinction that is not a
	// dependency ordering must not be modelled as one, or the finding is noise and
	// readers learn to skip it.
	//
	// `api` joins them: in the standard layout it holds contract definitions (OpenAPI
	// specs, .proto files and the code generated from them), which implementations are
	// supposed to depend on.
	//
	// That leaves one genuine ordering, and the explainer still catches it: `cmd` holds
	// entrypoints, so anything importing INTO cmd is a library reaching into a binary —
	// a real smell, and the only one this layout expresses.
	goStdLayers = []layerDef{
		{Name: "cmd", Patterns: []string{"cmd"}, Level: 2},
		{Name: "internal", Patterns: []string{"internal"}, Level: 1},
		{Name: "pkg", Patterns: []string{"pkg"}, Level: 1},
		{Name: "api", Patterns: []string{"api"}, Level: 1},
	}

	// Ruby on Rails layout.
	//
	// Rails is not a hexagonal/onion architecture: models routinely call service
	// objects, jobs, mailers and helpers, and that is idiomatic — not a layer
	// violation. Modelling it with fine-grained inner/outer levels made the layers
	// explainer flag most Rails codebases (model -> service, model -> job,
	// service -> helper, ...). So we collapse it to two tiers:
	//
	//   delivery (Level 2): controllers, views — the HTTP/request surface
	//   domain   (Level 1): everything the app is built from (models, services,
	//                       jobs, mailers, helpers, interactors, presenters, ...)
	//
	// With every domain directory at the same level, intra-domain references are
	// same-level and never violations; only a domain module reaching UP into the
	// delivery layer (e.g. a model or service referencing a controller/view
	// constant) is reported — which is a genuine Rails smell.
	railsLayers = []layerDef{
		{Name: "controller", Patterns: []string{"controller", "controllers"}, Level: 2},
		{Name: "view", Patterns: []string{"view", "views"}, Level: 2},
		{Name: "model", Patterns: []string{"model", "models"}, Level: 1},
		{Name: "service", Patterns: []string{"service", "services"}, Level: 1},
		{Name: "job", Patterns: []string{"job", "jobs", "worker", "workers"}, Level: 1},
		{Name: "mailer", Patterns: []string{"mailer", "mailers"}, Level: 1},
		{Name: "helper", Patterns: []string{"helper", "helpers"}, Level: 1},
		{Name: "interactor", Patterns: []string{"interactor", "interactors"}, Level: 1},
		{Name: "presenter", Patterns: []string{"presenter", "presenters"}, Level: 1},
		{Name: "serializer", Patterns: []string{"serializer", "serializers"}, Level: 1},
		{Name: "notifier", Patterns: []string{"notifier", "notifiers"}, Level: 1},
		{Name: "policy", Patterns: []string{"policy", "policies"}, Level: 1},
		{Name: "decorator", Patterns: []string{"decorator", "decorators"}, Level: 1},
		{Name: "form", Patterns: []string{"form", "forms"}, Level: 1},
		{Name: "query", Patterns: []string{"query", "queries"}, Level: 1},
		{Name: "validator", Patterns: []string{"validator", "validators"}, Level: 1},
		{Name: "type", Patterns: []string{"type", "types"}, Level: 1},
		{Name: "lib", Patterns: []string{"lib"}, Level: 1},
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

	// .NET clean architecture. The layer is the last dot-separated component of a
	// project name (Ordering.Domain, Catalog.API), which is why this pattern opts
	// into dotted matching below.
	// Only the four layers whose DEPENDENCY DIRECTION Clean Architecture actually
	// fixes. `.Abstractions`, `.Shared`, `.Common` and `.Core` are deliberately
	// absent: they name a shared kernel that sits at no particular level, and
	// giving them one produced ~230 "violations" on OrchardCore — a modular CMS
	// that is not clean architecture and never claimed to be. A pattern that fires
	// on the wrong repo is worse than one that fires on fewer right ones.
	dotnetCleanLayers = []layerDef{
		{Name: "domain", Patterns: []string{"domain", "entities"}, Level: 0},
		{Name: "application", Patterns: []string{"application", "usecases"}, Level: 1},
		{Name: "infrastructure", Patterns: []string{"infrastructure", "persistence", "repositories"}, Level: 2},
		{Name: "api", Patterns: []string{"api", "webapp", "grpc"}, Level: 3},
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
	// dottedSegments makes a path segment match on its dot-separated components as
	// well as whole. .NET names a project `<Product>.<Layer>` — `Ordering.Domain`,
	// `Catalog.API` — so the layer never equals the segment. Opt-in rather than
	// global: a dotted directory in another ecosystem (`cal.com`, `foo.web`) would
	// otherwise start matching layers it has nothing to do with.
	dottedSegments bool

	// signatureLayers, if non-empty, requires at least minSignatureLayers of the
	// matched layers to be distinctive ones from this set (so a pattern built
	// only from generic names — or from a single stray directory — does not
	// qualify).
	signatureLayers []string
	// autoloadRoot, if set, names a directory whose immediate children are
	// application layers by the framework's own contract even when no pattern
	// lists them. Rails/Zeitwerk autoloads every app/* directory as a root, so
	// app/tools or app/agents is as much a layer as app/models — and leaving it
	// unclassified makes every rule that governs it invisible to the explainer.
	autoloadRoot       string
	autoloadLevel      int
	minSignatureLayers int
}

// patternDefs lists all known architecture patterns. Order does not affect the
// outcome; bestPattern selects by specificity then confidence.
var patternDefs = []patternDef{
	// Framework-gated patterns (most specific).
	{name: "nextjs", layers: nextjsLayers, frameworks: []string{"nextjs"}},
	{name: "rails-mvc", layers: railsLayers, frameworks: []string{"rails"},
		autoloadRoot: "app", autoloadLevel: 1},
	{name: "android-clean", layers: androidLayers, frameworks: []string{"android"}},
	{name: "ios-clean", layers: iosLayers, frameworks: []string{"swiftui", "uikit"}},
	{name: "spring-layered", layers: springLayers, frameworks: []string{"spring"}},
	{name: "django", layers: djangoLayers, frameworks: []string{"django"}},
	{name: "dotnet-clean", layers: dotnetCleanLayers, frameworks: []string{"aspnetcore", "efcore"},
		dottedSegments: true, signatureLayers: []string{"domain", "infrastructure", "application"},
		minSignatureLayers: 2},
	{name: "ember-octane", layers: emberLayers, frameworks: []string{"ember"}},

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

// Explain analyzes the fact store and detects architectural patterns, one
// repository at a time.
//
// A layer taxonomy is a property of one codebase, and every input this explainer
// gates on — the dominant language, the frameworks present, which directories
// exist — is a per-repository measurement. Read across a union the signals mix:
// the monolith's `rails` framework fact admitted the Rails taxonomy for every
// other repository in the snapshot, its module names collided with theirs, and
// the violations reported against one repository were built from another's
// imports. Single-repo snapshots are unaffected — every fact carries the one
// label, so the loop below runs once over exactly what it used to see.
func (e *LayerExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	scopes := store.RepoLabels()
	sort.Strings(scopes)
	// Facts carrying no label are one more scope rather than a scope each: they
	// cannot be attributed to a repository, so the only honest grouping is
	// "everything unattributed", analysed together and never mixed with a
	// repository that named itself.
	scopes = append(scopes, "")

	var insights []facts.Insight
	for _, scope := range scopes {
		insights = append(insights, e.explainRepo(store, scope)...)
	}
	return insights, nil
}

// scopeFacts returns the facts carrying the given repo label. The store indexes
// only non-empty labels, so the unlabelled scope is collected by scan.
func scopeFacts(store *facts.Store, repo string) []facts.Fact {
	if repo != "" {
		return store.ByRepo(repo)
	}
	var out []facts.Fact
	// FactsRef, not All: this only reads, and the facts do not outlive the pass.
	for _, f := range store.FactsRef() {
		if f.Repo == "" {
			out = append(out, f)
		}
	}
	return out
}

// explainRepo runs the whole detection over one repository's facts.
func (e *LayerExplainer) explainRepo(store *facts.Store, repo string) []facts.Insight {
	scoped := scopeFacts(store, repo)

	// Test scaffolding is not architecture, and it is load-bearing here rather than
	// cosmetic: len(modules) is the denominator of the pattern's coverage confidence,
	// that confidence breaks ties in bestPattern, and a signature-layer gate can be
	// satisfied purely by test trees (src/test/…/adapter plus src/test/…/application
	// is enough to claim hexagonal). common.IsTestModule rather than a path test,
	// because `module_role` from a build file outranks what a path looks like.
	modules := make([]facts.Fact, 0, len(scoped))
	for _, m := range scoped {
		if m.Kind != facts.KindModule || common.IsTestModule(m) {
			continue
		}
		modules = append(modules, m)
	}
	if len(modules) == 0 {
		return nil
	}

	// Derive language/framework signals used to gate pattern detection.
	lang := dominantLanguage(modules)
	frameworks := presentFrameworks(scoped)

	// Detect which architecture patterns match
	patterns := e.detectPatterns(modules, lang, frameworks)

	var insights []facts.Insight

	// Declared layer patterns first: a repo whose intent facts declare layers
	// is verdicted against its declaration at confidence 1.0 by the same
	// violation machinery, and the heuristics below skip nothing — they still
	// serve every repo without a declaration.
	for _, dp := range declaredPatterns(store, repo) {
		mods := make([]string, 0, len(dp.Modules))
		for mod := range dp.Modules {
			mods = append(mods, mod)
		}
		sort.Strings(mods)
		evidence := make([]facts.Evidence, 0, len(mods))
		for _, mod := range mods {
			evidence = append(evidence, facts.Evidence{Fact: mod, Detail: fmt.Sprintf("module %q maps to declared layer %q", mod, dp.Modules[mod])})
		}
		insights = append(insights, facts.Insight{
			Title:       fmt.Sprintf("Architecture pattern: %s", dp.Name),
			Description: fmt.Sprintf("Declared layer order with %d layers and %d classified modules. Declared, not recognised: confidence is exact.", len(dp.Layers), len(dp.Modules)),
			Confidence:  1.0,
			Evidence:    evidence,
			Actions:     []string{"Keep the declaration beside the code it governs"},
		})
		violations := e.detectViolations(scoped, dp)
		for i := range violations {
			violations[i].Confidence = 1.0
		}
		insights = append(insights, violations...)
	}

	// Report detected architecture pattern
	if best := e.bestPattern(patterns); best != nil {
		// Sort the classified modules so the evidence order is deterministic —
		// ranging best.Modules directly would follow Go's randomized map order.
		mods := make([]string, 0, len(best.Modules))
		for mod := range best.Modules {
			mods = append(mods, mod)
		}
		sort.Strings(mods)
		evidence := make([]facts.Evidence, 0, len(mods))
		for _, mod := range mods {
			layer := best.Modules[mod]
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
		violations := e.detectViolations(scoped, best)
		insights = append(insights, violations...)
	}

	return insights
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
				if matchesLayerIn(mod.Name, layer.Patterns, def.dottedSegments) {
					pattern.Layers[layer.Name] = &def.layers[i]
					pattern.Modules[mod.Name] = layer.Name
					matchCount++
					break
				}
			}
		}

		// Anything the taxonomy did not name but the framework autoloads is
		// still a layer. Without this a Rails app's own vocabulary — app/tools,
		// app/agents, app/schemas — is unclassified, and those are usually
		// exactly the directories a house rule is written about.
		autoLayers := 0
		if def.autoloadRoot != "" {
			for _, mod := range modules {
				if _, classified := pattern.Modules[mod.Name]; classified {
					continue
				}
				name, ok := autoloadedLayer(mod.Name, def.autoloadRoot)
				if !ok {
					continue
				}
				if _, exists := pattern.Layers[name]; !exists {
					pattern.Layers[name] = &layerDef{
						Name: name, Patterns: []string{name}, Level: def.autoloadLevel,
					}
					autoLayers++
				}
				pattern.Modules[mod.Name] = name
				matchCount++
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
		layerCoverage := float64(len(pattern.Layers)) / float64(len(def.layers)+autoLayers)

		// Ceiling is deliberately below 1.0: confidence 1.0 is reserved for a
		// structural fact, and a pattern match is a coverage ratio over directory
		// names — a well-supported guess, never a certainty. A repo where every
		// module matched every layer would otherwise present as proof.
		pattern.Confidence = (coverage*0.6 + layerCoverage*0.4)
		if pattern.Confidence > common.MaxHeuristicConfidence {
			pattern.Confidence = common.MaxHeuristicConfidence
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

// presentFrameworks collects the set of frameworks present in the given facts,
// using the Props["framework"] attribute extractors set on routes, symbols and
// modules (e.g. nextjs, rails, django, android, spring). The caller passes one
// repository's facts: a framework is a property of the repository that uses it,
// and read across a union one repository's `rails` opened the Rails taxonomy to
// every other repository in the snapshot.
func presentFrameworks(ff []facts.Fact) map[string]bool {
	out := make(map[string]bool)
	for _, f := range ff {
		if fw, ok := f.Props["framework"].(string); ok && fw != "" {
			out[fw] = true
		}
	}
	return out
}

// detectViolations checks for layer boundary violations (inner layer importing
// outer layer). Each distinct (source module -> target module) pair is reported
// once: two files in the same module importing the same outer module are one
// violation, not two, so the count the renderer derives isn't inflated. Relative
// import targets (`./x`, `../y`) are resolved against the source module the same
// way the shared module-graph builder does, so JS/TS-style relative imports match
// a classified layer instead of silently missing. Output is sorted for
// determinism.
//
// The facts passed in are one repository's, so an import can only ever be
// verdicted against the taxonomy of the repository it was measured in.
func (e *LayerExplainer) detectViolations(scoped []facts.Fact, pattern *archPattern) []facts.Insight {
	projectOf := moduleProjects(scoped)
	type violation struct {
		sourceModule, targetModule string
		sourceLayer, targetLayer   string
		sourceLevel, targetLevel   int
		file                       string
		// depName and rawTarget are the two strings the snapshot diff can actually
		// match. touchedNames holds fact names plus both endpoints of added/removed
		// edges, and an import edge is {Source: dep.Name, Target: rel.Target} — the
		// RAW target, before the relative-import rewrite below. The importing file is
		// never a fact name, so evidence citing only the file can never be attributed
		// to a change, which routes every new violation to incidental and out of the
		// gate. Both are captured from the dependency that survives the dedup.
		depName, rawTarget string
	}
	seen := make(map[string]bool)
	var violations []violation

	for _, dep := range scoped {
		if dep.Kind != facts.KindDependency {
			continue
		}
		// Test code is not architecture. Gate on the file rather than the module:
		// resolveLayerModule walks UP to the nearest classified module, so a test or
		// mock nested inside a production module (Sources/Foo/Mocks/X.swift) would
		// otherwise have its imports attributed to the production layer.
		if facts.IsTestPath(dep.File) {
			continue
		}
		// Resolve the importing file's directory up to its nearest classified module,
		// so a file nested below the module root (Swift/Xcode) still attributes to a
		// layer instead of being dropped. Both the raw and repo-stripped directories
		// are tried: the stripped one is required in an append-mode snapshot (the file
		// is repo-prefixed and module names are not, so the raw dir yields the repo
		// label and nothing resolves), while the raw one is required in a single-repo
		// layout whose top-level package carries the repo's own name, where stripping
		// removes a real path segment. Using either alone silences this explainer for
		// one of the two shapes.
		sourceModule, sourceOK := resolveLayerModuleFor(dep, pattern.Modules)
		if !sourceOK {
			continue
		}
		sourceLayer := pattern.Modules[sourceModule]

		for _, rel := range dep.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}

			rawTarget := rel.Target
			target := rawTarget
			if strings.HasPrefix(target, ".") {
				target = common.ResolveRelativeImport(sourceModule, target)
			}

			// Resolve the target up to its enclosing classified module. Import targets
			// carry differing granularity across extractors — a module dir + symbol
			// name (Kotlin/Java `import a.b.C` -> "a/b/C") or a file stem (TypeScript) —
			// so an exact module lookup silently drops every class-/file-suffixed
			// target (which was hiding all Kotlin-sourced layer violations). Mirrors
			// package_metrics' resolveToModule; a no-op for bare-module-dir targets.
			targetModule, targetOK := resolveLayerModule(target, pattern.Modules)
			if !targetOK {
				continue
			}
			targetLayer := pattern.Modules[targetModule]
			target = targetModule

			sourceDef := pattern.Layers[sourceLayer]
			targetDef := pattern.Layers[targetLayer]
			if sourceDef == nil || targetDef == nil || sourceDef.Level >= targetDef.Level {
				continue
			}
			// Same assembly, no violation. .NET's layer boundary is the PROJECT, and
			// two directories inside one compile into the same DLL — the reason the
			// cycles explainer already says an intra-assembly cycle is a coupling
			// signal rather than a build problem. Without this, eShop reported
			// Basket.API/Repositories -> Basket.API/Model as infrastructure -> api,
			// because the sub-directory takes its layer from its own name while its
			// sibling inherits one from the project name they share.
			if p := projectOf[sourceModule]; p != "" && p == projectOf[target] {
				continue
			}

			key := sourceModule + "\x00" + target
			if seen[key] {
				continue
			}
			seen[key] = true
			violations = append(violations, violation{
				sourceModule: sourceModule, targetModule: target,
				sourceLayer: sourceLayer, targetLayer: targetLayer,
				sourceLevel: sourceDef.Level, targetLevel: targetDef.Level,
				file:    dep.File,
				depName: dep.Name, rawTarget: rawTarget,
			})
		}
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].sourceModule != violations[j].sourceModule {
			return violations[i].sourceModule < violations[j].sourceModule
		}
		return violations[i].targetModule < violations[j].targetModule
	})

	insights := make([]facts.Insight, 0, len(violations))
	for _, v := range violations {
		// The file entry stays FIRST: the dashboard's firstEvidence and the gate's
		// writeFindings both render the first qualifying entry, so the entities the
		// diff needs are appended behind it rather than prepended in front of it.
		evidence := []facts.Evidence{
			{File: v.file, Detail: fmt.Sprintf("import of %s", v.targetModule)},
		}
		seenEntity := make(map[string]bool, 4)
		addEntity := func(name, detail string) {
			if name == "" || seenEntity[name] {
				return
			}
			seenEntity[name] = true
			evidence = append(evidence, facts.Evidence{Fact: name, Detail: detail})
		}
		addEntity(v.depName, "import edge")
		addEntity(v.rawTarget, "import edge target")
		addEntity(v.sourceModule, "importing module")
		addEntity(v.targetModule, "imported module")

		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("Layer violation: %s -> %s", v.sourceLayer, v.targetLayer),
			Description: fmt.Sprintf(
				"Module %q (layer: %s, level %d) imports module %q (layer: %s, level %d). "+
					"Inner layers should not depend on outer layers.",
				v.sourceModule, v.sourceLayer, v.sourceLevel,
				v.targetModule, v.targetLayer, v.targetLevel,
			),
			Confidence: 0.8,
			Evidence:   evidence,
			Actions: []string{
				"Introduce an interface/port in the inner layer",
				"Move shared types to a common package",
				"Invert the dependency using dependency injection",
			},
		})
	}

	return insights
}

// resolveLayerModule walks path up its directory segments until it names a
// classified module (a key in modules), returning that module and true. If no
// ancestor is classified it returns false. This absorbs the granularity
// differences in extractor import targets (bare module dir, module dir + symbol
// name, or file stem) and in nested source layouts, so a layer violation is not
// missed just because the raw endpoint carried a trailing symbol/file segment.
func resolveLayerModule(path string, modules map[string]string) (string, bool) {
	cur := path
	for {
		if _, ok := modules[cur]; ok {
			return cur, true
		}
		i := strings.LastIndex(cur, "/")
		if i < 0 {
			return "", false
		}
		cur = cur[:i]
	}
}

// resolveLayerModuleFor resolves a dependency fact's own file to a classified module,
// trying every directory the file may name (see common.ModuleDirCandidates).
//
// Exact matches are tried across all candidates before any is walked up, for the same
// reason as common.resolveModuleDir: a walk-up can reach a short ancestor that exists
// only because of the OTHER snapshot shape, and letting it beat an exact match would
// attribute the import to the wrong module.
func resolveLayerModuleFor(dep facts.Fact, modules map[string]string) (string, bool) {
	candidates := common.ModuleDirCandidates(dep)
	for _, c := range candidates {
		if _, ok := modules[c]; ok {
			return c, true
		}
	}
	for _, c := range candidates {
		if m, ok := resolveLayerModule(c, modules); ok {
			return m, true
		}
	}
	return "", false
}

// matchesLayer checks if a module path contains any of the given patterns.
func matchesLayer(modulePath string, patterns []string) bool {
	return matchesLayerIn(modulePath, patterns, false)
}

// matchesLayerIn compares whole path segments, and with dotted=true each
// dot-separated component of a segment as well.
func matchesLayerIn(modulePath string, patterns []string, dotted bool) bool {
	for _, part := range strings.Split(strings.ToLower(modulePath), "/") {
		candidates := []string{part}
		if dotted && strings.Contains(part, ".") {
			candidates = append(candidates, strings.Split(part, ".")...)
		}
		for _, c := range candidates {
			for _, pattern := range patterns {
				if c == pattern {
					return true
				}
			}
		}
	}
	return false
}

// moduleProjects maps each module to the assembly it compiles into, from the
// `project` prop the MSBuild pass sets. Empty for languages that have no such
// unit, which leaves their violation behaviour unchanged.
func moduleProjects(ff []facts.Fact) map[string]string {
	out := map[string]string{}
	for _, m := range ff {
		if m.Kind != facts.KindModule {
			continue
		}
		if p, ok := m.Props["project"].(string); ok && p != "" {
			out[m.Name] = p
		}
	}
	return out
}

// autoloadedLayer names the layer an autoloaded module belongs to: the first
// path segment under the framework's autoload root. app/tools/replan_week is
// part of "tools", the same way app/models/coaching is part of "model".
//
// The root must be the *first* segment, not any segment. A monolith that also
// contains a front-end app has directories like ember_app/app/routes, and
// matching "app" anywhere swept a second framework's whole layout into the
// Rails taxonomy — which inflated its coverage until it displaced the pattern
// that was correctly winning, replacing a real analysis with a wrong one.
// Nested Rails roots (packwerk packages/*/app) are deliberately not claimed:
// classifying too little is recoverable, classifying the wrong framework is not.
func autoloadedLayer(modulePath, root string) (string, bool) {
	parts := strings.Split(strings.ToLower(modulePath), "/")
	if len(parts) < 2 || parts[0] != root || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
