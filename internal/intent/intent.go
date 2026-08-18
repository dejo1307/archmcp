// Package intent parses declared architectural intent — what a repository is
// SUPPOSED to be — as distinct from everything else in this codebase, which
// measures what it is. A declaration names the repo's service identity, the
// seams it intends to consume with their via-kinds, the surfaces it commits
// to serving, and its layer order.
//
// Declarations have two homes: `enola-intent.yaml` at a repo's root (source,
// reviewed beside the code it governs) and the cluster config's `intent:`
// block (for repos an operator observes but does not own). When both exist
// for one repo the cluster entry overrides the repo file — wholesale, per
// repo, never key-by-key — and the override is recorded on the resolved
// declaration, so exactly one source is authoritative in any run and which
// one is always visible.
//
// Parsing validates vocabulary: a `via` must be one the linker defines
// (facts.AllViaKinds); a free-form spelling is a parse error naming the
// allowed set. Nothing in this package consumes the model — verdicting
// belongs to a later stage.
package intent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/enola-labs/enola/internal/facts"
)

// RepoFileName is the per-repo declaration file, at the repo root. It cannot
// live under .enola/, which is gitignored output — a declaration is source.
const RepoFileName = "enola-intent.yaml"

// ClusterSource is the Source value of a declaration taken from the cluster
// config's intent: block rather than a repo file.
const ClusterSource = "cluster-config"

// Declaration is one repo's declared intent. Every section is optional;
// declaring nothing is not an error.
type Declaration struct {
	Service  Service   `yaml:"service"`
	Consumes []Seam    `yaml:"consumes"`
	Serves   []Surface `yaml:"serves"`
	Layers   []Layer   `yaml:"layers"`

	// Components and Rules are the repo's declared desired architecture — its
	// law, not a decision about it. They live here, in the always-validated
	// declaration beside the code they govern, rather than on wiki pages:
	// pages record decisions, and a decision references rule ids — it never
	// carries the rules themselves.
	Components []ConstraintComponent `yaml:"components"`
	Rules      []ConstraintRule      `yaml:"rules"`

	UseRecipe yaml.Node `yaml:"use_recipe"`

	// Source records where this declaration came from: the repo file's path,
	// or ClusterSource. Overridden reports that a repo file existed but the
	// cluster entry won (wholesale, per the intent-file-format decision).
	Source     string `yaml:"-"`
	Overridden bool   `yaml:"-"`
}

// Service is the repo's declared identity.
type Service struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Seam is a declared repo-to-repo dependency: this repo intends to call
// `Repo` via the named mechanism.
type Seam struct {
	Repo string `yaml:"repo"`
	Via  string `yaml:"via"`
}

// Surface is a declared serving commitment: this repo offers callers the
// named mechanism.
type Surface struct {
	Via         string `yaml:"via"`
	Description string `yaml:"description"`
}

// Layer is one entry of a declared layer order, top-most first; Paths are
// path globs relative to the repo root.
type Layer struct {
	Name  string   `yaml:"name"`
	Paths []string `yaml:"paths"`
}

// ValidLayerPath reports whether a layer path is one of the two forms the
// classifier implements: an exact module path (`src/lib`), or a `prefix/**`
// subtree matching the prefix and everything under it.
//
// Until this existed, `layers:` paths were the one declaration field with NO
// form validation at all. Anything at all was accepted, compiled into an intent
// fact, matched against every module, and — when it matched none, which is what
// every unsupported glob form does — left a layer order that validated clean and
// governed nothing. Rejecting the form here is what turns that into a message
// naming what is allowed, at the moment the author can still fix it.
//
// The dialect is deliberately narrower than the component `match` dialect: it
// has no basename form. A layer is a REGION of the tree, and a rule about files
// named `*_controller.rb` wherever they live is a component, not a layer.
func ValidLayerPath(p string) bool {
	if p == "" {
		return false
	}
	prefix, isSubtree := strings.CutSuffix(p, "/**")
	if isSubtree && prefix == "" {
		return false
	}
	if !isSubtree {
		prefix = p
	}
	return !strings.ContainsAny(prefix, "*?[]{}\\")
}

// layerPathProblems reports every malformed path in one layer entry. Shared by
// the repo-file and page forms so the two cannot drift into accepting different
// dialects for the same field.
func layerPathProblems(loc string, l Layer) []string {
	var problems []string
	for i, p := range l.Paths {
		if !ValidLayerPath(p) {
			problems = append(problems, fmt.Sprintf("%s.paths[%d]: %q must be an exact path (src/lib) or a prefix/** subtree (src/lib/**) — a layer names a region of the tree, so no other glob form is read", loc, i, p))
		}
	}
	return problems
}

// Validate checks a declaration's vocabulary and shape. Every reported
// problem names what is allowed — a parse error a user can act on without
// reading source.
func (d *Declaration) Validate() error {
	if problems := d.Problems(); len(problems) > 0 {
		return fmt.Errorf("invalid intent declaration: %s", strings.Join(problems, "; "))
	}
	return nil
}

