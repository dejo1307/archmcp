package dotnetextractor

// Razor — Blazor components (`.razor`) and MVC / Razor Pages views (`.cshtml`).
//
// WHY THIS IS NOT A PARSER. A Razor file interleaves HTML, C# and a transition
// syntax with no standalone grammar; the real one lives in the Razor compiler and
// generates a C# class. What this file does instead is find the regions where C#
// appears and harvest the NAMES referenced there. That is deliberately less than
// a parse and is enough for the job: the reference edges are what stop a member
// whose only caller is markup from reading as dead code.
//
// The measured problem, on the benchmark corpus before this existed:
// MudBlazor reported 5,749 orphans out of 9,287 symbols — 62% of a maintained
// component library — because `MudAlert.razor.cs` declares `OnClickHandler` and
// only `MudAlert.razor` calls it. OrchardCore's view models went the same way.
//
// The `.razor` component and its `.razor.cs` code-behind converge WITHOUT any
// special handling: both name the same directory-anchored symbol and both carry
// `partial`, so mergePartialTypes unifies them exactly as it does two halves of a
// hand-written partial class. That is why the component fact below is marked
// partial even when no code-behind exists.

import (
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

func isRazorComponent(relFile string) bool {
	return strings.EqualFold(filepath.Ext(relFile), ".razor")
}

func isRazorView(relFile string) bool {
	return strings.EqualFold(filepath.Ext(relFile), ".cshtml")
}

func isRazorFile(relFile string) bool {
	return isRazorComponent(relFile) || isRazorView(relFile)
}

// isDirectivesOnlyRazor reports whether a base name is one of the well-known
// files that carry directives for a whole directory rather than declaring a
// component or a view of their own.
func isDirectivesOnlyRazor(base string) bool {
	switch base {
	case "_Imports", "_ViewImports", "_ViewStart":
		return true
	}
	return false
}

// razorDoc is what one Razor file declares and references.
type razorDoc struct {
	namespace  string
	inherits   string
	implements []string
	injects    []string // injected TYPES; the local name is not a graph node
	model      string   // @model, the .cshtml view-model type
	routes     []string // @page templates
	layout     string
	components []string // component tags instantiated in the markup
	refs       []string // every other name referenced from a C# region
	codeBody   string   // the concatenated @code / @functions bodies
	codeLine   int      // 1-based line where the first code body starts
}

// ── Region blanking ─────────────────────────────────────────────────────────

var (
	razorComment = regexp.MustCompile(`(?s)@\*.*?\*@`)
	htmlComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// blankOut replaces every match with same-length whitespace, preserving newlines.
// Byte offsets and line numbers must survive comment removal because the code
// block's line number is reported into the fact set.
func blankOut(re *regexp.Regexp, s string) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		b := []byte(m)
		for i := range b {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
		return string(b)
	})
}

// matchBrace returns the index just past the '}' closing the '{' at open, or -1.
// Strings and char literals are skipped so a brace inside them does not close the
// block — `@code { var s = "}"; }` is real C#.
func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '"', '\'':
			q := s[i]
			i++
			for i < len(s) && s[i] != q {
				if s[i] == '\\' {
					i++
				}
				i++
			}
		}
	}
	return -1
}

// matchParen is matchBrace for '(' / ')'.
func matchParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '"', '\'':
			q := s[i]
			i++
			for i < len(s) && s[i] != q {
				if s[i] == '\\' {
					i++
				}
				i++
			}
		}
	}
	return -1
}

func lineAt(s string, off int) int { return strings.Count(s[:off], "\n") + 1 }

// ── Directives ──────────────────────────────────────────────────────────────

var directiveLine = regexp.MustCompile(`(?m)^[ \t]*@(page|model|namespace|inherits|implements|inject|using|attribute|layout|typeparam|addTagHelper|removeTagHelper|preservewhitespace|rendermode)\b[ \t]*(.*)$`)

// quoted extracts the first double-quoted literal, e.g. @page "/counter/{id:int}".
var quoted = regexp.MustCompile(`"([^"]*)"`)

// ── Markup references ───────────────────────────────────────────────────────

