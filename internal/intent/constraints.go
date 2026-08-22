package intent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// ConstraintComponent names a set of measured facts by where they live: any
// fact whose file falls under a match pattern (and, when narrowed, whose kind
// or name agrees) is a member. Components exist so rules can speak about
// the architecture in the declaration's own vocabulary — "the domain", "the
// adapters" — instead of repeating path lists per rule. Service scopes the
// selector to one repository of a multi-repo snapshot, by its exact repo
// label: members are facts of that repo, and every other narrowing ANDs with
// it. A component may carry a service with no match patterns — that is the
// whole service — where a serviceless component still needs at least one.
//
// NamePattern narrows to a family of fact names rather than to one name: it
// speaks the bounded name dialect ValidNamePattern admits and MatchBoundedName
// applies — an exact name, one prefix*, or one *suffix — which is the same
// pair require_name's pattern and require's when_edge_to are held to. A
// starless pattern is exact equality, so what a component selected before the
// dialect reached it, it selects still. Sharing the validator with the matcher
// is the point: a name family a declaration may write and a name family the
// evaluator recognizes cannot come apart.
//
// Where selects by what the facts CARRY instead of where they sit: a
// conjunction of property tests over the props the extractors measured, so a
// component can name "the view components" or "the storage models" without a
// directory appearing anywhere. It ANDs with every other narrowing for the
// same reason service does — every field on this struct narrows, none widens —
// so a component carrying both a match and a where is the path scope
// intersected with the predicate, which is how a trusted path scope gets
// sharpened without being replaced. A where alone is legal and needs no match:
// the predicate IS the selector.
type ConstraintComponent struct {
	Name        string         `yaml:"name"`
	Service     string         `yaml:"service"`
	Match       []string       `yaml:"match"`
	Kind        string         `yaml:"kind"`
	NamePattern string         `yaml:"name_pattern"`
	Where       map[string]any `yaml:"where"`
	// Ancestor names a class every member descends from, read transitively
	// over resolved ancestry a provider emitted rather than over the one-level
	// superclass text the extractor records. It is a separate key from a
	// `where: {superclass:}` pair because the two are different claims.
	Ancestor string `yaml:"ancestor"`

	// SourceFile is the repo-relative enola/constraints file that declared
	// this component, stamped at load time; empty means declared inline. It
	// is provenance, never YAML input: the compiled fact's File carries it,
	// so a verdict cites the declaring file rather than the merged whole.
	SourceFile string `yaml:"-"`

	Recipe   string `yaml:"-"`
	Instance string `yaml:"-"`
	Role     string `yaml:"-"`
}

