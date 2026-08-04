package tsextractor

import (
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// GraphQL client operations — the client half of the GraphQL seam.
//
// A gql-tagged template literal or a standalone .graphql operation document
// names its operation kind and root fields literally; each root field becomes a
// client-role route fact (`Query.pageViews`), the joinable name the graphql
// cross-repo signal matches against a server's root-field facts. Type
// definition blocks (`type Query { … }`) are schema COPIES on the client side
// (codegen inputs) and deliberately emit nothing — the operations are the
// client truth.

var gqlOperationHead = regexp.MustCompile(`(?m)^\s*(query|mutation|subscription)\b[^{]*\{`)

func isGraphQLDocFile(path string) bool {
	return strings.HasSuffix(path, ".graphql") || strings.HasSuffix(path, ".gql")
}

// extractGraphQLClientOps scans text (a gql template body or a .graphql file)
// for operations and returns one client route per root field.
func extractGraphQLClientOps(text, relFile, source string) []facts.Fact {
	var out []facts.Fact
	seen := map[string]bool{}
	for _, m := range gqlOperationHead.FindAllStringSubmatchIndex(text, -1) {
		kind := text[m[2]:m[3]]
		kindName := strings.ToUpper(kind[:1]) + kind[1:]
		for _, field := range gqlRootFields(text[m[1]:]) {
			full := kindName + "." + field
			if seen[full] {
				continue
			}
			seen[full] = true
			out = append(out, facts.Fact{
				Kind: facts.KindRoute,
				Name: full,
				File: relFile,
				Line: 1 + strings.Count(text[:m[0]], "\n"),
				Props: map[string]any{
					"language":          "typescript",
					"framework":         "graphql",
					facts.PropRole:      facts.RoleClient,
					facts.PropRouteType: facts.RouteTypeGraphQL,
					facts.PropSource:    source,
				},
			})
		}
	}
	return out
}

// gqlRootFields returns the depth-1 field names of an operation body starting
// just after its opening brace. Fragment spreads, directives and arguments are
// skipped; a field is the identifier that opens a depth-1 selection.
func gqlRootFields(body string) []string {
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
			i++ // fragment spread "..."
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
			// `alias: field` — the FIELD is the contract name, the alias local.
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

// gqlTag matches gql`…` / graphql`…` tagged template literals.
var gqlTag = regexp.MustCompile("(?s)\\b(?:gql|graphql)`([^`]*)`")

// extractGraphQLTagFacts pulls client operations out of a source file's tagged
// templates.
func extractGraphQLTagFacts(src []byte, relFile string) []facts.Fact {
	text := string(src)
	if !strings.Contains(text, "gql`") && !strings.Contains(text, "graphql`") {
		return nil
	}
	var out []facts.Fact
	for _, m := range gqlTag.FindAllStringSubmatchIndex(text, -1) {
		for _, f := range extractGraphQLClientOps(text[m[2]:m[3]], relFile, facts.RouteSourceGraphQLTag) {
			f.Line += strings.Count(text[:m[2]], "\n")
			out = append(out, f)
		}
	}
	return out
}