var (
	// A component tag is PascalCase; HTML elements are lowercase by convention and
	// the Razor compiler uses exactly this rule to tell them apart. A namespaced
	// tag (<MudBlazor.MudChip>) keeps only its last segment, which is the type.
	componentTag = regexp.MustCompile(`</?([A-Z][A-Za-z0-9_]*(?:\.[A-Z][A-Za-z0-9_]*)*)[\s/>]`)

	// ASP.NET tag helpers bind to model members by NAME, with no @ transition at
	// all: `asp-for="DisplayMenuFilter"`. Every one of OrchardCore's view-model
	// false positives was reached this way, so a scanner that follows only @
	// transitions finds none of them.
	tagHelperAttr = regexp.MustCompile(`\basp-(?:for|validation-for|validation-class-for|items)\s*=\s*"([^"]*)"`)

	// A directive attribute carries a C# expression in its VALUE
	// (@onclick="Handler", @bind-Value="Model.Prop"). The attribute name itself is
	// Razor syntax, not a reference, so it must not be harvested as one.
	directiveAttr = regexp.MustCompile(`@[a-zA-Z][-a-zA-Z0-9_:]*\s*=\s*"([^"]*)"`)

	identifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// csharpNoise are tokens that appear in C# regions but are never a repository
// symbol. Keeping them would be mostly harmless — bindMemberCall drops any name
// the repository does not declare — but `string`, `Count` and `Value` ARE declared
// somewhere in a corpus this size, and a spurious reference SUPPRESSES a genuine
// dead-code finding. That is the direction worth guarding.
var csharpNoise = map[string]bool{
	"if": true, "else": true, "for": true, "foreach": true, "while": true, "do": true,
	"switch": true, "case": true, "default": true, "break": true, "continue": true,
	"return": true, "try": true, "catch": true, "finally": true, "throw": true,
	"new": true, "var": true, "await": true, "async": true, "using": true, "in": true,
	"is": true, "as": true, "null": true, "true": true, "false": true, "this": true,
	"base": true, "typeof": true, "nameof": true, "sizeof": true, "checked": true,
	"lock": true, "ref": true, "out": true, "params": true, "when": true, "where": true,
	"select": true, "from": true, "let": true, "orderby": true, "group": true, "by": true,
	"into": true, "join": true, "on": true, "equals": true, "yield": true, "get": true,
	"set": true, "value": true, "and": true, "or": true, "not": true, "with": true,
	// Primitive and near-primitive type names.
	"string": true, "int": true, "long": true, "short": true, "byte": true, "bool": true,
	"double": true, "float": true, "decimal": true, "char": true, "object": true,
	"void": true, "dynamic": true, "String": true, "Int32": true, "Boolean": true,
	"Object": true, "Task": true, "Nullable": true,
	// Razor/Blazor built-ins that are syntax rather than repository symbols.
	"context": true, "code": true, "functions": true, "text": true,
}

// stripLiterals blanks the contents of string and char literals, so prose does
// not become references. `@T["Enable Admin Menu filter"]` otherwise contributed
// `Enable`, `Admin`, `Menu` and `filter`, and a fabricated reference is worse than
// a missing one: it vouches for a symbol nothing uses and suppresses a genuine
// dead-code finding.
//
// INTERPOLATION HOLES SURVIVE. `$"{Section.ToStringFast(true)}/{Previous.Link}"`
// is a string whose braces hold real code, and it is how Blazor builds most of its
// hrefs — blanking it wholesale would lose exactly the references worth having.
func stripLiterals(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		q := b[i]
		if q != '"' && q != '\'' {
			continue
		}
		i++
		for i < len(b) && b[i] != q {
			switch b[i] {
			case '\\':
				b[i] = ' '
				if i+1 < len(b) && b[i+1] != '\n' {
					i++
					b[i] = ' '
				}
			case '{':
				// Skip the hole, leaving its code intact.
				for i < len(b) && b[i] != '}' && b[i] != q {
					i++
				}
				if i < len(b) && b[i] == '}' {
					i++
				}
				i--
			default:
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
			i++
		}
	}
	return string(b)
}

// harvest appends every plausible symbol name in a C# region.
func harvest(dst *[]string, region string) {
	for _, id := range identifier.FindAllString(stripLiterals(region), -1) {
		if csharpNoise[id] || len(id) < 2 {
			continue
		}
		*dst = append(*dst, id)
	}
}

// shortType reduces a qualified type name to its last segment. Facts are named
// after their DIRECTORY, and a bare type reference resolves through the C#
// extractor's project-wide index, so `@model A.B.CViewModel` must target
// `CViewModel` — the same reduction componentTag applies to `<Ns.Component />`.
func shortType(t string) string {
	if i := strings.LastIndex(t, "."); i >= 0 {
		return t[i+1:]
	}
	return t
}

// scanRazor walks a Razor file and returns what it declares and references.
func scanRazor(src, relFile string) *razorDoc {
	d := &razorDoc{}
	s := blankOut(htmlComment, blankOut(razorComment, src))

	// 1. Code blocks first, so their bodies are not also scanned as markup. Both
	//    spellings occur, with or without a space before the brace.
	for _, kw := range []string{"@code", "@functions"} {
		for i := 0; ; {
			j := strings.Index(s[i:], kw)
			if j < 0 {
				break
			}
			at := i + j
			k := at + len(kw)
			for k < len(s) && (s[k] == ' ' || s[k] == '\t' || s[k] == '\r' || s[k] == '\n') {
				k++
			}
			if k >= len(s) || s[k] != '{' {
				i = at + len(kw)
				continue
			}
			end := matchBrace(s, k)
			if end < 0 {
				break
			}
			body := s[k+1 : end-1]
			if d.codeBody == "" {
				d.codeLine = lineAt(s, k+1)
				d.codeBody = body
			} else {
				d.codeBody += "\n" + body
			}
			s = s[:at] + strings.Repeat(" ", end-at) + s[end:]
			i = end
		}
	}

	// 2. Directives.
	for _, m := range directiveLine.FindAllStringSubmatch(s, -1) {
		name, rest := m[1], strings.TrimSpace(m[2])
		switch name {
		case "page":
			if q := quoted.FindStringSubmatch(rest); q != nil && q[1] != "" {
				d.routes = append(d.routes, q[1])
			}
		case "model":
			d.model = firstType(rest)
		case "namespace":
			d.namespace = rest
		case "inherits":
			d.inherits = firstType(rest)
		case "implements":
			d.implements = append(d.implements, firstType(rest))
		case "layout":
			d.layout = firstType(rest)
		case "inject":
			// `@inject IFoo Bar` — the TYPE is the graph node, the local name is not.
			if t := firstType(rest); t != "" {
				d.injects = append(d.injects, t)
			}
		}
	}
	s = directiveLine.ReplaceAllString(s, "")

	// 3. Component tags and tag-helper attributes.
	for _, m := range componentTag.FindAllStringSubmatch(s, -1) {
		name := m[1]
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		d.components = append(d.components, name)
	}
	for _, m := range tagHelperAttr.FindAllStringSubmatch(s, -1) {
		harvest(&d.refs, m[1])
	}

	// 4. Directive-attribute values, harvested and then blanked so step 5 does not
	//    also read the attribute NAME as an expression.
	for _, m := range directiveAttr.FindAllStringSubmatch(s, -1) {
		harvest(&d.refs, m[1])
	}
	s = directiveAttr.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Repeat(" ", len(m))
	})

	// 5. Remaining @ transitions.
	harvestTransitions(d, s)

	d.components = dedupeSorted(d.components)
	d.refs = dedupeSorted(d.refs)
	d.injects = dedupeSorted(d.injects)
	return d
}