// ConstraintRule declares one enforcement statement about components. Exactly
// one of thirteen forms selects what the rule says: Forbid/To (this component must
// not reach that one), ForbidReach/To (this component must not reach that one
// through ANY measured path — the transitive form, walked breadth-first under
// a hard depth cap; Via narrows the walked edge kinds and defaults to every
// rule-via kind), Allow/Only (this component's edges may land only in the
// named components), Protect/Owners (only the named components may reach this
// one — the ownership form, walked from the whole graph rather than from a
// source component), Private/Except (the component's non-exported members may
// be reached only from inside the component or from the Except components —
// the visibility form, verdicted over every rule-via edge kind against the
// extractor-measured exported prop, so it needs no Via of its own), ForbidFact
// (this component's membership must be empty), Cap/MaxMembers (this
// component's membership must not exceed a count), or Require (members of this
// component matching WhenPropContains must satisfy MustPropContain — the
// property form, verdicting what a member fact carries rather than what edges
// it makes; "every storage member whose columns contain company_id must have
// fk_constraints containing company_id->companies". Its second antecedent,
// WhenEdgeTo/Via, selects on the member's OWN outgoing edges instead of on a
// prop it carries — "every getter that calls reactiveUnwrap must carry the
// cached decorator" — with the far end a literal, never a component, so the
// form still resolves entirely on the member fact. Both antecedents together
// narrow, never widen: a member must satisfy each declared one to be in
// scope), or RequireDefines/Method
// (every class-kind member symbol must have a measured method symbol of the
// given name — the protocol form; a class whose definition could ride a
// mixin, an included module, or a superclass is out of scope, fail closed,
// because the check cannot see through composition it did not measure), or
// RequireName/Pattern (every member fact's name must match a bounded
// convention pattern — prefix*, *suffix, or an exact name; the naming form
// speaks the same deliberately small dialect philosophy match does, so a
// pattern the evaluator would silently mis-apply cannot be declared), or
// RequireEdge/Via/Direction (every member must have at least one measured edge
// of the Via kind — inbound means some source points at the member, outbound
// means the member points somewhere — optionally scoped by To to a counterpart
// component; the existential form, demanding an edge EXISTS where every other
// edge form forbids one, so an orphaned event or an unconsumed route is a
// breach instead of invisible; a member whose edge visibility the snapshot
// cannot demonstrate for that file kind is skipped with a named count, fail
// closed, never silently compliant and never falsely violated), or
// Protocol/Steps/Via (every member of the protocol component that makes a Via
// edge into step K's members must also make Via edges into every step
// 1..K-1's members — the ordered form. What it verdicts is STRUCTURAL protocol
// conformance: a member referencing a later step's surface without referencing
// every prerequisite step's surface, which a static fact graph can decide. It
// is never a runtime-ordering claim — that step 1 is CALLED before step 2 at
// runtime is unverifiable from a static graph, so the compiled fact carries
// verification: structural and a future runtime provider owns the observed
// level. A member touching no step is a bystander the rule does not bind, and
// a member whose file class cannot demonstrate the Via kind is skipped with a
// named count, fail closed), or
// Guide/Message (steering, not law: advice surfaced to whoever is about to
// edit inside the component — "similar implementations here used X; consider
// it" — with optional Exemplars naming prior art by repo-relative file path
// or fact name; exemplar existence is checked at delivery time against the
// snapshot, never at parse time, because prior art may move without the
// advice going stale).
// Unlike a claim (a measurement expected to hold), a rule carries enforcement
// semantics: a breach is a decided-rule finding, and Because — mandatory — is
// the rationale the resulting finding surfaces, so a violation always says why
// the rule exists, not only that it was broken. Mode softens enforcement:
// ratchet (the default) verdicts at full confidence, advisory reports below
// the check gate's floor so the finding surfaces without failing anything.
// A guidance rule takes only the non-enforcing modes — notify (its default:
// contract/hook channel only, never a finding) or advisory — because
// graduation to law means writing a law form, not hardening this one.
type ConstraintRule struct {
	ID          string `yaml:"id"`
	Forbid      string `yaml:"forbid"`
	ForbidReach string `yaml:"forbid_reach"`
	To          string `yaml:"to"`

	// ToName is the forbid form's far end named as a LITERAL rather than
	// resolved as a component, in the same bounded dialect WhenEdgeTo speaks —
	// an exact name, a prefix*, or a *suffix. It exists because a component
	// resolves against measured facts, and the far end of an edge is often
	// something the snapshot never measured a fact for: an external package
	// (`@ember/render-modifiers`), or a function imported from one, whose call
	// target is recorded as a name that resolves to nothing. Those edges are
	// measured on the near side and are exactly what a convention forbids, so
	// refusing to name them made a whole class of written rules unwritable.
	// The literal is compared against the edge target the near end recorded,
	// which is the only thing the graph holds about that end.
	ToName []string `yaml:"to_name"`

	Allow      string   `yaml:"allow"`
	Only       []string `yaml:"only"`
	Protect    string   `yaml:"protect"`
	Owners     []string `yaml:"owners"`
	Private    string   `yaml:"private"`
	Except     []string `yaml:"except"`
	ForbidFact string   `yaml:"forbid_fact"`
	Cap        string   `yaml:"cap"`
	MaxMembers int      `yaml:"max_members"`

	Require          string     `yaml:"require"`
	WhenPropContains *PropMatch `yaml:"when_prop_contains"`

	// WhenEdgeTo is the require form's second antecedent: the member's OWN
	// outgoing edges of the rule's Via kind, matched against literal targets in
	// the same bounded dialect RequireName speaks — an exact name, a prefix*,
	// or a *suffix. Every entry is a LITERAL, never a component name: the form
	// resolves the near end on the member fact and the far end on the string
	// the declaration wrote, so nothing here resolves a second component
	// against a measured edge and the require form stays off the edge-walking
	// list. Present with WhenPropContains, the two antecedents narrow together.
	// A target carries no whitespace of any kind: the compiled rule holds the
	// set as one whitespace-separated prop, so the screen is the same
	// unicode.IsSpace the round trip splits on.
	WhenEdgeTo []string `yaml:"when_edge_to"`

	// WhenVia names the edge kind the WhenEdgeTo antecedent reads on the
	// require_edge form, where Via is already spent naming the edge the rule
	// DEMANDS. The require form has no such collision — its Via is the
	// antecedent's own kind — so WhenVia is refused there rather than offered
	// as a second spelling of the same thing. Two positive edges make the
	// pairing: a member that makes one edge must also make another, which asks
	// the graph for what it holds in both clauses and never for the absence of
	// a fact it may have failed to measure.
	WhenVia string `yaml:"when_via"`

	MustPropContain *PropMatch `yaml:"must_prop_contain"`

	RequireDefines string `yaml:"require_defines"`
	Method         string `yaml:"method"`

	RequireName string `yaml:"require_name"`
	Pattern     string `yaml:"pattern"`

	RequireEdge string `yaml:"require_edge"`
	Direction   string `yaml:"direction"`

	Protocol string   `yaml:"protocol"`
	Steps    []string `yaml:"steps"`

	Guide     string   `yaml:"guide"`
	Message   string   `yaml:"message"`
	Exemplars []string `yaml:"exemplars"`

	Via     string `yaml:"via"`
	Mode    string `yaml:"mode"`
	Because string `yaml:"because"`

	Exempt []ConstraintExemption `yaml:"exempt"`

	// SourceFile mirrors ConstraintComponent.SourceFile: the repo-relative
	// constraints file that declared this rule, empty when inline.
	SourceFile string `yaml:"-"`

	Recipe   string `yaml:"-"`
	Instance string `yaml:"-"`
}

