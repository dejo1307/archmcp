package tsextractor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/gqlscan"
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
//
// The operation grammar itself lives in internal/gqlscan, shared with the Ruby
// client scanner so both languages name the same document identically.

func isGraphQLDocFile(path string) bool {
	return strings.HasSuffix(path, ".graphql") || strings.HasSuffix(path, ".gql")
}

// detectGraphQLDocs probes for .graphql/.gql operation documents. A Swift or
// Kotlin repository carries its Apollo operation documents with no
// package.json anywhere (an iOS app being the measured case: 41 documents,
// zero TypeScript), so doc presence must activate the extractor on its own —
// the rest of the TypeScript machinery no-ops with no TS files to read, and
// schema COPIES stay inert because type-definition blocks emit nothing.
// Search depth adapts exactly as findTSRoot's does: Gradle nests Apollo
// documents deep (Android projects keep them at app/src/main/graphql/), so a
// deep-nested project searches further.
func detectGraphQLDocs(repoPath string) bool {
	maxDepth := 3
	if isDeepNestedProject(repoPath) {
		maxDepth = 8
	}
	found := false
	root := filepath.Clean(repoPath)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || tsSkipDirs[name]) {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(root, path)
			if rel != "." && strings.Count(filepath.ToSlash(rel), "/") >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if isGraphQLDocFile(path) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// extractGraphQLClientOps scans text (a gql template body or a .graphql file)
// for operations and returns one client route per root field.
func extractGraphQLClientOps(text, relFile, source string) []facts.Fact {
	var out []facts.Fact
	seen := map[string]bool{}
	for _, m := range gqlscan.OperationHead.FindAllStringSubmatchIndex(text, -1) {
		kind := text[m[2]:m[3]]
		kindName := strings.ToUpper(kind[:1]) + kind[1:]
		for _, field := range gqlscan.RootFields(text[m[1]:]) {
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
