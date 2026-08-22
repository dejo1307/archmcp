package intent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const RecipesDirName = "enola/recipes"

type RecipeRole struct {
	Name string `yaml:"name"`
	// Optional marks a role an instantiation may leave unbound: the rules
	// that reference it are expanded away for that instantiation and the
	// lint surface says so, rather than the load failing. A recipe grows a
	// role this way without breaking every repository that already binds it.
	Optional bool `yaml:"optional"`
	// Selector defaults a binding inherits key by key when it gives none of
	// its own: a role whose members are known by what they carry rather than
	// by where they sit declares it once here, and a binding that gives only
	// paths (or nothing, when the default match suffices) still selects them.
	Match       []string       `yaml:"match"`
	Kind        string         `yaml:"kind"`
	NamePattern string         `yaml:"name_pattern"`
	Where       map[string]any `yaml:"where"`
}

func (role RecipeRole) defaulted() bool {
	return len(role.Match) > 0 || role.Kind != "" || role.NamePattern != "" || len(role.Where) > 0
}

type Recipe struct {
	Path      string           `yaml:"-"`
	Name      string           `yaml:"recipe"`
	Roles     []RecipeRole     `yaml:"roles"`
	Rules     []ConstraintRule `yaml:"rules"`
	UseRecipe yaml.Node        `yaml:"use_recipe"`
}

type RecipeBinding struct {
	Service     string         `yaml:"service"`
	Match       []string       `yaml:"match"`
	Kind        string         `yaml:"kind"`
	NamePattern string         `yaml:"name_pattern"`
	Where       map[string]any `yaml:"where"`
	Owns        string         `yaml:"owns"`
	Ancestor    string         `yaml:"ancestor"`
	Public      []string       `yaml:"public"`
}

type InstanceExemption struct {
	Rule    string `yaml:"rule"`
	Witness string `yaml:"witness"`
	Owner   string `yaml:"owner"`
	Because string `yaml:"because"`
	Since   string `yaml:"since"`
}

type RecipeInstantiation struct {
	Recipe string                   `yaml:"recipe"`
	As     string                   `yaml:"as"`
	Bind   map[string]RecipeBinding `yaml:"bind"`
	Mode   string                   `yaml:"mode"`
	Exempt []InstanceExemption      `yaml:"exempt"`
}

func LoadRecipesDir(repoPath string) ([]Recipe, []string, error) {
	dir := filepath.Join(repoPath, filepath.FromSlash(RecipesDirName))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if dirAbsent(err) {
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
	var recipes []Recipe
	var problems []string
	for _, name := range names {
		relPath := RecipesDirName + "/" + name
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, fmt.Errorf("reading %s: %w", filepath.Join(dir, name), err)
		}
		var rec Recipe
		if err := yaml.Unmarshal(data, &rec); err != nil {
			problems = append(problems, fmt.Sprintf("%s: not parseable as YAML: %v", relPath, err))
			continue
		}
		rec.Normalize()
		rec.Path = relPath
		recipes = append(recipes, rec)
	}
	return recipes, problems, nil
}

