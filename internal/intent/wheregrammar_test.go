package intent

import (
	"strings"
	"testing"
	"unicode"
)

// The narrowing half of the unicode defect: an ASCII whitespace screen against
// a unicode-splitting decoder let a value pasted out of a rendered document
// validate clean and select on its first word alone.
func TestWhere_AnyUnicodeWhitespaceInAValueIsAValidationProblem(t *testing.T) {
	for _, value := range []string{
		"ViewComponent::Base AND_ALSO_THIS",
		"ViewComponent::Base\tAND_ALSO_THIS",
		"\nViewComponent::Base",
		"ViewComponent::Base\u00a0AND_ALSO_THIS",
		"ViewComponent::Base\u0085AND_ALSO_THIS",
		"ViewComponent::Base\vAND_ALSO_THIS",
		"ViewComponent::Base\fAND_ALSO_THIS",
		"ViewComponent::Base\rAND_ALSO_THIS",
	} {
		c := ConstraintComponent{Name: "concepts", Where: map[string]any{"superclass": value}}
		problems := whereProblems("loc", c)
		if len(problems) != 1 || !strings.Contains(problems[0], "must carry no whitespace") {
			t.Errorf("whereProblems(%q) = %v, want the whitespace problem", value, problems)
		}
	}
}

// The widening half: even if a value carrying whitespace reached compilation,
// the round trip must return the predicate the declaration wrote — never a
// shorter one, because a predicate that lost a test selects MORE.
func TestWhere_EncodeDecodeRoundTripIsLosslessOverWhitespace(t *testing.T) {
	for _, value := range []string{
		"ViewComponent::Base AND_ALSO_THIS",
		"\nViewComponent::Base",
		"a b",
		"100% Sure",
		"trailing ",
		"%41",
	} {
		predicate := map[string]string{"superclass": value}
		decoded := DecodeWhere(EncodeWhere(predicate))
		if len(decoded) != 1 || decoded[0].Prop != "superclass" || decoded[0].Value != value {
			t.Errorf("round trip of %q = %+v, want one pair carrying the value verbatim", value, decoded)
		}
	}
}

// A compiled where prop that decodes to no property test must select nothing.
// The widening vector was the opposite: an empty predicate handed the component
// every fact its match patterns covered, and a strict 1.0 breach was
// manufactured against a class the declaration excluded.
func TestWhere_UndecodableFieldsBecomeUnsatisfiableRatherThanAbsent(t *testing.T) {
	for _, tc := range []struct {
		encoded string
		fields  int
	}{
		{"superclass= Base", 2},
		{"=Base", 1},
		{"superclass=", 1},
		{"Base", 1},
		{"   ", 1},
		// The case the whole-prop fallback cannot catch: one field decodes and
		// another does not, so the predicate would come back one test SHORTER
		// than the declaration wrote it — and a predicate missing a test selects
		// more, never less.
		{"superclass=Base junk", 2},
	} {
		decoded := DecodeWhere(tc.encoded)
		if len(decoded) != tc.fields {
			t.Fatalf("DecodeWhere(%q) = %+v, want %d pair(s): nothing may be dropped", tc.encoded, decoded, tc.fields)
		}
		unsatisfiable := false
		for _, pair := range decoded {
			if pair.Unsatisfiable {
				unsatisfiable = true
			}
		}
		if !unsatisfiable {
			t.Errorf("DecodeWhere(%q) = %+v, want at least one unsatisfiable pair", tc.encoded, decoded)
		}
	}
}

// A where carrying only the reserved kind key compiles to no predicate, so the
// evaluator sees an unselectable component. Validation inspected the raw map
// and let it through, which is the two surfaces disagreeing about one
// declaration.
func TestWhere_APredicateThatNarrowsNothingIsAValidationProblem(t *testing.T) {
	c := ConstraintComponent{Name: "concepts", Where: map[string]any{"kind": "symbol"}}
	problems := constraintProblems([]ConstraintComponent{c}, nil)
	found := false
	for _, p := range problems {
		if strings.Contains(p, "narrows a kind but selects nothing on its own") {
			found = true
		}
	}
	if !found {
		t.Errorf("problems = %v, want the narrows-nothing problem", problems)
	}
	if c.Selects() {
		t.Error("Selects() = true, want false: kind alone selects no member")
	}
}

// The likeliest typo in a Ruby shop. Read as a literal it is a token nothing
// equals — a rule that enforces nothing while reading as law.
func TestWhere_TransposedComparatorIsANamedError(t *testing.T) {
	for _, value := range []string{"=>30", "=<30", "==30", "!=30", "=30", ">=abc", ">"} {
		c := ConstraintComponent{Name: "hairy", Where: map[string]any{"cyclomatic": value}}
		problems := whereProblems("loc", c)
		if len(problems) != 1 || !strings.Contains(problems[0], "is not a numeric threshold") {
			t.Errorf("whereProblems(%q) = %v, want the threshold-grammar problem", value, problems)
		}
	}
}