// PropMatch names one space-separated set-valued prop and a value it must (or
// must not fail to) contain as a whole member. Membership, never substring:
// "columns contains company_id" must not be satisfied by parent_company_id.
type PropMatch struct {
	Prop  string `yaml:"prop"`
	Value string `yaml:"value"`
}

type ConstraintExemption struct {
	Witness string `yaml:"witness" json:"witness"`
	Owner   string `yaml:"owner" json:"owner"`
	Because string `yaml:"because" json:"because"`
	Since   string `yaml:"since" json:"since"`
}

func EncodeExemptions(exempt []ConstraintExemption) string {
	if len(exempt) == 0 {
		return ""
	}
	sorted := append([]ConstraintExemption(nil), exempt...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Witness < sorted[j].Witness })
	data, err := json.Marshal(sorted)
	if err != nil {
		return ""
	}
	return string(data)
}

func DecodeExemptions(encoded string) []ConstraintExemption {
	if encoded == "" {
		return nil
	}
	var out []ConstraintExemption
	if err := json.Unmarshal([]byte(encoded), &out); err != nil {
		return nil
	}
	return out
}

// AllowedComponentKinds is the closed fact-kind vocabulary a component selector
// may narrow to — the measured kinds the constraints explainer resolves over,
// plus the two reference kinds it resolves over ONLY when a declaration names
// one. A component that omits `kind:` never acquires a test_ref, a file_ref or
// a lint fact: those carry reference edges (or, for lint, a linter's report)
// rather than architectural coupling, and the explainers that count dependents
// exclude them for that reason. Naming the kind is the opt-in, and it is what
// lets a rule speak about tests at all — "a component test must not reach a
// fixture factory" has a test file at its near end, and no component could
// select one — and what lets a rule wrap a linter's rule: `kind: lint` with a
// `where: lint_rule` and a `forbid_fact` form.
var AllowedComponentKinds = map[string]bool{
	"module": true, "symbol": true, "route": true, "storage": true,
	"test_ref": true, "file_ref": true, "lint": true,
}

func allowedComponentKinds() string {
	return "file_ref, lint, module, route, storage, symbol, test_ref"
}

// AllowedRuleVias is the closed edge vocabulary a rule may forbid — relation
// kinds the graph actually carries, so a rule can only forbid something the
// evaluator can see. Implements covers inheritance and mixin inclusion (the
// include/extend/prepend edges the Ruby extractor emits as dependency
// carriers), which is what makes concern rules — who may include what, and
// what a concern may reach — compose from the existing edge forms.
var AllowedRuleVias = map[string]bool{
	"depends_on": true, "imports": true, "calls": true, "implements": true,
}

func allowedRuleVias() string {
	return "calls, depends_on, implements, imports"
}

// AllowedRuleModes is the closed enforcement-mode vocabulary. Ratchet (the
// default) fails the gate on NEW violations; advisory reports below the gate's
// floor and fails nothing; strict fails on every violation, baselined or not —
// the check gate recognizes strict-titled constraint findings and exempts them
// from delta scoping, with the suppression ledger as the only override.
var AllowedRuleModes = map[string]bool{
	"ratchet": true, "advisory": true, "strict": true,
}

func allowedRuleModes() string {
	return "advisory, ratchet, strict"
}

// AllowedEdgeDirections is the require_edge form's closed direction
// vocabulary: inbound demands that some measured edge land on each member,
// outbound demands that each member make one.
var AllowedEdgeDirections = map[string]bool{
	"inbound": true, "outbound": true,
}

func allowedEdgeDirections() string {
	return "inbound, outbound"
}

// AllowedGuidanceModes is the guidance form's own mode vocabulary — the
// non-enforcing subset, with notify as the default. The enforce-class modes
// (ratchet, strict) are rejected on a guidance rule at validation: steering
// that fails a gate is law wearing the wrong form, and graduation means
// writing a law form on the declaring file.
var AllowedGuidanceModes = map[string]bool{
	"notify": true, "advisory": true,
}

func allowedGuidanceModes() string {
	return "advisory, notify"
}

