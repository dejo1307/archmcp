// Package gqlscan reads GraphQL operation documents with an exact text
// scanner, shared by every extractor that meets client-side GraphQL —
// TypeScript gql tags and .graphql documents, and Ruby query strings. One
// scanner means one definition of "an operation's root fields", so the same
// document yields the same route names no matter which language carried it.
package gqlscan

import "regexp"

// OperationHead matches the start of a GraphQL operation — the kind keyword
// through its opening brace — at statement position.
var OperationHead = regexp.MustCompile(`(?m)^\s*(query|mutation|subscription)\b[^{]*\{`)

// RootFields returns the depth-1 field names of an operation body starting
// just after its opening brace. Fragment spreads, directives and arguments are
// skipped; a field is the identifier that opens a depth-1 selection. For an
// `alias: field` selection the FIELD is the contract name, the alias local.
func RootFields(body string) []string {
	var fields []string
	depth := 1
	i := 0
	expectField := true
	for i < len(body) && depth > 0 {
		c := body[i]
		switch {
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
		case c == '.':
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
