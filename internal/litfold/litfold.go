// Package litfold is the one definition of bounded literal derivation: the
// rules by which an extractor may resolve a name or expression to a string
// literal the source states, without evaluating anything. Three forms and only
// three — a file-local single-assignment constant, a literal argument carried
// by a wrapper call, and a template literal whose interpolated base precedes a
// literal path tail. Consumers scan their own language's syntax and hand the
// candidates here; widening the set is an ADR amendment, not a local edit.
package litfold

import "regexp"

// Assignments is a file-local single-assignment store. A name recorded twice
// resolves to nothing regardless of the values — reassignment tracking is
// evaluation, and evaluation is out.
type Assignments struct {
	value map[string]string
	dup   map[string]bool
}

// NewAssignments creates an empty store.
func NewAssignments() *Assignments {
	return &Assignments{value: map[string]string{}, dup: map[string]bool{}}
}

// Add records one assignment to a name. An empty literal records a
// NON-literal assignment: it counts toward the single-assignment rule (so a
// second assignment kills the name) but resolves to nothing itself. A nil
// store ignores the call — the nil-map ergonomics consumers had before this
// type existed (a template scanner with no source file passes nil).
func (a *Assignments) Add(name, literal string) {
	if a == nil || a.dup[name] {
		return
	}
	if _, seen := a.value[name]; seen {
		delete(a.value, name)
		a.dup[name] = true
		return
	}
	a.value[name] = literal
}

// Resolve returns the literal a name was assigned exactly once, and whether
// the derivation holds. A nil store resolves nothing.
func (a *Assignments) Resolve(name string) (string, bool) {
	if a == nil {
		return "", false
	}
	v, ok := a.value[name]
	return v, ok && v != ""
}

// wrapperCall matches a call expression carrying exactly one string-literal
// argument: an identifier (optionally predicate/bang-suffixed), an opening
// parenthesis, one quoted literal, a closing parenthesis.
var wrapperCall = regexp.MustCompile(`^[A-Za-z_][\w.]*[!?]?\(\s*(?:"([^"]*)"|'([^']*)')\s*\)$`)

// WrapperLiteralPath derives the path literal from a wrapper-call expression
// in request position — `build_url("/pageview")` yields "/pageview". The
// wrapped literal must read as a request path (a leading slash); any other
// literal derives nothing, so a name-lookup wrapper can never fabricate a
// route.
func WrapperLiteralPath(expr string) (string, bool) {
	m := wrapperCall.FindStringSubmatch(expr)
	if m == nil {
		return "", false
	}
	lit := m[1]
	if lit == "" {
		lit = m[2]
	}
	if len(lit) < 2 || lit[0] != '/' {
		return "", false
	}
	return lit, true
}

// templateTail matches a template body whose head is a single interpolation
// and whose tail is a "/"-rooted literal — `${config.HOST}/mcp`.
var templateTail = regexp.MustCompile(`^\$\{[^}]+\}/`)

// TemplateTailPath reports whether a template-literal body is an interpolated
// base followed by a literal path tail — the one interpolation-headed shape an
// HTTP-client scanner may admit. The base is never resolved to a host here;
// the consumer's path normalization keeps the tail and hints the target.
func TemplateTailPath(body string) bool {
	return templateTail.MatchString(body)
}