// The silent widening: strconv.ParseFloat reads Go's whole numeric literal
// vocabulary, so "<=Inf" validated clean, selected every fact carrying the
// property as a number, and — being a well-formed threshold — emitted no
// finding at any confidence. "1_0" means ten and "0x1fp0" means thirty-one, and
// nobody reading the YAML would say so.
func TestWhere_NonDecimalThresholdsAreNamedErrors(t *testing.T) {
	for _, value := range []string{
		"<=Inf", "<=+Inf", ">=-Inf", ">-Inf", "<=inf", "<=Infinity",
		">=NaN", ">=nan",
		">=1_0", ">=0x1fp0", ">=0x10", ">=1e3", ">=+5", ">= 5", ">=5.", ">=.5",
	} {
		c := ConstraintComponent{Name: "hairy", Where: map[string]any{"cyclomatic": value}}
		problems := whereProblems("loc", c)
		if len(problems) != 1 {
			t.Errorf("whereProblems(%q) = %v, want exactly the threshold-grammar problem", value, problems)
			continue
		}
		if _, _, ok := ParseThreshold(value); ok {
			t.Errorf("ParseThreshold(%q) parsed — the validator and the evaluator must read one grammar", value)
		}
	}
}

// A comparator that has been through a rendered document. opensThreshold read
// value[0] as a byte, so the leading 0xE2 of "≥30" opened nothing: the value
// validated with no problem at all and then selected nothing, which is the
// silence "=>30" is already rejected to prevent.
//
// The first sixteen of these were a blocklist, and the rest are why a blocklist
// was the wrong shape: every one of them compiled to a literal token and
// selected nothing with only a 0.4 advisory to show for it, because nobody had
// named it. The grammar is ASCII, so the screen is an allowlist now and the
// list below is a sample of an open set rather than its definition.
func TestWhere_MultiByteComparatorLookalikesAreNamedErrors(t *testing.T) {
	for _, value := range []string{
		"≥30", "≤30", "≠30", "＞30", "＜30", "＝30", "»30", "«30", "‹30", "›30",
		"⩾30", "⩽30", "≧30", "≦30", "≮30", "≯30",
		"≫30", "≪30", "≳30", "≲30", "﹥30", "﹤30", "﹦30", "⩼30", "⋝30", "⋜30",
		"≽30", "⩵30", "≡30", "≈30", "⟩30", "〉30", "❯30", "⇒30", "→30",
	} {
		c := ConstraintComponent{Name: "hairy", Where: map[string]any{"cyclomatic": value}}
		problems := whereProblems("loc", c)
		if len(problems) != 1 || !strings.Contains(problems[0], "a non-ASCII symbol this grammar has no reading for") {
			t.Errorf("whereProblems(%q) = %v, want the refused-opening-rune problem", value, problems)
		}
	}
}

// The allowlist has to leave the alphabets a real identifier is written in
// alone: refusing every non-ASCII rune would be a blocklist with the opposite
// sign, and a property value naming a class written in one of them has to keep
// validating.
func TestWhere_NonASCIILettersAndDigitsStillValidate(t *testing.T) {
	for _, value := range []string{"Ünicode::Klass", "Ärende", "Θεός", "модуль", "日本語クラス", "٣rd"} {
		c := ConstraintComponent{Name: "named", Where: map[string]any{"superclass": value}}
		if problems := whereProblems("loc", c); len(problems) != 0 {
			t.Errorf("whereProblems(%q) = %v, want none — the screen refuses symbols, not alphabets", value, problems)
		}
	}
}

func TestWhere_WellFormedThresholdsStillValidate(t *testing.T) {
	for _, value := range []string{">=30", "<=2", ">0", "<3.5", ">=0", ">-1", "<=12.75"} {
		c := ConstraintComponent{Name: "hairy", Where: map[string]any{"cyclomatic": value}}
		if problems := whereProblems("loc", c); len(problems) != 0 {
			t.Errorf("whereProblems(%q) = %v, want none", value, problems)
		}
	}
}

// The escape is what keeps a compiled predicate one field per pair, and the
// round trip alone cannot see it: the decoder splits on the ASCII space, so a
// value carrying only exotic whitespace survives an ASCII-only escape intact
// and the test passes with the screen broken. Assert the property directly —
// after encoding, the only whitespace left in the compiled prop is the single
// space separating one pair from the next.
func TestWhere_EncodeLeavesNoUnescapedWhitespaceInAField(t *testing.T) {
	for _, ws := range []rune{
		'\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xa0,
		0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006,
		0x2007, 0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000,
	} {
		encoded := EncodeWhere(map[string]string{"superclass": "A" + string(ws) + "B", "framework": "rails"})
		fields := strings.Split(encoded, " ")
		if len(fields) != 2 {
			t.Errorf("EncodeWhere with U+%04X = %q, want two fields: an unescaped whitespace rune is a field the decoder can lose", ws, encoded)
		}
		for _, field := range fields {
			if strings.IndexFunc(field, unicode.IsSpace) >= 0 {
				t.Errorf("field %q of U+%04X carries unescaped whitespace", field, ws)
			}
		}
		decoded := DecodeWhere(encoded)
		if len(decoded) != 2 {
			t.Errorf("round trip of U+%04X = %+v, want both pairs", ws, decoded)
		}
	}
}
