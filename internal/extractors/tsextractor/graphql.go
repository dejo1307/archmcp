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
	root := filepath.Clean(repoPath) //factpath:host
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

// gqlTagOpen matches the opening of a gql`…` / graphql`…` tagged template.
//
// The leading class is load-bearing: a tag sits where an expression starts, so
// the character before it is punctuation or whitespace, never part of a path.
// `uri: ${this.config.railsURL}/graphql` ends in the word and a backtick and is
// not a tag at all — the backtick closes the surrounding template — and a
// pattern keyed only on the word matched it. Where the body ENDS is not a regex
// question at all; see templateBody.
var gqlTagOpen = regexp.MustCompile("(?:^|[\\s=(,{\\[:;>?])(?:gql|graphql)`")

// templateBody returns the span of a template literal whose opening backtick is
// at start, handling the one thing a regex cannot: `${…}` interpolations that
// contain their own template literals, and therefore their own backticks.
//
// The old pattern was `([^`]*)`, which ends the body at the first inner
// backtick. One frontend's analytics reports interpolate a whole selection —
// `${inOverview ? `sessionPageviewQuery(…) {…}` : `…`}` — so the captured body
// was the operation head and half an interpolation, with no closing brace in
// it. Two consequences, and the quiet one is worse. The visible one: the head
// regex had no `${…}` to skip, so it stopped at the interpolation's brace and
// read the JavaScript variable as the operation's first field, which is where
// Query.inOverview and Query.onlyVisits came from. The quiet one: every real
// root field after the truncation point was never seen at all.
func templateBody(text string, start int) (body string, end int) {
	i := start + 1
	for i < len(text) {
		switch {
		case text[i] == '\\':
			i += 2
		case text[i] == '`':
			return text[start+1 : i], i + 1
		case text[i] == '$' && i+1 < len(text) && text[i+1] == '{':
			i = skipInterpolation(text, i+2)
		default:
			i++
		}
	}
	// Unterminated: return nothing. The tempting alternative — hand back the
	// rest of the file, since an operation at the top of it still names its
	// root fields — is how `uri: ${config.railsURL}/graphql` came to be read
	// as a tag opening. The backtick after that word CLOSES an ordinary
	// template, so the scan ran to end of file and read an Apollo options
	// object as a document, emitting Query.variables and Query.network. A
	// missing edge beats a wrong one.
	return "", len(text)
}

// skipInterpolation walks from just past a `${` to just past its matching `}`,
// following nested braces and any template literals inside — which may
// themselves interpolate.
func skipInterpolation(text string, i int) int {
	braces := 1
	for i < len(text) && braces > 0 {
		switch text[i] {
		case '\\':
			i++
		case '{':
			braces++
		case '}':
			braces--
		case '`':
			_, next := templateBody(text, i)
			i = next
			continue
		}
		i++
	}
	return i
}

// extractGraphQLTagFacts pulls client operations out of a source file's tagged
// templates.
func extractGraphQLTagFacts(src []byte, relFile string) []facts.Fact {
	text := string(src)
	if !strings.Contains(text, "gql`") && !strings.Contains(text, "graphql`") {
		return nil
	}
	var out []facts.Fact
	for at := 0; at < len(text); {
		m := gqlTagOpen.FindStringIndex(text[at:])
		if m == nil {
			break
		}
		open := at + m[1] - 1
		body, end := templateBody(text, open)
		for _, f := range extractGraphQLClientOps(body, relFile, facts.RouteSourceGraphQLTag) {
			f.Line += strings.Count(text[:open+1], "\n")
			out = append(out, f)
		}
		at = end
	}
	return out
}