// harvestTransitions walks the `@` transitions left in the markup: explicit
// expressions `@(...)`, statement blocks `@{...}`, control flow `@if (...)`, and
// implicit expressions `@Member.Chain(args)`.
func harvestTransitions(d *razorDoc, s string) {
	for i := 0; i < len(s); i++ {
		if s[i] != '@' {
			continue
		}
		// `@@` is an escaped literal at-sign, not a transition.
		if i+1 < len(s) && s[i+1] == '@' {
			i++
			continue
		}
		j := i + 1
		if j >= len(s) {
			break
		}
		switch {
		case s[j] == '(':
			if end := matchParen(s, j); end > 0 {
				harvest(&d.refs, s[j:end])
				i = end - 1
			}
		case s[j] == '{':
			if end := matchBrace(s, j); end > 0 {
				harvest(&d.refs, s[j:end])
				i = end - 1
			}
		case isIdentStart(s[j]):
			k := j
			for k < len(s) && isIdentPart(s[k]) {
				k++
			}
			word := s[j:k]
			// A control-flow keyword takes its parenthesised head and its block.
			if word == "if" || word == "foreach" || word == "for" || word == "while" ||
				word == "switch" || word == "using" || word == "lock" || word == "do" {
				for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
					k++
				}
				if k < len(s) && s[k] == '(' {
					if end := matchParen(s, k); end > 0 {
						harvest(&d.refs, s[k:end])
						i = end - 1
						continue
					}
				}
				i = k - 1
				continue
			}
			// An implicit expression: the identifier plus its member chain, index
			// and call arguments.
			end := k
			for end < len(s) {
				switch s[end] {
				case '.':
					n := end + 1
					for n < len(s) && isIdentPart(s[n]) {
						n++
					}
					if n == end+1 {
						goto done
					}
					end = n
				case '(':
					n := matchParen(s, end)
					if n < 0 {
						goto done
					}
					end = n
				case '[':
					n := strings.IndexByte(s[end:], ']')
					if n < 0 {
						goto done
					}
					end += n + 1
				default:
					goto done
				}
			}
		done:
			harvest(&d.refs, s[j:end])
			i = end - 1
		}
	}
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// firstType returns the first type-ish token of a directive argument, dropping
// generic arguments and the local name in `@inject IFoo Bar`.
func firstType(rest string) string {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	if i := strings.IndexAny(rest, " \t"); i > 0 {
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '<'); i > 0 {
		rest = rest[:i]
	}
	return strings.TrimSuffix(strings.TrimSpace(rest), ";")
}