// constraintProblems validates a declaration's components and rules with the
// same rules every other vocabulary gets: named errors, allowed sets spelled
// out. Returned as problem strings so Declaration.Validate folds them into its
// single error alongside the other sections'.
func constraintProblems(components []ConstraintComponent, rules []ConstraintRule) []string {
	var problems []string
	// Locations are indexed per declaring file — inline entries keep the bare
	// components[i]/rules[i] spelling they always had (the merge appends file
	// entries after them, so inline indices are unchanged), while an entry
	// from a constraints file is cited as its file's own i-th entry.
	perSource := map[string]int{}
	at := func(section, sourceFile string) string {
		key := sourceFile + "\x00" + section
		i := perSource[key]
		perSource[key]++
		if sourceFile == "" {
			return fmt.Sprintf("%s[%d]", section, i)
		}
		return fmt.Sprintf("%s: %s[%d]", sourceFile, section, i)
	}
	declaredIn := func(sourceFile string) string {
		if sourceFile == "" {
			return RepoFileName
		}
		return sourceFile
	}
	componentNames := map[string]bool{}
	componentSource := map[string]string{}
	// Which components carry a predicate, read the same way the evaluator reads
	// it: the COMPILED predicate, so a where declaring only the reserved kind key
	// — which compiles to no property test — is not one, exactly as
	// component.predicated() in the explainer is not.
	predicated := map[string]bool{}
	symbolGranular := map[string]bool{}
	for _, c := range components {
		if len(c.Predicate()) > 0 {
			predicated[c.Name] = true
		}
		if c.Kind == "symbol" {
			symbolGranular[c.Name] = true
		}
	}
	for _, c := range components {
		var loc string
		if c.Recipe != "" {
			loc = fmt.Sprintf("%s: use_recipe %s (recipe %s) role %s", c.SourceFile, c.Instance, c.Recipe, c.Role)
		} else {
			loc = at("components", c.SourceFile)
		}
		if c.Recipe == "" && !validToken(c.Name) {
			problems = append(problems, fmt.Sprintf("%s: name %q must be a lowercase token", loc, c.Name))
		}
		if c.Service != "" && !validToken(c.Service) {
			problems = append(problems, fmt.Sprintf("%s (%s): service %q must be a lowercase token", loc, c.Name, c.Service))
		}
		if !c.Selects() {
			problems = append(problems, fmt.Sprintf("%s (%s): needs at least one match pattern, a service, or a where predicate", loc, c.Name))
		}
		for j, m := range c.Match {
			if !validConstraintMatch(m) {
				problems = append(problems, fmt.Sprintf("%s.match[%d]: %q must be an exact path, a prefix/** subtree, or a **/name basename glob carrying at most one * around a non-empty literal (no other glob forms)", loc, j, m))
			}
		}
		if c.Kind != "" && !AllowedComponentKinds[c.Kind] {
			problems = append(problems, fmt.Sprintf("%s: kind %q is not a measured fact kind (allowed: %s)", loc, c.Kind, allowedComponentKinds()))
		}
		if c.NamePattern != "" && !ValidNamePattern(c.NamePattern) {
			problems = append(problems, fmt.Sprintf("%s (%s): name_pattern %q must be an exact name, a prefix*, or a *suffix (no other pattern forms)", loc, c.Name, c.NamePattern))
		}
		problems = append(problems, ancestorProblems(loc, c)...)
		problems = append(problems, whereProblems(loc, c)...)
		// A name collision is flagged whenever a constraints file is involved,
		// naming both declaring files: a merged set with two definitions of
		// one component has no single answer for what the name selects.
		if componentNames[c.Name] && (c.SourceFile != "" || componentSource[c.Name] != "") {
			problems = append(problems, fmt.Sprintf("%s: component %q is already declared by %s", loc, c.Name, declaredIn(componentSource[c.Name])))
		}
		if !componentNames[c.Name] {
			componentSource[c.Name] = c.SourceFile
		}
		componentNames[c.Name] = true
	}
	ruleIDs := map[string]bool{}
	ruleSource := map[string]string{}
	for _, r := range rules {
		var loc string
		if r.Recipe != "" {
			loc = fmt.Sprintf("%s: use_recipe %s (recipe %s) rule %s", r.SourceFile, r.Instance, r.Recipe, strings.TrimPrefix(r.ID, r.Instance+"/"))
		} else {
			loc = at("rules", r.SourceFile)
		}
		if r.Recipe == "" && !validToken(r.ID) {
			problems = append(problems, fmt.Sprintf("%s: id %q must be a lowercase token", loc, r.ID))
		} else if ruleIDs[r.ID] {
			if r.SourceFile == "" && ruleSource[r.ID] == "" {
				problems = append(problems, fmt.Sprintf("%s: id %q is declared twice in this declaration", loc, r.ID))
			} else {
				problems = append(problems, fmt.Sprintf("%s: id %q is already declared by %s", loc, r.ID, declaredIn(ruleSource[r.ID])))
			}
		}
		if !ruleIDs[r.ID] {
			ruleSource[r.ID] = r.SourceFile
		}
		ruleIDs[r.ID] = true
		problems = append(problems, ruleFormProblems(loc, r, componentNames, "component")...)
		problems = append(problems, predicateRoleProblems(loc, r, predicated, symbolGranular, "component")...)
		if r.Guide != "" && len(r.Exempt) > 0 {
			problems = append(problems, fmt.Sprintf("%s (%s): exempt belongs to the law forms — guidance emits no violations to exempt", loc, r.ID))
		}
		problems = append(problems, exemptionProblems(loc, r.ID, r.Exempt)...)
	}
	return problems
}