// Problems returns every validation problem individually — the list Validate
// folds into its single error. Exported for surfaces that report per problem
// rather than fail on the first (`constraints lint`), so authoring feedback
// and snapshot-time validation can never check different rules.
func (d *Declaration) Problems() []string {
	var problems []string
	for i, c := range d.Consumes {
		if c.Repo == "" {
			problems = append(problems, fmt.Sprintf("consumes[%d]: missing repo", i))
		}
		if !facts.AllViaKinds[c.Via] {
			problems = append(problems, fmt.Sprintf("consumes[%d]: via %q is not a linker mechanism (allowed: %s)", i, c.Via, allowedViaKinds()))
		}
	}
	for i, sv := range d.Serves {
		if !facts.AllViaKinds[sv.Via] {
			problems = append(problems, fmt.Sprintf("serves[%d]: via %q is not a linker mechanism (allowed: %s)", i, sv.Via, allowedViaKinds()))
		}
	}
	for i, l := range d.Layers {
		if l.Name == "" {
			problems = append(problems, fmt.Sprintf("layers[%d]: missing name", i))
		}
		if len(l.Paths) == 0 {
			problems = append(problems, fmt.Sprintf("layers[%d] (%s): no paths", i, l.Name))
		}
		problems = append(problems, layerPathProblems(fmt.Sprintf("layers[%d] (%s)", i, l.Name), l)...)
	}
	if !d.UseRecipe.IsZero() {
		problems = append(problems, fmt.Sprintf("use_recipe is not inline vocabulary — instantiations live in %s/*.yaml files, beside the code each bounded context governs", ConstraintsDirName))
	}
	return append(problems, constraintProblems(d.Components, d.Rules)...)
}

// AllowedVia reports whether a via names a linker mechanism.
func AllowedVia(via string) bool { return facts.AllViaKinds[via] }

func allowedViaKinds() string {
	kinds := make([]string, 0, len(facts.AllViaKinds))
	for k := range facts.AllViaKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return strings.Join(kinds, ", ")
}

// Parse reads one declaration from YAML and validates it.
func Parse(data []byte) (*Declaration, error) {
	var d Declaration
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parsing intent declaration: %w", err)
	}
	d.Normalize()
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// LoadRepoFile reads a repo's declared intent: enola-intent.yaml if present,
// plus any enola/constraints/*.yaml files merged after the inline sections.
// Validation runs on the merged declaration, so a rule in one constraints
// file may name a component declared inline or in another file. Nothing
// declared anywhere is (nil, nil) — declaring nothing is not an error;
// anything present-but-invalid is an error, never silently ignored.
func LoadRepoFile(repoPath string) (*Declaration, error) {
	path := filepath.Join(repoPath, RepoFileName)
	var fromFile *Declaration
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", path, err)
	default:
		var d Declaration
		if err := yaml.Unmarshal(data, &d); err != nil {
			return nil, fmt.Errorf("%s: parsing intent declaration: %w", path, err)
		}
		d.Normalize()
		// Repo-relative on purpose: an absolute path would embed the machine's
		// checkout location in a fact and break cross-machine byte-identity.
		d.Source = RepoFileName
		fromFile = &d
	}
	files, fileProblems, err := LoadConstraintsDir(repoPath)
	if err != nil {
		return nil, err
	}
	recipes, recipeFileProblems, err := LoadRecipesDir(repoPath)
	if err != nil {
		return nil, err
	}
	recipeProblems, _ := RecipeProblems(recipes)
	problems := append(append(fileProblems, recipeFileProblems...), recipeProblems...)
	merged := MergeConstraintsFiles(fromFile, files)
	merged, expandProblems := ApplyRecipes(merged, files, recipes)
	problems = append(problems, expandProblems...)
	if len(problems) > 0 {
		// Every path that rejects a declaration says so in the same words, so a
		// reader downstream — the benchmark oracle among them — can tell a
		// declaration this build cannot read from a generation that failed for
		// any other reason. The problems this path collects are declaration
		// problems and nothing else.
		return nil, fmt.Errorf("%s: invalid intent declaration: %s", repoPath, strings.Join(problems, "; "))
	}
	if merged == nil {
		return nil, nil
	}
	if err := merged.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(repoPath, filepath.FromSlash(merged.Source)), err)
	}
	return merged, nil
}

// Resolve applies the composition rule to one repo's two possible sources:
// the cluster entry wins wholesale when both exist, and the result records
// that the repo file was overridden. Either input may be nil.
func Resolve(fromFile, fromCluster *Declaration) *Declaration {
	if fromCluster == nil {
		return fromFile
	}
	resolved := *fromCluster
	resolved.Source = ClusterSource
	resolved.Overridden = fromFile != nil
	return &resolved
}