// ── Fact emission ───────────────────────────────────────────────────────────

// razorFacts turns one Razor file into facts.
//
// A `.razor` file IS a class — the Razor compiler generates one named after the
// file — so it emits a component symbol carrying the markup's reference edges,
// marked `partial` so it merges with any `.razor.cs` code-behind.
//
// A `.cshtml` view is not: MVC and Razor Pages generate a class nothing
// references by name. It therefore emits a KindFileRef, the same reference-only
// vehicle the Java and TypeScript extractors use, carrying solely `calls`. That
// keeps a view from ever becoming a dead-code candidate itself while still
// vouching for the view-model members it binds.
func razorFacts(src, relFile string) []facts.Fact {
	d := scanRazor(src, relFile)
	rel := filepath.ToSlash(relFile)
	dir := path.Dir(rel)
	base := strings.TrimSuffix(path.Base(rel), path.Ext(rel))

	var out []facts.Fact
	if isDirectivesOnlyRazor(base) {
		// _Imports.razor / _ViewImports.cshtml / _ViewStart.cshtml carry directives
		// that apply to a whole directory. The Razor compiler generates no component
		// from them, so emitting a symbol invents a type nothing can reference — 977
		// of MudBlazor's post-change orphans were files like these.
		return nil
	}
	if isRazorComponent(relFile) {
		out = append(out, razorComponentFact(d, rel, dir, base))
		out = append(out, razorCodeFacts(d, rel, base)...)
	} else {
		out = append(out, razorViewRef(d, rel))
	}
	out = append(out, razorRoutes(d, rel, dir, base)...)
	return out
}

