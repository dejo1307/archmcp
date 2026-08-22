package intent

import (
	"embed"
	"fmt"
	"path"
	"sort"

	"gopkg.in/yaml.v3"
)

// Recipes that ship with the binary: convention sets a team can adopt without
// authoring them, bound to its own parts at the instantiation site.
//
// They are shipped rather than printed as examples because a convention nobody
// can adopt in one line is a convention nobody adopts. A repository still
// authors its own under enola/recipes/, and a local recipe of the same name
// replaces the shipped one entirely: what a team wrote about its own codebase
// beats what arrived in a binary, and the override is reported rather than
// silent so nobody has to wonder which one ran.
//
//go:embed recipes/*.yaml
var builtinRecipeFiles embed.FS

// BuiltinRecipeSource is what a shipped recipe cites as its declaring file, so
// a verdict traced back to one says where it came from instead of naming a
// path that exists in no repository.
const BuiltinRecipeSource = "enola:recipes"

// BuiltinRecipes returns the shipped recipes in name order. A shipped recipe
// that does not parse is a build defect rather than a user error, so it is
// returned as a problem naming the file and the rest still load.
func BuiltinRecipes() ([]Recipe, []string) {
	entries, err := builtinRecipeFiles.ReadDir("recipes")
	if err != nil {
		return nil, []string{fmt.Sprintf("built-in recipes: %v", err)}
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	var recipes []Recipe
	var problems []string
	for _, name := range names {
		data, err := builtinRecipeFiles.ReadFile(path.Join("recipes", name))
		if err != nil {
			problems = append(problems, fmt.Sprintf("built-in recipe %s: %v", name, err))
			continue
		}
		var rec Recipe
		if err := yaml.Unmarshal(data, &rec); err != nil {
			problems = append(problems, fmt.Sprintf("built-in recipe %s: %v", name, err))
			continue
		}
		rec.Normalize()
		rec.Path = BuiltinRecipeSource + "/" + name
		recipes = append(recipes, rec)
	}
	return recipes, problems
}

// MergeBuiltinRecipes puts the shipped recipes behind the repository's own. A
// local recipe of the same name replaces the shipped one and the replacement is
// reported, so the collision reads as the deliberate override it is rather than
// as the duplicate-name error an accidental clash raises.
func MergeBuiltinRecipes(local []Recipe) ([]Recipe, []string) {
	builtin, problems := BuiltinRecipes()
	authored := make(map[string]string, len(local))
	for _, rec := range local {
		authored[rec.Name] = rec.Path
	}
	out := make([]Recipe, 0, len(local)+len(builtin))
	out = append(out, local...)
	var notes []string
	for _, rec := range builtin {
		if where, overridden := authored[rec.Name]; overridden {
			notes = append(notes, fmt.Sprintf("%s replaces the recipe %q that ships with enola", where, rec.Name))
			continue
		}
		out = append(out, rec)
	}
	return out, append(problems, notes...)
}
