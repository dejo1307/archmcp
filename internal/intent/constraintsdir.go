package intent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConstraintsDirName is the per-repo constraints directory, beside
// enola-intent.yaml at the repo root. Like the declaration file it cannot
// live under .enola/, which is gitignored output — constraints are source,
// reviewed beside the code they govern. Splitting them into per-domain files
// keeps a large declaration reviewable and lets CODEOWNERS route each file
// to the team that owns its domain; inline declaration remains legal for
// small repos.
const ConstraintsDirName = "enola/constraints"

// ConstraintsFile is one parsed enola/constraints/*.yaml: the declaration's
// constraints halves — components and rules, same schema as the inline
// sections — under its repo-relative path.
type ConstraintsFile struct {
	Path       string                `yaml:"-"`
	Components []ConstraintComponent `yaml:"components"`
	Rules      []ConstraintRule      `yaml:"rules"`
	UseRecipe  []RecipeInstantiation `yaml:"use_recipe"`
}

// LoadConstraintsDir reads a repo's enola/constraints directory. Files come
// back in sorted filename order — the merge order, so loading is
// deterministic — with every component and rule stamped with its declaring
// file's repo-relative path. Per-file parse problems each cite their file
// and are returned beside the files that did parse, so a lint surface can
// report all of them; the error covers only a directory or file that could
// not be read at all. An absent or empty directory is (nil, nil, nil) —
// declaring nothing is not an error.
func LoadConstraintsDir(repoPath string) ([]ConstraintsFile, []string, error) {
	dir := filepath.Join(repoPath, filepath.FromSlash(ConstraintsDirName))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	var files []ConstraintsFile
	var problems []string
	for _, name := range names {
		relPath := ConstraintsDirName + "/" + name
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, fmt.Errorf("reading %s: %w", filepath.Join(dir, name), err)
		}
		var f ConstraintsFile
		if err := yaml.Unmarshal(data, &f); err != nil {
			problems = append(problems, fmt.Sprintf("%s: not parseable as YAML: %v", relPath, err))
			continue
		}
		f.Path = relPath
		for i := range f.Components {
			f.Components[i].SourceFile = relPath
		}
		for i := range f.Rules {
			f.Rules[i].SourceFile = relPath
		}
		files = append(files, f)
	}
	return files, problems, nil
}

// MergeConstraintsFiles appends every constraints file's components and rules
// onto the declaration, after any declared inline, in the loader's sorted
// file order — a stable merge, so the resolved sets are a function of what
// the files declare and never of filesystem enumeration. A nil declaration
// with files present becomes a constraints-only declaration sourced from the
// directory: a repo may declare its law without declaring a service.
func MergeConstraintsFiles(d *Declaration, files []ConstraintsFile) *Declaration {
	if len(files) == 0 {
		return d
	}
	if d == nil {
		d = &Declaration{Source: ConstraintsDirName}
	}
	for _, f := range files {
		d.Components = append(d.Components, f.Components...)
		d.Rules = append(d.Rules, f.Rules...)
	}
	return d
}