func razorComponentFact(d *razorDoc, rel, dir, base string) facts.Fact {
	props := map[string]any{
		"language": "razor",
		// A component IS a class — the Razor compiler generates one — and it must say
		// so, because symbol_kind is what puts a name into the type index that
		// resolves bare type references. Calling it "component" instead cost a real
		// edge: MudBlazor's .razor half merged over its .razor.cs half, took the
		// unrecognised kind with it, and `DialogService.ShowCoreAsync --calls-->
		// MudDialogContainer` was dropped, turning live code into apparent dead code.
		// The component-ness travels as razor_component + framework instead.
		"symbol_kind":     facts.SymbolClass,
		"razor_component": true,
		"framework":       "blazor",
		"exported":        true,
		// Marked partial with no code-behind present as well: a component whose
		// members live in an @code block is still one half of the same generated
		// class, and the merge must not depend on whether a .razor.cs exists.
		"partial": true,
	}
	if d.namespace != "" {
		props["namespace"] = d.namespace
		props["fqn"] = d.namespace + "." + base
	}
	rels := []facts.Relation{{Kind: facts.RelDeclares, Target: dir}}
	if d.inherits != "" {
		rels = append(rels, facts.Relation{Kind: facts.RelImplements, Target: shortType(d.inherits)})
	}
	for _, i := range d.implements {
		rels = append(rels, facts.Relation{Kind: facts.RelImplements, Target: shortType(i)})
	}
	for _, i := range d.injects {
		rels = append(rels, facts.Relation{Kind: facts.RelInjects, Target: shortType(i)})
	}
	for _, c := range d.components {
		if c != base {
			rels = append(rels, facts.Relation{Kind: facts.RelInstantiates, Target: c})
		}
	}
	if d.layout != "" {
		rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: d.layout})
	}
	for _, r := range d.refs {
		rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: r})
	}
	return facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      dir + "." + base,
		File:      rel,
		Line:      1,
		Props:     props,
		Relations: rels,
	}
}

// razorViewRef is the reference-only fact for a `.cshtml` view.
func razorViewRef(d *razorDoc, rel string) facts.Fact {
	targets := make([]string, 0, len(d.refs)+len(d.components)+1)
	targets = append(targets, d.refs...)
	targets = append(targets, d.components...)
	if d.model != "" {
		targets = append(targets, shortType(d.model))
	}
	if d.inherits != "" {
		targets = append(targets, shortType(d.inherits))
	}
	for _, i := range d.injects {
		targets = append(targets, shortType(i))
	}

	rels := make([]facts.Relation, 0, len(targets))
	for _, t := range dedupeSorted(targets) {
		rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: t})
	}
	return facts.Fact{
		Kind:      facts.KindFileRef,
		Name:      rel,
		File:      rel,
		Props:     map[string]any{"language": "razor", "framework": "aspnetcore"},
		Relations: rels,
	}
}

// razorCodeFacts parses an @code / @functions body by wrapping it in a synthetic
// compilation unit and handing it to the ordinary C# walker.
//
// The wrapper occupies exactly ONE line and is followed by enough blank lines to
// put the body back on the line it occupies in the .razor file, so every symbol's
// reported line is the real one rather than an offset into a synthetic buffer.
func razorCodeFacts(d *razorDoc, rel, base string) []facts.Fact {
	if strings.TrimSpace(d.codeBody) == "" {
		return nil
	}
	header := "public partial class " + base + " {"
	if d.namespace != "" {
		header = "namespace " + d.namespace + "; " + header
	}
	// After the one-line header plus N newlines the cursor sits at the start of
	// line N+1, and the body's first character belongs to line codeLine — so N is
	// codeLine-1, not codeLine-2.
	pad := d.codeLine - 1
	if pad < 0 {
		pad = 0
	}
	synth := header + strings.Repeat("\n", pad) + d.codeBody + "\n}"
	return extractFileAST([]byte(synth), rel)
}

// razorRoutes emits the page routes a Razor file declares.
//
// Typed as a UI route (`type=page`), not a server route. A Blazor or Razor Pages
// URL is one the BROWSER navigates to, not an HTTP contract served to other
// services — indexing it as a server route would make every page a cross-repo
// match candidate and, worse, an "unused route no client calls" finding. The
// linker already excludes this type for exactly that reason.
func razorRoutes(d *razorDoc, rel, dir, base string) []facts.Fact {
	var out []facts.Fact
	for _, tmpl := range d.routes {
		p := tmpl
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		props := map[string]any{
			"method":    "GET",
			"type":      "page",
			"language":  "razor",
			"framework": "blazor",
		}
		if isRazorView(rel) {
			props["framework"] = "razorpages"
		}
		rels := []facts.Relation{{Kind: facts.RelDeclares, Target: dir}}
		if isRazorComponent(rel) {
			rels = append(rels, facts.Relation{Kind: facts.RelHandledBy, Target: dir + "." + base})
		}
		out = append(out, facts.Fact{
			Kind:      facts.KindRoute,
			Name:      p,
			File:      rel,
			Line:      1,
			Props:     props,
			Relations: rels,
		})
	}
	return out
}

func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