func ruleFormProblems(loc string, r ConstraintRule, names map[string]bool, noun string) []string {
	var problems []string
	forms := 0
	for _, form := range RuleForms {
		if form.Subject(r) != "" {
			forms++
		}
	}
	if forms != 1 {
		problems = append(problems, fmt.Sprintf("%s (%s): exactly one of %s selects the rule form (%d given)", loc, r.ID, ruleFormKeys(), forms))
	}
	component := func(field, name string) {
		if name != "" && !names[name] {
			problems = append(problems, fmt.Sprintf("%s (%s): %s %q names no declared %s", loc, r.ID, field, name, noun))
		}
	}
	for _, form := range RuleForms {
		component(form.Key, form.Subject(r))
	}
	edgeForm := r.Forbid != "" || r.Allow != "" || r.Protect != ""
	switch {
	case r.Forbid != "":
		if r.To == "" && len(r.ToName) == 0 {
			problems = append(problems, fmt.Sprintf("%s (%s): forbid needs a far end — a to component, or a to_name literal where the far end is something the snapshot measures no fact for", loc, r.ID))
		}
		component("to", r.To)
	case r.ForbidReach != "":
		if r.To == "" {
			problems = append(problems, fmt.Sprintf("%s (%s): forbid_reach needs a to component", loc, r.ID))
		}
		component("to", r.To)
	case r.Allow != "":
		if len(r.Only) == 0 {
			problems = append(problems, fmt.Sprintf("%s (%s): allow needs at least one only component", loc, r.ID))
		}
		for _, name := range r.Only {
			component("only", name)
		}
	case r.Protect != "":
		if len(r.Owners) == 0 {
			problems = append(problems, fmt.Sprintf("%s (%s): protect needs at least one owners component", loc, r.ID))
		}
		for _, name := range r.Owners {
			component("owners", name)
		}
	case r.Private != "":
		for _, name := range r.Except {
			component("except", name)
		}
	case r.Cap != "":
		if r.MaxMembers < 1 {
			problems = append(problems, fmt.Sprintf("%s (%s): cap needs max_members of at least 1 — an empty surface is forbid_fact's form", loc, r.ID))
		}
	case r.RequireEdge != "":
		if !AllowedRuleVias[r.Via] {
			problems = append(problems, fmt.Sprintf("%s (%s): via %q is not a rule edge kind (allowed: %s)", loc, r.ID, r.Via, allowedRuleVias()))
		}
		if !AllowedEdgeDirections[r.Direction] {
			problems = append(problems, fmt.Sprintf("%s (%s): require_edge needs a direction (allowed: %s) — inbound demands a measured edge land on each member, outbound demands each member make one", loc, r.ID, allowedEdgeDirections()))
		}
		component("to", r.To)
	case r.RequireDefines != "":
		if r.Method == "" || strings.ContainsAny(r.Method, " \t") {
			problems = append(problems, fmt.Sprintf("%s (%s): require_defines needs a method — one whitespace-free method name the class members must define", loc, r.ID))
		}
	case r.RequireName != "":
		if !ValidNamePattern(r.Pattern) {
			problems = append(problems, fmt.Sprintf("%s (%s): require_name needs a pattern that is an exact name, a prefix*, or a *suffix (no other pattern forms)", loc, r.ID))
		}
	case r.Protocol != "":
		if !AllowedRuleVias[r.Via] {
			problems = append(problems, fmt.Sprintf("%s (%s): via %q is not a rule edge kind (allowed: %s)", loc, r.ID, r.Via, allowedRuleVias()))
		}
		if len(r.Steps) < 2 {
			problems = append(problems, fmt.Sprintf("%s (%s): protocol needs at least 2 steps — a single step declares no order to conform to", loc, r.ID))
		}
		seenStep := map[string]bool{}
		for _, step := range r.Steps {
			component("steps", step)
			if seenStep[step] {
				problems = append(problems, fmt.Sprintf("%s (%s): step %q appears twice in the declared order — each step holds one position", loc, r.ID, step))
			}
			seenStep[step] = true
		}
	case r.Require != "":
		if r.MustPropContain == nil || r.MustPropContain.Prop == "" || r.MustPropContain.Value == "" {
			problems = append(problems, fmt.Sprintf("%s (%s): require needs must_prop_contain with prop and value — a requirement that demands nothing enforces nothing", loc, r.ID))
		}
		if r.WhenPropContains != nil && (r.WhenPropContains.Prop == "" || r.WhenPropContains.Value == "") {
			problems = append(problems, fmt.Sprintf("%s (%s): when_prop_contains needs both prop and value; omit it to require of every member", loc, r.ID))
		}
		if len(r.WhenEdgeTo) > 0 && !AllowedRuleVias[r.Via] {
			problems = append(problems, fmt.Sprintf("%s (%s): when_edge_to needs a via (allowed: %s) — the antecedent reads one kind of the member's own outgoing edges, and which kind is never defaulted", loc, r.ID, allowedRuleVias()))
		}
		for j, target := range r.WhenEdgeTo {
			switch {
			case strings.IndexFunc(target, unicode.IsSpace) >= 0:
				problems = append(problems, fmt.Sprintf("%s (%s): when_edge_to[%d] %q must carry no whitespace — the compiled rule holds the targets as one whitespace-separated set, and the split that reads it back is unicode.IsSpace's, so a target carrying any space rune at all would validate as one name and evaluate as another", loc, r.ID, j, target))
			case !ValidNamePattern(target):
				problems = append(problems, fmt.Sprintf("%s (%s): when_edge_to[%d] %q must be a literal edge target — an exact name, a prefix*, or a *suffix (no other pattern forms) — never a component name", loc, r.ID, j, target))
			}
		}
	case r.Guide != "":
		if r.Message == "" {
			problems = append(problems, fmt.Sprintf("%s (%s): guide needs a message — the advice is what a guidance rule delivers", loc, r.ID))
		}
		// An exemplar names prior art — one file or one fact somebody can open
		// and read — and its existence check runs through the same matcher a
		// component's match uses. That matcher now speaks a basename glob, and
		// an exemplar is its one caller that never passes through
		// validConstraintMatch, so the two would part company here: `**/x`
		// would resolve against whatever the glob reached rather than failing
		// closed as a name nothing carries. Refuse the prefix rather than teach
		// exemplars a pattern dialect they have no use for.
		for j, ex := range r.Exemplars {
			if ex == "" || strings.ContainsAny(ex, " \t") {
				problems = append(problems, fmt.Sprintf("%s (%s): exemplars[%d] %q must be a non-empty whitespace-free file path or fact name", loc, r.ID, j, ex))
				continue
			}
			if strings.HasPrefix(ex, BasenameGlobPrefix) {
				problems = append(problems, fmt.Sprintf("%s (%s): exemplars[%d] %q names prior art, not a pattern — give the file or fact a reader should open", loc, r.ID, j, ex))
			}
		}
		if r.Mode != "" && !AllowedGuidanceModes[r.Mode] {
			problems = append(problems, fmt.Sprintf("%s (%s): mode %q is not a guidance mode (allowed: %s) — guidance steers, never enforces; graduating it to law means writing a law form", loc, r.ID, r.Mode, allowedGuidanceModes()))
		}
	}
	if edgeForm && !AllowedRuleVias[r.Via] {
		problems = append(problems, fmt.Sprintf("%s (%s): via %q is not a rule edge kind (allowed: %s)", loc, r.ID, r.Via, allowedRuleVias()))
	}
	// Forbid_reach walks every rule-via edge kind by default, so its via is
	// optional — but a declared one must still come from the vocabulary.
	if r.ForbidReach != "" && r.Via != "" && !AllowedRuleVias[r.Via] {
		problems = append(problems, fmt.Sprintf("%s (%s): via %q is not a rule edge kind (allowed: %s)", loc, r.ID, r.Via, allowedRuleVias()))
	}
	if !edgeForm && r.ForbidReach == "" && r.RequireEdge == "" && r.Protocol == "" && len(r.WhenEdgeTo) == 0 && r.Via != "" {
		problems = append(problems, fmt.Sprintf("%s (%s): via belongs to the edge forms (forbid, forbid_reach, allow, require_edge, protocol) and to require's when_edge_to antecedent, not this one", loc, r.ID))
	}
	if r.Forbid == "" && r.ForbidReach == "" && r.RequireEdge == "" && r.To != "" {
		problems = append(problems, fmt.Sprintf("%s (%s): to belongs to the forbid, forbid_reach and require_edge forms", loc, r.ID))
	}
	if r.RequireEdge == "" && r.Direction != "" {
		problems = append(problems, fmt.Sprintf("%s (%s): direction belongs to the require_edge form", loc, r.ID))
	}
	if r.Protocol == "" && len(r.Steps) > 0 {
		problems = append(problems, fmt.Sprintf("%s (%s): steps belongs to the protocol form", loc, r.ID))
	}
	if r.Allow == "" && len(r.Only) > 0 {
		problems = append(problems, fmt.Sprintf("%s (%s): only belongs to the allow form", loc, r.ID))
	}
	if r.Protect == "" && len(r.Owners) > 0 {
		problems = append(problems, fmt.Sprintf("%s (%s): owners belongs to the protect form", loc, r.ID))
	}
	if r.Private == "" && len(r.Except) > 0 {
		problems = append(problems, fmt.Sprintf("%s (%s): except belongs to the private form", loc, r.ID))
	}
	if r.Cap == "" && r.MaxMembers != 0 {
		problems = append(problems, fmt.Sprintf("%s (%s): max_members belongs to the cap form", loc, r.ID))
	}
	if r.Require == "" && (r.WhenPropContains != nil || r.MustPropContain != nil) {
		problems = append(problems, fmt.Sprintf("%s (%s): when_prop_contains/must_prop_contain belong to the require form", loc, r.ID))
	}
	if len(r.ToName) > 0 {
		switch {
		case r.Forbid == "":
			problems = append(problems, fmt.Sprintf("%s (%s): to_name belongs to the forbid form — the other edge forms resolve their far end against a component's members, and naming a literal there has not been given a meaning", loc, r.ID))
		case r.To != "":
			problems = append(problems, fmt.Sprintf("%s (%s): to and to_name both name the far end; declare exactly one, because a component resolves against measured facts and a literal against the recorded edge target", loc, r.ID))
		}
		for j, target := range r.ToName {
			switch {
			case strings.IndexFunc(target, unicode.IsSpace) >= 0:
				problems = append(problems, fmt.Sprintf("%s (%s): to_name[%d] %q must carry no whitespace — the compiled rule holds the targets as one whitespace-separated set, and the split that reads it back is unicode.IsSpace's", loc, r.ID, j, target))
			case !ValidNamePattern(target):
				problems = append(problems, fmt.Sprintf("%s (%s): to_name[%d] %q must be a literal edge target — an exact name, a prefix*, or a *suffix (no other pattern forms) — never a component name", loc, r.ID, j, target))
			}
		}
	}
	if r.Require == "" && r.RequireEdge == "" && len(r.WhenEdgeTo) > 0 {
		problems = append(problems, fmt.Sprintf("%s (%s): when_edge_to belongs to the require and require_edge forms", loc, r.ID))
	}
	if r.WhenVia != "" {
		switch {
		case r.RequireEdge == "":
			problems = append(problems, fmt.Sprintf("%s (%s): when_via belongs to the require_edge form, whose via names the edge the rule demands — every other form reads its antecedent on via itself", loc, r.ID))
		case len(r.WhenEdgeTo) == 0:
			problems = append(problems, fmt.Sprintf("%s (%s): when_via names the edge kind the when_edge_to antecedent reads, and no antecedent is declared", loc, r.ID))
		case !AllowedRuleVias[r.WhenVia]:
			problems = append(problems, fmt.Sprintf("%s (%s): when_via %q is not a measured edge kind (allowed: %s)", loc, r.ID, r.WhenVia, allowedRuleVias()))
		}
	}
	if r.RequireEdge != "" && len(r.WhenEdgeTo) > 0 && r.WhenVia == "" {
		problems = append(problems, fmt.Sprintf("%s (%s): when_edge_to on the require_edge form needs a when_via (allowed: %s) — via already names the edge this rule demands, so the kind the antecedent reads is never defaulted from it", loc, r.ID, allowedRuleVias()))
	}
	if r.RequireDefines == "" && r.Method != "" {
		problems = append(problems, fmt.Sprintf("%s (%s): method belongs to the require_defines form", loc, r.ID))
	}
	if r.RequireName == "" && r.Pattern != "" {
		problems = append(problems, fmt.Sprintf("%s (%s): pattern belongs to the require_name form", loc, r.ID))
	}
	if r.Guide == "" && (r.Message != "" || len(r.Exemplars) > 0) {
		problems = append(problems, fmt.Sprintf("%s (%s): message/exemplars belong to the guide form", loc, r.ID))
	}
	if r.Guide == "" && r.Mode != "" && !AllowedRuleModes[r.Mode] {
		problems = append(problems, fmt.Sprintf("%s (%s): mode %q is not an enforcement mode (allowed: %s)", loc, r.ID, r.Mode, allowedRuleModes()))
	}
	if r.Because == "" {
		problems = append(problems, fmt.Sprintf("%s (%s): needs a because — a rule with no stated rationale cannot surface one in its findings", loc, r.ID))
	}
	return problems
}

