package intent

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A where predicate selects component members by what the measured facts
// CARRY rather than by where they sit: `where: {superclass: ViewComponent::Base}`
// names every Rails view component in the repository, including the ones
// nested inside a file whose path says nothing about them. It exists because
// the props the extractors already measure — framework, superclass,
// symbol_kind, decorators, storage_kind, the numeric complexity counts —
// carve the graph along the lines rules actually want to speak about, and no
// path dialect can reach them.
//
// The predicate is a conjunction and nothing else: every pair must hold, and
// there is no or and no negation in this version. A disjunction is two
// components, which reads better than a nested boolean in YAML; a negation
// asks the snapshot to answer for facts it may simply not have measured,
// which is the opposite of failing closed.
//
// One value is not a value: WhereAnyValue asks whether the fact carries the
// property AT ALL, whatever it says. That is the positive half of the thing a
// negation would ask, and it does not inherit the negation's problem — "this
// fact carries fields_written" is a claim about something measured, where "this
// fact does not" cannot be told from "nothing measured writes here". It exists
// because some conventions are about a property's presence rather than its
// content: a function derived from tracked state must not write ANY field, and
// naming the fields it may not write would be a different and much weaker rule.

// WhereAnyValue is the reserved VALUE that asks for the property's presence
// rather than its content. It is spelled with angle brackets because no
// measured prop value carries them — a token that could collide with a real
// value would silently turn an ordinary comparison into an existence test, and
// the whole point of the bounded dialects in this package is that a
// declaration cannot mean something other than it says.
const WhereAnyValue = "<any>"

// WhereReservedKind is the one key inside a where predicate that does not name
// a fact property: it narrows the fact KIND, the same narrowing the
// component's own kind field carries. Reserving it lets a predicate say "the
// storage facts whose storage_kind is model" in one vocabulary; declaring both
// spellings at once is an error rather than a silent precedence rule.
const WhereReservedKind = "kind"

// whereReservedKeys are the keys a predicate may carry that are not property
// names. Everything else in a where clause is a fact property.
var whereReservedKeys = map[string]bool{
	WhereReservedKind: true,
}

// WherePair is one decoded property test — the shape the evaluator walks,
// ordered by prop so a membership resolves the same way on every run.
type WherePair struct {
	Prop  string
	Value string
	// Unsatisfiable marks a field of the compiled where prop that did not
	// decode into a property test. It is carried rather than dropped, and
	// matches no fact: a predicate that silently lost one of its tests would
	// select MORE than the declaration said, which is the one direction this
	// vocabulary must never fail in.
	Unsatisfiable bool
}

// whereComparators are the numeric thresholds a where value may open with,
// longest first so >= is never read as > followed by a stray =. The grammar is
// deliberately tiny: one comparator, one number, nothing else. A value opening
// with < or > that does not parse is a named error, never a value compared as
// a literal string — a threshold the evaluator silently misread would be the
// exact silent no-op this vocabulary exists to prevent.
var whereComparators = []string{">=", "<=", ">", "<"}

// FactKind resolves the component's fact-kind narrowing from either spelling —
// the component's own kind field or the predicate's reserved kind key. The two
// are never both set: validation rejects that.
func (c ConstraintComponent) FactKind() string {
	if c.Kind != "" {
		return c.Kind
	}
	if v, found := whereValueString(c.Where[WhereReservedKind]); found {
		return v
	}
	return ""
}