func RecipeProblems(recipes []Recipe) ([]string, []string) {
	var problems, warnings []string
	recipeSource := map[string]string{}
	for _, rec := range recipes {
		if !validToken(rec.Name) {
			problems = append(problems, fmt.Sprintf("%s: recipe %q must be a lowercase token", rec.Path, rec.Name))
		} else if declaredBy, seen := recipeSource[rec.Name]; seen {
			problems = append(problems, fmt.Sprintf("%s: recipe %q is already declared by %s", rec.Path, rec.Name, declaredBy))
		} else {
			recipeSource[rec.Name] = rec.Path
		}
		if !rec.UseRecipe.IsZero() {
			problems = append(problems, fmt.Sprintf("%s: use_recipe inside a recipe is not supported — a recipe expands into rules, never into other recipes; instantiations live in %s/*.yaml files", rec.Path, ConstraintsDirName))
		}
		roleNames := map[string]bool{}
		for i, role := range rec.Roles {
			loc := fmt.Sprintf("%s: roles[%d]", rec.Path, i)
			if !validToken(role.Name) {
				problems = append(problems, fmt.Sprintf("%s: name %q must be a lowercase token", loc, role.Name))
			} else if roleNames[role.Name] {
				problems = append(problems, fmt.Sprintf("%s: role %q is declared twice in this recipe", loc, role.Name))
			}
			roleNames[role.Name] = true
		}
		if len(rec.Roles) == 0 {
			problems = append(problems, fmt.Sprintf("%s: recipe %q declares no roles — a recipe with no role slots binds nothing", rec.Path, rec.Name))
		}
		if len(rec.Rules) == 0 {
			problems = append(problems, fmt.Sprintf("%s: recipe %q declares no rules — a recipe with no rules instantiates nothing", rec.Path, rec.Name))
		}
		ruleIDs := map[string]bool{}
		referenced := map[string]bool{}
		for i, r := range rec.Rules {
			loc := fmt.Sprintf("%s: rules[%d]", rec.Path, i)
			if !validToken(r.ID) {
				problems = append(problems, fmt.Sprintf("%s: id %q must be a lowercase token", loc, r.ID))
			} else if ruleIDs[r.ID] {
				problems = append(problems, fmt.Sprintf("%s: id %q is declared twice in this recipe", loc, r.ID))
			}
			ruleIDs[r.ID] = true
			if len(r.Exempt) > 0 {
				problems = append(problems, fmt.Sprintf("%s (%s): exempt is not recipe vocabulary — exemptions name concrete witnesses, so they attach at the instantiation (use_recipe exempt:), scoped to a rule", loc, r.ID))
			}
			problems = append(problems, ruleFormProblems(loc, r, roleNames, "role")...)
			for _, role := range ruleRoleReferences(r) {
				referenced[role] = true
			}
		}
		for _, role := range rec.Roles {
			if validToken(role.Name) && !referenced[role.Name] {
				warnings = append(warnings, fmt.Sprintf("%s: warning — role %q is referenced by no rule (dead role): a binding for it would select facts no rule governs", rec.Path, role.Name))
			}
		}
	}
	return problems, warnings
}