func exemptionProblems(loc, ruleID string, exempt []ConstraintExemption) []string {
	var problems []string
	seenWitness := map[string]bool{}
	for j, ex := range exempt {
		entry := fmt.Sprintf("%s (%s): exempt[%d]", loc, ruleID, j)
		switch {
		case strings.TrimSpace(ex.Witness) == "":
			problems = append(problems, fmt.Sprintf("%s: missing witness — the exact violation identity the rule would otherwise report, matched exactly (no glob forms)", entry))
		case seenWitness[ex.Witness]:
			problems = append(problems, fmt.Sprintf("%s: witness %q is already exempted on this rule", entry, ex.Witness))
		}
		seenWitness[ex.Witness] = true
		if strings.TrimSpace(ex.Owner) == "" {
			problems = append(problems, fmt.Sprintf("%s: missing owner — an exemption is a decision someone signs", entry))
		}
		if strings.TrimSpace(ex.Because) == "" {
			problems = append(problems, fmt.Sprintf("%s: missing because — an exemption with no recorded reason is how a violation goes permanently silent", entry))
		}
		if _, err := time.Parse("2006-01-02", ex.Since); err != nil {
			problems = append(problems, fmt.Sprintf("%s: since %q must be YYYY-MM-DD", entry, ex.Since))
		}
	}
	return problems
}