// Predicate is the property half of the where clause: every pair except the
// reserved keys, stringified. A value the scalar conversion cannot render is
// dropped here and reported as a problem by validation, so a declaration that
// fails to validate never compiles into a predicate that quietly means
// something narrower than it says.
func (c ConstraintComponent) Predicate() map[string]string {
	if len(c.Where) == 0 {
		return nil
	}
	out := make(map[string]string, len(c.Where))
	for k, v := range c.Where {
		if whereReservedKeys[k] {
			continue
		}
		if s, found := whereValueString(v); found {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EncodeWhere renders a predicate as the compiled fact's where prop: pairs
// sorted by property name and joined with single spaces, so the fact — and
// every fingerprint downstream of it — is a function of the declared SET rather
// than of the YAML order the author happened to write. Property names carry no
// `=` (validation enforces the character set), so the first `=` of each pair is
// unambiguously the separator and a value may contain as many more as it likes.
//
// Values are percent-escaped over whitespace and over `%` itself, which is what
// makes the round trip lossless. Without it a value carrying a space — or any
// of the runes unicode calls space, which an ASCII screen does not see — split
// into two fields on the way back and the predicate decoded to something the
// declaration never said. Widening a predicate is how a rule comes to judge
// code its declaration excluded, so the encoding, not only the validator, has
// to make it impossible.
func EncodeWhere(predicate map[string]string) string {
	if len(predicate) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(predicate))
	for prop, value := range predicate {
		pairs = append(pairs, escapeWhereField(prop)+"="+escapeWhereField(value))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, " ")
}

// DecodeWhere reads the compiled where prop back into the ordered pairs the
// evaluator tests. Nothing is ever dropped. A field that does not decode into a
// property test becomes an unsatisfiable pair, and a non-empty prop that yields
// no pair at all becomes one — because the alternative reading of a corrupt
// predicate is "no predicate", and a component whose predicate vanished selects
// the whole tree its match patterns cover.
func DecodeWhere(encoded string) []WherePair {
	if encoded == "" {
		return nil
	}
	var out []WherePair
	for _, field := range strings.Split(encoded, " ") {
		if field == "" {
			continue
		}
		prop, value, found := strings.Cut(field, "=")
		prop, value = unescapeWhereField(prop), unescapeWhereField(value)
		if !found || prop == "" || value == "" {
			out = append(out, WherePair{Value: field, Unsatisfiable: true})
			continue
		}
		out = append(out, WherePair{Prop: prop, Value: value})
	}
	if len(out) == 0 {
		out = append(out, WherePair{Value: encoded, Unsatisfiable: true})
	}
	return out
}

// escapeWhereField percent-encodes the bytes of every whitespace rune, and the
// escape character itself, so a field of the compiled prop is exactly one pair.
func escapeWhereField(s string) string {
	if strings.IndexFunc(s, whereNeedsEscape) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		if !whereNeedsEscape(r) {
			b.WriteRune(r)
			continue
		}
		var buf [utf8.UTFMax]byte
		for _, encoded := range buf[:utf8.EncodeRune(buf[:], r)] {
			fmt.Fprintf(&b, "%%%02X", encoded)
		}
	}
	return b.String()
}

func whereNeedsEscape(r rune) bool { return r == '%' || unicode.IsSpace(r) }

// unescapeWhereField reverses escapeWhereField. A `%` not followed by two hex
// digits is a literal `%`: it cannot have come from the encoder, and the
// round-trip property this function guards is the one that matters.
func unescapeWhereField(s string) string {
	if !strings.ContainsRune(s, '%') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if decoded, ok := hexByte(s[i+1], s[i+2]); ok {
				b.WriteByte(decoded)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func hexByte(hi, lo byte) (byte, bool) {
	h, hok := hexDigit(hi)
	l, lok := hexDigit(lo)
	return h<<4 | l, hok && lok
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// ParseThreshold reads a numeric comparison out of a where value: the operator
// and the number it compares against. It is exported because the validator and
// the evaluator must agree on the grammar down to the character — a where that
// parsed at declaration time and did not at verdict time would be a rule that
// enforces nothing while reading as law.
func ParseThreshold(value string) (op string, n float64, ok bool) {
	for _, comparator := range whereComparators {
		rest, cut := strings.CutPrefix(value, comparator)
		if !cut {
			continue
		}
		parsed, numeric := thresholdNumber(rest)
		if !numeric {
			return comparator, 0, false
		}
		return comparator, parsed, true
	}
	return "", 0, false
}

// thresholdNumber is the whole numeric grammar a threshold may use: an optional
// minus, at least one decimal digit, and at most one fractional part.
//
// strconv.ParseFloat accepts a great deal more, and every extra form is a way
// for a declaration to mean something no reader of the YAML would guess.
// "<=Inf" parsed as positive infinity, so it validated clean and selected every
// numeric fact while emitting no finding at any confidence — the exact silent
// widening this vocabulary exists to prevent. ">=1_0" means ten and ">=0x1fp0"
// means thirty-one. A threshold in a constraints file is a count written the
// way a count is written, and nothing else parses.
func thresholdNumber(s string) (float64, bool) {
	digits, fraction, fractional := strings.Cut(strings.TrimPrefix(s, "-"), ".")
	if !decimalDigits(digits) {
		return 0, false
	}
	if fractional && !decimalDigits(fraction) {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(s, 64)
	return parsed, err == nil
}

func decimalDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// SatisfiesThreshold decides one parsed comparison. Sharing it with the
// evaluator is the same argument ParseThreshold's export is: one definition of
// what >= means.
func SatisfiesThreshold(op string, measured, declared float64) bool {
	switch op {
	case ">=":
		return measured >= declared
	case "<=":
		return measured <= declared
	case ">":
		return measured > declared
	case "<":
		return measured < declared
	}
	return false
}

// comparatorOpeners are the characters a value trying to be a comparison can
// start with. `=` and `!` are in the set although no comparator begins with
// them: `=>30` is the hash-rocket transposition a Ruby shop types, and read as
// a literal it is a token nothing in the snapshot will ever equal — a rule that
// enforces nothing while reading as law, which is the exact silence this
// vocabulary exists to prevent.
const comparatorOpeners = "<>=!"

// A comparator that has been through a rendered document comes back as a rune
// that is not one: `≥`, `＞`, `»`, `≫`, `⩾`, `﹥`, `⇒`, `❯`. Each arrives by
// exactly the route `=>30` does, and read as a literal each is a token nothing
// measured will ever equal — a rule that enforces nothing while reading as law.
//
// Enumerating them was the first attempt and it is the wrong shape: the set of
// runes that LOOK like a relation has no edge, and every name left off the list
// compiled to a literal and selected nothing with only a 0.4 advisory to show
// for it. This is the allowlist instead. The threshold grammar is ASCII, and a
// property value is a name or a number, so a value may open with any ASCII rune
// (the four comparators are then parsed, everything else is a literal) or with
// any non-ASCII LETTER, DIGIT or combining MARK — the alphabets a real
// identifier is written in. A non-ASCII symbol or punctuation rune opens
// nothing this grammar can mean, and is a named error whether or not anyone
// thought of it in advance.
func openingRuneAllowed(r rune) bool {
	if r < utf8.RuneSelf {
		return true
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// opensThreshold reports whether a value is trying to be a comparison — the
// test that turns a malformed comparator into a named error instead of a
// literal string nothing will ever equal.
func opensThreshold(value string) bool {
	first, _ := utf8.DecodeRuneInString(value)
	return value != "" && strings.ContainsRune(comparatorOpeners, first)
}

// refusedOpeningRune returns the non-ASCII symbol or punctuation rune a value
// opens with, or 0.
func refusedOpeningRune(value string) rune {
	first, size := utf8.DecodeRuneInString(value)
	if value == "" || size == 0 || openingRuneAllowed(first) {
		return 0
	}
	return first
}

// whereValueString renders a YAML scalar as the string the predicate compares
// with. Ints and floats are rendered in the same canonical form the evaluator
// renders a measured numeric prop in, so `cyclomatic: 10` and a measured 10
// meet as the same token; a list or a map is not a scalar and is refused, so a
// nested structure cannot compile into a predicate that means less than it
// looks like.
func whereValueString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	}
	return "", false
}

// whereProblems validates one component's predicate: the reserved kind key
// against the same closed fact-kind vocabulary the component field takes, every
// other key as a property name, and every value as a whitespace-free scalar or
// a well-formed threshold.
//
// The whitespace screen reads the same alphabet the decoder splits on. An ASCII
// screen against a unicode split is how `superclass: "Base AND_THIS"` pasted
// out of a rendered document validated clean and selected on `Base` alone.
func whereProblems(loc string, c ConstraintComponent) []string {
	if c.Where == nil {
		return nil
	}
	var problems []string
	if len(c.Where) == 0 {
		problems = append(problems, fmt.Sprintf("%s (%s): where needs at least one property pair — an empty predicate narrows nothing", loc, c.Name))
		return problems
	}
	keys := make([]string, 0, len(c.Where))
	for k := range c.Where {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		raw := c.Where[key]
		if !validPropName(key) {
			problems = append(problems, fmt.Sprintf("%s (%s): where key %q must be a fact property name (lowercase letters, digits and underscores)", loc, c.Name, key))
			continue
		}
		value, scalar := whereValueString(raw)
		if !scalar {
			problems = append(problems, fmt.Sprintf("%s (%s): where %s must be a scalar value — a list or a map is not a property test", loc, c.Name, key))
			continue
		}
		if key == WhereReservedKind {
			if c.Kind != "" {
				problems = append(problems, fmt.Sprintf("%s (%s): kind is declared twice — as the component's kind field and inside where; keep one", loc, c.Name))
			}
			if !AllowedComponentKinds[value] {
				problems = append(problems, fmt.Sprintf("%s (%s): where kind %q is not a measured fact kind (allowed: %s)", loc, c.Name, value, allowedComponentKinds()))
			}
			continue
		}
		switch {
		// The presence test is a reserved value, not a comparison. It opens with
		// `<` and would otherwise be read as a malformed threshold.
		case value == WhereAnyValue:
		case value == "":
			problems = append(problems, fmt.Sprintf("%s (%s): where %s has an empty value — a property test with nothing to test for selects nothing", loc, c.Name, key))
		case strings.IndexFunc(value, unicode.IsSpace) >= 0:
			problems = append(problems, fmt.Sprintf("%s (%s): where %s value %q must carry no whitespace — a set-valued property is matched one whole member at a time", loc, c.Name, key, value))
		case refusedOpeningRune(value) != 0:
			problems = append(problems, fmt.Sprintf("%s (%s): where %s value %q opens with %q, a non-ASCII symbol this grammar has no reading for — the comparators are the ASCII >=, <=, > and <, and a relation pasted out of a rendered document compares nothing; a value may open with an ASCII rune or with a letter, a digit or a combining mark", loc, c.Name, key, value, refusedOpeningRune(value)))
		case opensThreshold(value):
			if _, _, ok := ParseThreshold(value); !ok {
				problems = append(problems, fmt.Sprintf("%s (%s): where %s value %q is not a numeric threshold — write one of >=, <=, >, < followed by a decimal number (an optional -, digits, an optional fraction), quoted so YAML reads it as a string; Inf, NaN, 0x1fp0 and 1_0 are Go literal forms, not thresholds", loc, c.Name, key, value))
			}
		}
	}
	if len(c.Predicate()) == 0 && len(problems) == 0 {
		problems = append(problems, fmt.Sprintf("%s (%s): where declares only %s, which narrows a kind but selects nothing on its own — add a property test, a match or a service", loc, c.Name, WhereReservedKind))
	}
	return problems
}

// Selects reports whether a component declares anything that can select a
// member. It reads the compiled predicate rather than the raw where map,
// because a where carrying only the reserved kind key compiles to no predicate
// at all — and a component the evaluator sees as unselectable must be the same
// component the validator refused.
func (c ConstraintComponent) Selects() bool {
	return len(c.Match) > 0 || c.Service != "" || len(c.Predicate()) > 0 || c.Ancestor != "" || len(c.Handles) > 0 || c.GovernedBy != ""
}

// validPropName is the character set a where key may use. It is deliberately
// not validToken: fact property names carry underscores (storage_kind,
// symbol_kind, getter_calls) where component names and rule ids carry hyphens,
// and folding the two vocabularies together would let a typo in one pass as
// valid in the other.
func validPropName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