func ruleRoleReferences(r ConstraintRule) []string {
	refs := []string{
		r.Forbid, r.ForbidReach, r.To, r.Allow, r.Protect, r.Private,
		r.ForbidFact, r.Cap, r.Require, r.RequireEdge, r.RequireDefines,
		r.RequireName, r.ForbidName, r.ForbidCycles, r.Independent, r.Protocol, r.Guide,
	}
	refs = append(refs, r.Only...)
	refs = append(refs, r.Owners...)
	refs = append(refs, r.Except...)
	refs = append(refs, r.Steps...)
	refs = append(refs, r.Among...)
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

func recipeReferencedRoles(rec Recipe) []string {
	declared := map[string]bool{}
	for _, role := range rec.Roles {
		declared[role.Name] = true
	}
	set := map[string]bool{}
	for _, r := range rec.Rules {
		for _, role := range ruleRoleReferences(r) {
			if declared[role] {
				set[role] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for role := range set {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}

func ApplyRecipes(d *Declaration, files []ConstraintsFile, recipes []Recipe) (*Declaration, []string) {
	components, rules, problems := ExpandInstantiations(files, recipes)
	if len(components) == 0 && len(rules) == 0 {
		return d, problems
	}
	if d == nil {
		d = &Declaration{Source: ConstraintsDirName}
	}
	d.Components = append(d.Components, components...)
	d.Rules = append(d.Rules, rules...)
	return d, problems
}

func ExpandInstantiations(files []ConstraintsFile, recipes []Recipe) ([]ConstraintComponent, []ConstraintRule, []string) {
	recipeByName := map[string]Recipe{}
	loaded := make([]string, 0, len(recipes))
	for _, rec := range recipes {
		if _, seen := recipeByName[rec.Name]; !seen {
			recipeByName[rec.Name] = rec
			loaded = append(loaded, rec.Name)
		}
	}
	sort.Strings(loaded)
	var components []ConstraintComponent
	var rules []ConstraintRule
	var problems []string
	instanceSource := map[string]string{}
	for _, f := range files {
		for i, inst := range f.UseRecipe {
			loc := fmt.Sprintf("%s: use_recipe[%d]", f.Path, i)
			ok := true
			if !validToken(inst.As) {
				problems = append(problems, fmt.Sprintf("%s: as %q must be a lowercase token — the instance name prefixes every expanded rule id", loc, inst.As))
				ok = false
			} else if declaredBy, seen := instanceSource[inst.As]; seen {
				problems = append(problems, fmt.Sprintf("%s: instance %q is already declared by %s — the expanded rule ids would collide", loc, inst.As, declaredBy))
				ok = false
			} else {
				instanceSource[inst.As] = f.Path
			}
			rec, found := recipeByName[inst.Recipe]
			if !found {
				problems = append(problems, fmt.Sprintf("%s: recipe %q names no loaded recipe (loaded: %s)", loc, inst.Recipe, loadedRecipeList(loaded)))
				continue
			}
			if inst.Mode != "" && !AllowedRuleModes[inst.Mode] && !AllowedGuidanceModes[inst.Mode] {
				problems = append(problems, fmt.Sprintf("%s: mode %q is not an enforcement or guidance mode (allowed: advisory, notify, ratchet, strict)", loc, inst.Mode))
				ok = false
			}
			declaredRoles := map[string]bool{}
			roleNames := make([]string, 0, len(rec.Roles))
			for _, role := range rec.Roles {
				declaredRoles[role.Name] = true
				roleNames = append(roleNames, role.Name)
			}
			for _, bound := range sortedBindRoles(inst.Bind) {
				if !declaredRoles[bound] {
					problems = append(problems, fmt.Sprintf("%s: bind %q names no role of recipe %s (roles: %s)", loc, bound, rec.Name, strings.Join(roleNames, ", ")))
					ok = false
				}
			}
			optional := map[string]bool{}
			for _, role := range rec.Roles {
				if role.Optional || role.defaulted() {
					optional[role.Name] = true
				}
			}
			for _, role := range recipeReferencedRoles(rec) {
				if _, bound := inst.Bind[role]; !bound && !optional[role] {
					problems = append(problems, fmt.Sprintf("%s: recipe %s's rules reference role %q and the instantiation binds no paths to it", loc, rec.Name, role))
					ok = false
				}
			}
			recipeRuleIDs := map[string]bool{}
			ruleList := make([]string, 0, len(rec.Rules))
			for _, rr := range rec.Rules {
				recipeRuleIDs[rr.ID] = true
				ruleList = append(ruleList, rr.ID)
			}
			exemptByRule := map[string][]ConstraintExemption{}
			for j, ex := range inst.Exempt {
				if !recipeRuleIDs[ex.Rule] {
					problems = append(problems, fmt.Sprintf("%s: exempt[%d] names rule %q, which recipe %s does not declare (rules: %s)", loc, j, ex.Rule, rec.Name, strings.Join(ruleList, ", ")))
					ok = false
					continue
				}
				exemptByRule[ex.Rule] = append(exemptByRule[ex.Rule], ConstraintExemption{
					Witness: ex.Witness, Owner: ex.Owner, Because: ex.Because, Since: ex.Since,
				})
			}
			if !ok {
				continue
			}
			components = append(components, expandBindings(rec, inst, f.Path)...)
			rules = append(rules, expandRules(rec, inst, exemptByRule, f.Path)...)
		}
	}
	return components, rules, problems
}

func expandBindings(rec Recipe, inst RecipeInstantiation, sourceFile string) []ConstraintComponent {
	var out []ConstraintComponent
	for _, role := range rec.Roles {
		b, bound := inst.Bind[role.Name]
		if !bound && !role.defaulted() {
			continue
		}
		if len(b.Match) == 0 {
			b.Match = append([]string(nil), role.Match...)
		}
		if b.Kind == "" {
			b.Kind = role.Kind
		}
		if b.NamePattern == "" {
			b.NamePattern = role.NamePattern
		}
		if b.Where == nil {
			b.Where = role.Where
		}
		out = append(out, ConstraintComponent{
			Name:        inst.As + "/" + role.Name,
			Service:     b.Service,
			Match:       append([]string(nil), b.Match...),
			Kind:        b.Kind,
			NamePattern: b.NamePattern,
			Where:       b.Where,
			Owns:        b.Owns,
			Ancestor:    b.Ancestor,
			Public:      append([]string(nil), b.Public...),
			SourceFile:  sourceFile,
			Recipe:      rec.Name,
			Instance:    inst.As,
			Role:        role.Name,
		})
	}
	return out
}

func expandRules(rec Recipe, inst RecipeInstantiation, exemptByRule map[string][]ConstraintExemption, sourceFile string) []ConstraintRule {
	bind := func(role string) string {
		if role == "" {
			return ""
		}
		return inst.As + "/" + role
	}
	bindAll := func(roles []string) []string {
		if len(roles) == 0 {
			return nil
		}
		bound := make([]string, 0, len(roles))
		for _, role := range roles {
			bound = append(bound, bind(role))
		}
		return bound
	}
	out := make([]ConstraintRule, 0, len(rec.Rules))
	for _, rr := range rec.Rules {
		if unbound := unboundOptionalRole(rec, inst, rr); unbound != "" {
			continue
		}
		n := rr
		n.ID = inst.As + "/" + rr.ID
		n.Forbid = bind(rr.Forbid)
		n.ForbidReach = bind(rr.ForbidReach)
		n.To = bind(rr.To)
		n.Allow = bind(rr.Allow)
		n.Only = bindAll(rr.Only)
		n.Protect = bind(rr.Protect)
		n.Owners = bindAll(rr.Owners)
		n.Private = bind(rr.Private)
		n.Except = bindAll(rr.Except)
		n.ForbidFact = bind(rr.ForbidFact)
		n.Cap = bind(rr.Cap)
		n.Require = bind(rr.Require)
		n.RequireEdge = bind(rr.RequireEdge)
		n.RequireDefines = bind(rr.RequireDefines)
		n.RequireName = bind(rr.RequireName)
		n.ForbidName = bind(rr.ForbidName)
		n.ForbidCycles = bind(rr.ForbidCycles)
		n.Among = bindAll(rr.Among)
		n.Independent = bind(rr.Independent)
		n.Protocol = bind(rr.Protocol)
		n.Steps = bindAll(rr.Steps)
		n.Guide = bind(rr.Guide)
		n.Exemplars = append([]string(nil), rr.Exemplars...)
		n.Owns = nil
		for _, o := range rr.Owns {
			n.Owns = append(n.Owns, ComponentOwnership{Component: bind(o.Component), Owns: o.Owns})
		}
		if rr.WhenPropContains != nil {
			when := *rr.WhenPropContains
			n.WhenPropContains = &when
		}
		if rr.MustPropContain != nil {
			must := *rr.MustPropContain
			n.MustPropContain = &must
		}
		if inst.Mode != "" {
			n.Mode = inst.Mode
		}
		n.Exempt = append([]ConstraintExemption(nil), exemptByRule[rr.ID]...)
		n.SourceFile = sourceFile
		n.Recipe = rec.Name
		n.Instance = inst.As
		out = append(out, n)
	}
	return out
}

func sortedBindRoles(bind map[string]RecipeBinding) []string {
	roles := make([]string, 0, len(bind))
	for role := range bind {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

func loadedRecipeList(loaded []string) string {
	if len(loaded) == 0 {
		return "none — " + RecipesDirName + "/ declares nothing"
	}
	return strings.Join(loaded, ", ")
}

// unboundOptionalRole names the optional role a rule references that the
// instantiation left unbound, or "" when every role the rule needs is bound.
func unboundOptionalRole(rec Recipe, inst RecipeInstantiation, r ConstraintRule) string {
	optional := map[string]bool{}
	for _, role := range rec.Roles {
		if role.Optional {
			optional[role.Name] = true
		}
		if role.defaulted() {
			delete(optional, role.Name)
		}
	}
	for _, role := range ruleRoleReferences(r) {
		if _, bound := inst.Bind[role]; !bound && optional[role] {
			return role
		}
	}
	return ""
}

// UnboundOptionalRules lists, per instantiation, the recipe rules expanded
// away because an optional role was left unbound, so the lint surface can say
// which laws a binding did not take.
func UnboundOptionalRules(recipes []Recipe, files []ConstraintsFile) []string {
	byName := map[string]Recipe{}
	for _, r := range recipes {
		byName[r.Name] = r
	}
	var out []string
	for _, f := range files {
		for _, inst := range f.UseRecipe {
			rec, ok := byName[inst.Recipe]
			if !ok {
				continue
			}
			for _, rr := range rec.Rules {
				if role := unboundOptionalRole(rec, inst, rr); role != "" {
					out = append(out, fmt.Sprintf("%s: use_recipe %s leaves optional role %q unbound, so rule %s is not in force", f.Path, inst.As, role, rr.ID))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// RequiredRoles lists the roles a recipe's rules reference that an
// instantiation must bind: every referenced role that is not optional.
func RequiredRoles(rec Recipe) []string {
	optional := map[string]bool{}
	for _, role := range rec.Roles {
		if role.Optional || role.defaulted() {
			optional[role.Name] = true
		}
	}
	var out []string
	for _, role := range recipeReferencedRoles(rec) {
		if !optional[role] {
			out = append(out, role)
		}
	}
	sort.Strings(out)
	return out
}