// ValidNamePattern enforces the bounded name dialect: an exact name, a prefix
// followed by one trailing *, or one leading * followed by a suffix — never a
// general glob or regex, for the same reason match patterns are bounded: a
// convention the evaluator would silently mis-apply must be impossible to
// declare. The literal part must be non-empty and carry no pattern
// metacharacters.
//
// Three declaration sites speak it — require_name's pattern, which matches a
// member's own name; require's when_edge_to, which matches the literal far end
// of a member's outgoing edge; and a component's name_pattern, which narrows
// membership to a family of fact names — and MatchBoundedName below is the one
// matcher all three are evaluated with, so what a declaration may say and what
// the evaluator does with it cannot drift apart.
func ValidNamePattern(pattern string) bool {
	literal := pattern
	switch {
	case strings.HasPrefix(pattern, "*"):
		literal = pattern[1:]
	case strings.HasSuffix(pattern, "*"):
		literal = pattern[:len(pattern)-1]
	}
	if literal == "" {
		return false
	}
	return !strings.ContainsAny(literal, "*?[]{}")
}

// MatchBoundedName applies the dialect ValidNamePattern admits: one trailing *
// matches a prefix, one leading * matches a suffix, no * matches exactly.
// Plain string comparison, deliberately — the dialect was bounded at parse
// time exactly so no matching engine's semantics could leak in.
func MatchBoundedName(name, pattern string) bool {
	switch {
	case strings.HasPrefix(pattern, "*"):
		return strings.HasSuffix(name, pattern[1:])
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(name, pattern[:len(pattern)-1])
	default:
		return name == pattern
	}
}

