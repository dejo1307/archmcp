// Package gqlscan reads GraphQL operation documents with an exact text
// scanner, shared by every extractor that meets client-side GraphQL —
// TypeScript gql tags and .graphql documents, and Ruby query strings. One
// scanner means one definition of "an operation's root fields", so the same
// document yields the same route names no matter which language carried it.
package gqlscan

import "regexp"

// OperationHead matches the start of a GraphQL operation — the kind keyword
// through its opening brace — at statement position.
//
// A `${…}` in the variable list is stepped over rather than treated as that
// brace. A mobile client declares `$pageviewFilters: [${filterType}!]`, and
// stopping at the interpolation's brace put the body start INSIDE it: the
// scanner then read `filterType` as the operation's first root field and every
// real field after it at the wrong depth. The two ends have to agree —
// RootFields skips interpolations too — or the fix only moves where the desync
// begins.
var OperationHead = regexp.MustCompile(`(?m)^\s*(query|mutation|subscription)\b(?:\$\{[^{}]*\}|[^{])*\{`)

// RootFields returns the depth-1 field names of an operation body starting
// just after its opening brace. Fragment spreads, directives and arguments are
// skipped; a field is the identifier that opens a depth-1 selection. For an
// `alias: field` selection the FIELD is the contract name, the alias local.
func isIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func RootFields(body string) []string {
	var fields []string
	depth := 1
	i := 0
	expectField := true
	for i < len(body) && depth > 0 {
		c := body[i]
		switch {
		case c == '$' && i+1 < len(body) && body[i+1] == '{':
			// A JavaScript template interpolation, not GraphQL. Its braces are
			// not selection sets and its identifier is not a field: a mobile
			// client declares `[${filterType}!]` in a variable list and a web
			// frontend writes `${inOverview ? '' : '…'}` inside one, and both
			// arrived in the graph as root fields named Query.filterType and
			// Query.inOverview. Worse than the two false facts is what counting
			// `${` as a depth increase does to everything after it — a
			// `${FRAGMENT}` spread at depth 1 desynchronises the counter, and
			// nested fields start reporting as root ones.
			//
			// Skipped brace-balanced rather than to the first `}`, because an
			// interpolation routinely contains its own object or nested
			// template.
			i += 2
			braces := 1
			for i < len(body) && braces > 0 {
				switch body[i] {
				case '{':
					braces++
				case '}':
					braces--
				}
				i++
			}
			expectField = depth == 1
		case c == '{':
			depth++
			expectField = depth == 1
			i++
		case c == '}':
			depth--
			expectField = depth == 1
			i++
		case c == '#':
			for i < len(body) && body[i] != '\n' {
				i++
			}
		case c == '(':
			par := 1
			i++
			for i < len(body) && par > 0 {
				switch body[i] {
				case '(':
					par++
				case ')':
					par--
				}
				i++
			}
		case c == '@':
			// A directive, not a field. `candidatesConnection(…)\n @connection(…)`
			// puts a newline between the field and its directive, and a newline
			// is what resets the scanner to expect a field — so `connection`
			// was read as a second root field on seven mobile-client documents.
			i++
			for i < len(body) && isIdentChar(body[i]) {
				i++
			}
			expectField = false
		case c == '.':
			// A fragment spread's name is not a field either, and the same
			// newline reset would otherwise take it.
			expectField = false
			i++
		case depth == 1 && expectField && (c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')):
			j := i
			for j < len(body) && (body[j] == '_' || (body[j] >= 'a' && body[j] <= 'z') ||
				(body[j] >= 'A' && body[j] <= 'Z') || (body[j] >= '0' && body[j] <= '9')) {
				j++
			}
			word := body[i:j]
			k := j
			for k < len(body) && (body[k] == ' ' || body[k] == '\t') {
				k++
			}
			if k < len(body) && body[k] == ':' {
				i = k + 1
				continue
			}
			if word != "on" && word != "fragment" {
				fields = append(fields, word)
			}
			expectField = false
			i = j
		case c == '\n' || c == ',':
			expectField = depth == 1
			i++
		default:
			i++
		}
	}
	return fields
}
