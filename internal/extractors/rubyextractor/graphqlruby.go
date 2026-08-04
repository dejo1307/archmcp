package rubyextractor

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// graphql-ruby contract extraction — the server half of the GraphQL seam.
//
// A graphql-ruby schema's operation surface is declared with the `field` DSL
// inside the root types (QueryType / MutationType / SubscriptionType). Those
// declarations are the contract a client's operation names bind to, so each
// becomes a route fact (`Query.fieldName`, type=graphql, role=server) — the
// composed-path idea from NestJS applied to GraphQL's root fields. Non-root
// types' fields are schema internals, not operations, and emit nothing.

var graphqlRootClass = regexp.MustCompile(`class\s+(Query|Mutation|Subscription)Type\b`)

// graphqlFieldDecl matches `field :name` at a plausible DSL position. The
// symbol form is the graphql-ruby convention; a string first argument is not
// part of the root-field DSL.
var graphqlFieldDecl = regexp.MustCompile(`(?m)^\s*field\s+:([a-z_][a-z0-9_]*)`)

// extractGraphQLRubyRoutes emits one server-role graphql route per root field.
// Field names camelize exactly as graphql-ruby does by default (snake_case →
// camelCase); a schema overriding that default would need configuration this
// deliberately does not read — the miss is visible as an unmatched client op.
func extractGraphQLRubyRoutes(src []byte, relFile string) []facts.Fact {
	if !strings.Contains(filepath.ToSlash(relFile), "graphql") {
		return nil
	}
	root := graphqlRootClass.FindSubmatchIndex(src)
	if root == nil {
		return nil
	}
	kind := string(src[root[2]:root[3]])
	var out []facts.Fact
	seen := map[string]bool{}
	for _, m := range graphqlFieldDecl.FindAllSubmatchIndex(src, -1) {
		name := graphqlCamelize(string(src[m[2]:m[3]]))
		full := kind + "." + name
		if seen[full] {
			continue
		}
		seen[full] = true
		out = append(out, facts.Fact{
			Kind: facts.KindRoute,
			Name: full,
			File: relFile,
			Line: 1 + countNewlines(src[:m[0]]),
			Props: map[string]any{
				"language":          "ruby",
				"framework":         "graphql-ruby",
				facts.PropRole:      facts.RoleServer,
				facts.PropRouteType: facts.RouteTypeGraphQL,
				facts.PropSource:    facts.RouteSourceGraphQLRubyDSL,
			},
		})
	}
	return out
}

func graphqlCamelize(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func countNewlines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}