// validConstraintMatch enforces the bounded glob dialect: an exact
// repo-relative path, a `prefix/**` subtree, or a `**/<name>` basename glob —
// nothing more. Any other glob metacharacter is rejected at parse time, so a
// selector the evaluator would silently fail to match is an error the
// declaration's author sees instead. Declared layers keep the first two forms
// and not the third (see layers' matchDeclaredLayerPath): a layer is a place,
// and a filename that appears in several places is not one.
func validConstraintMatch(pattern string) bool {
	if glob, ok := strings.CutPrefix(pattern, BasenameGlobPrefix); ok {
		return ValidBasenameGlob(glob)
	}
	prefix, _ := strings.CutSuffix(pattern, "/**")
	if prefix == "" {
		return false
	}
	return !strings.ContainsAny(prefix, "*?[]{}")
}

// BasenameGlobPrefix opens the one match form that is about a file's name
// rather than its place. It reads as "at any depth", and it is the only
// position `**` may take on the left: a `**` between segments would be a
// second axis of freedom the evaluator does not have.
const BasenameGlobPrefix = "**/"

// ValidBasenameGlob reports whether glob is one path segment carrying at most
// one `*` around a non-empty literal — `*_controller.js`, `Gemfile`,
// `schema.*`. The literal must survive the star's removal and carry no
// metacharacter of its own, which is what keeps the dialect small enough for
// the evaluator to implement exactly: no `?`, no character class, no brace
// set, no escape, and no second star whose backtracking the author would have
// to reason about. `**/*` is therefore malformed rather than a spelling of
// "everything", for the same reason `name_pattern: *` is.
//
// Shared with the evaluator rather than restated there, so what a declaration
// is allowed to say and what the matcher will do with it cannot drift apart.
func ValidBasenameGlob(glob string) bool {
	literal := strings.Replace(glob, "*", "", 1)
	if literal == "" || strings.Contains(literal, "/") {
		return false
	}
	return !strings.ContainsAny(literal, `*?[]{}\`)
}

// ancestorProblems validates the ancestry selector at declaration time: the
// name must read as a constant path, and it must not be spelled twice through
// the where clause, whose superclass pair is the one-level literal and not the
// same claim.
func ancestorProblems(loc string, c ConstraintComponent) []string {
	if c.Ancestor == "" {
		return nil
	}
	var problems []string
	if !validConstantPath(c.Ancestor) {
		problems = append(problems, fmt.Sprintf("%s (%s): ancestor %q must be a constant path such as ApplicationRecord or ViewComponent::Base", loc, c.Name, c.Ancestor))
	}
	if c.Kind != "" && c.Kind != "symbol" {
		problems = append(problems, fmt.Sprintf("%s (%s): ancestor selects classes, so kind must be symbol or absent, not %q", loc, c.Name, c.Kind))
	}
	return problems
}

func validConstantPath(name string) bool {
	for _, segment := range strings.Split(name, "::") {
		if segment == "" || segment[0] < 'A' || segment[0] > 'Z' {
			return false
		}
		for _, r := range segment {
			if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
				return false
			}
		}
	}
	return true
}
