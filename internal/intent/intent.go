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

// Validate checks a declaration's vocabulary and shape. Every reported
// problem names what is allowed — a parse error a user can act on without
// reading source.
func (d *Declaration) Validate() error {
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
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid intent declaration: %s", strings.Join(problems, "; "))
	}
	return nil
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
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// LoadRepoFile reads a repo's enola-intent.yaml if present. A missing file is
// (nil, nil) — declaring nothing is not an error; a present-but-invalid file
// is an error, never silently ignored.
func LoadRepoFile(repoPath string) (*Declaration, error) {
	path := filepath.Join(repoPath, RepoFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	d, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Repo-relative on purpose: an absolute path would embed the machine's
	// checkout location in a fact and break cross-machine byte-identity.
	d.Source = RepoFileName
	return d, nil
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
