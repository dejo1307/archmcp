package tsextractor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/factpath"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/gqlscan"
	sitter "github.com/tree-sitter/go-tree-sitter"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
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

// GraphQL server SDL — the server half of the seam for schema-first Node
// GraphQL servers (Apollo, Yoga, Mercurius, express-graphql, graphql-http, and
// schemas assembled with GraphQL Tools or GraphQL.js).
//
// A schema-first server names its SDL as a plain string or a gql-tagged
// template (`type Query { … }`), which is a different
// grammar from an operation document: a field carries a return type after `:`
// or an argument list after `(`, where an operation's root field carries
// neither. Root fields on Query/Mutation/Subscription (including `extend type`,
// how a modular schema splits its root fields across files) become server-role
// route facts, joinable against the same client route facts gql tags and
// .graphql documents already produce. Resolver-to-field binding is a separate,
// later capability — this reads only the schema surface, the way the
// graphql-ruby field DSL does on the Ruby side.

// graphqlServerSignal is deliberately a set of high-confidence constructor,
// registration, and package-import signals. Package strings cover APIs whose
// local import may be aliased; call shapes cover GraphQL.js and GraphQL Tools.
// Apollo stops at the constructor name because TypeScript may insert a generic
// argument list (`new ApolloServer<MyContext>(...)`).
var graphqlServerPackages = map[string]bool{
	"@apollo/server": true, "apollo-server": true, "apollo-server-express": true,
	"apollo-server-fastify": true, "apollo-server-koa": true,
	"graphql-yoga": true, "mercurius": true, "express-graphql": true,
	"graphql-http": true, "@graphql-tools/schema": true,
}

type graphqlServerContext struct {
	enabled      bool
	sdlDocuments map[string]bool
}

// detectGraphQLServerUsage establishes repository context through syntax, not
// text: examples in comments and strings must not turn a library into a server.
// It also records standalone SDL documents with direct provenance (an import by
// a server-construction file, or Hasura's metadata convention).
func detectGraphQLServerUsage(repoPath string, files []string) graphqlServerContext {
	ctx := graphqlServerContext{sdlDocuments: map[string]bool{}}
	for _, relFile := range files {
		if facts.IsTestPath(relFile) {
			continue
		}
		if isGraphQLDocFile(relFile) {
			if isHasuraSDLPath(relFile) {
				ctx.enabled = true
				ctx.sdlDocuments[filepath.ToSlash(relFile)] = true
			}
			continue
		}
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			continue
		}
		if !possibleGraphQLServerSignal(src) {
			continue
		}
		server, imports := graphQLServerASTSignals(src, relFile)
		if server {
			ctx.enabled = true
			for _, imported := range imports {
				ctx.sdlDocuments[imported] = true
			}
		}
	}
	return ctx
}

func possibleGraphQLServerSignal(src []byte) bool {
	text := string(src)
	for _, token := range []string{
		"ApolloServer", "GraphQLServer", "buildSchema", "makeExecutableSchema", "graphqlHTTP",
		"@apollo/server", "apollo-server", "graphql-yoga", "mercurius", "express-graphql",
		"graphql-http", "@graphql-tools/schema",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func isHasuraSDLPath(path string) bool {
	p := "/" + strings.ToLower(filepath.ToSlash(path)) + "/"
	return strings.Contains(p, "/hasura/metadata/")
}

func graphQLServerASTSignals(src []byte, relFile string) (bool, []string) {
	lang := typescript.LanguageTypescript()
	if strings.HasSuffix(relFile, ".tsx") || strings.HasSuffix(relFile, ".jsx") {
		lang = typescript.LanguageTSX()
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(lang)); err != nil {
		return false, nil
	}
	tree := parser.Parse(src, nil)
	defer tree.Close()
	kinds := tsKindsFor(strings.HasSuffix(relFile, ".tsx") || strings.HasSuffix(relFile, ".jsx"))
	root := tree.RootNode()
	server := false
	var gqlImports []string
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch kindOf(kinds, n) {
		case "import_statement":
			if source := findChildByKind(kinds, n, "string"); source != nil {
				path := strings.Trim(nodeText(source, src), `"'`)
				base := path
				if strings.HasPrefix(base, "@") {
					parts := strings.Split(base, "/")
					if len(parts) >= 2 {
						base = parts[0] + "/" + parts[1]
					}
				} else if i := strings.IndexByte(base, '/'); i >= 0 {
					base = base[:i]
				}
				if graphqlServerPackages[base] {
					server = true
				}
				if isGraphQLDocFile(path) && strings.HasPrefix(path, ".") {
					gqlImports = append(gqlImports, factpath.Clean(factpath.Join(factpath.Dir(relFile), path)))
				}
			}
		case "new_expression":
			if ctor := n.ChildByFieldName("constructor"); ctor != nil {
				name := nodeText(ctor, src)
				if i := strings.IndexByte(name, '<'); i >= 0 {
					name = name[:i]
				}
				if name == "ApolloServer" || name == "GraphQLServer" {
					server = true
				}
			}
		case "call_expression":
			if fn := n.ChildByFieldName("function"); fn != nil {
				name := nodeText(fn, src)
				if name == "buildSchema" || name == "makeExecutableSchema" || name == "graphqlHTTP" {
					server = true
				}
			}
		}
		for i := range n.ChildCount() {
			walk(n.Child(i))
		}
	}
	walk(root)
	if !server {
		return false, nil
	}
	return true, gqlImports
}

// serverSDLOpen matches an SDL binding/object property or direct buildSchema
// call through its opening backtick. Bindings may carry a TypeScript annotation
// (`const schema: string = ...`) and modular schemas commonly use suffixed names
// (`gqlSchema`, `userTypeDefs`). Object properties stay restricted to the
// conventional schema/typeDefs keys so an arbitrary fooSchema property is not
// promoted merely because the repository also runs a GraphQL server.
var serverSDLOpen = regexp.MustCompile("(?:(?:\\b(?:typeDefs|schema|[A-Za-z_$][A-Za-z0-9_$]*(?:TypeDefs|Schema))\\s*(?::\\s*[^=\\n]+)?\\s*=)|(?:\\b(?:typeDefs|schema)\\s*:)|(?:\\bbuildSchema\\s*\\())\\s*(?:(?:gql|graphql)\\s*)?`")

// sdlTypeBlock matches a Query/Mutation/Subscription root type declaration
// through its opening brace.
var sdlTypeBlock = regexp.MustCompile(`(?m)^\s*(?:extend\s+)?type\s+(Query|Mutation|Subscription)\b[^{]*\{`)

// extractGraphQLServerSDL returns one server route per root field in candidate
// SDL templates. The caller owns the repository-level server gate.
func extractGraphQLServerSDL(src []byte, relFile string) []facts.Fact {
	raw := string(src)
	if !strings.Contains(raw, "`") ||
		(!strings.Contains(raw, "schema") && !strings.Contains(raw, "Schema") &&
			!strings.Contains(raw, "typeDefs") && !strings.Contains(raw, "TypeDefs") &&
			!strings.Contains(raw, "buildSchema")) {
		return nil
	}
	text := string(blankTSComments(src, relFile))
	var out []facts.Fact
	seen := map[string]bool{}
	for _, m := range serverSDLOpen.FindAllStringIndex(text, -1) {
		open := m[1] - 1 // the backtick itself
		body, _ := templateBody(text, open)
		baseLine := 1 + strings.Count(text[:open], "\n")
		for _, f := range sdlRootFields(body, relFile, baseLine) {
			if seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			out = append(out, f)
		}
	}
	return out
}

func blankTSComments(src []byte, relFile string) []byte {
	lang := typescript.LanguageTypescript()
	if strings.HasSuffix(relFile, ".tsx") || strings.HasSuffix(relFile, ".jsx") {
		lang = typescript.LanguageTSX()
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(lang)); err != nil {
		return src
	}
	tree := parser.Parse(src, nil)
	defer tree.Close()
	out := append([]byte(nil), src...)
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Kind() == "comment" {
			for i := int(n.StartByte()); i < int(n.EndByte()) && i < len(out); i++ {
				if out[i] != '\n' && out[i] != '\r' {
					out[i] = ' '
				}
			}
			return
		}
		for i := range n.ChildCount() {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())
	return out
}

// extractGraphQLServerSDLDocument handles schema-first servers that load SDL
// from standalone .graphql/.gql files (Hasura metadata and loader-based Node
// servers are common examples). Repository context is established by the
// caller; this function only parses the document surface.
func extractGraphQLServerSDLDocument(src []byte, relFile string) []facts.Fact {
	return sdlRootFields(string(src), relFile, 1)
}

// sdlRootFields returns one route fact per root field declared inside every
// Query/Mutation/Subscription block of an SDL document, with line numbers
// relative to baseLine (the document's own start line in its enclosing file).
func sdlRootFields(body, relFile string, baseLine int) []facts.Fact {
	var out []facts.Fact
	for _, tm := range sdlTypeBlock.FindAllStringSubmatchIndex(body, -1) {
		kind := body[tm[2]:tm[3]] // Query, Mutation, or Subscription
		blockStart := tm[1]       // just after the opening brace
		blockEnd := matchingBrace(body, blockStart)
		blockLine := baseLine + strings.Count(body[:blockStart], "\n")
		for _, field := range sdlFieldNames(body[blockStart:blockEnd]) {
			out = append(out, facts.Fact{
				Kind: facts.KindRoute,
				Name: kind + "." + field.name,
				File: relFile,
				Line: blockLine + field.lineOffset,
				Props: map[string]any{
					"language":          "typescript",
					"framework":         "graphql-sdl",
					facts.PropRole:      facts.RoleServer,
					facts.PropRouteType: facts.RouteTypeGraphQL,
					facts.PropSource:    facts.RouteSourceGraphQLSDL,
				},
			})
		}
	}
	return out
}

// matchingBrace returns the index into text of the closing brace matching the
// opening one just before start (start is the position right after it), or
// len(text) if the block is unterminated. Comments and string/block-string
// values are skipped so braces in descriptions or default values do not alter
// the structural depth.
func matchingBrace(text string, start int) int {
	depth := 1
	i := start
	for i < len(text) && depth > 0 {
		switch text[i] {
		case '#':
			i = skipSDLComment(text, i)
			continue
		case '"':
			i = skipSDLString(text, i)
			continue
		case '{':
			depth++
		case '}':
			depth--
		}
		i++
	}
	if depth != 0 {
		return len(text)
	}
	return i - 1
}

type sdlField struct {
	name       string
	lineOffset int
}

// sdlFieldNames reads direct field declarations in a root type body. It tracks
// argument and nested-value depth so multiline arguments cannot masquerade as
// fields, and skips GraphQL comments and string/block-string descriptions.
func sdlFieldNames(block string) []sdlField {
	var fields []sdlField
	parenDepth, braceDepth, line := 0, 0, 0
	for i := 0; i < len(block); {
		switch {
		case block[i] == '\n':
			line++
			i++
		case block[i] == '#':
			i = skipSDLComment(block, i)
		case block[i] == '"':
			next := skipSDLString(block, i)
			line += strings.Count(block[i:next], "\n")
			i = next
		case block[i] == '(':
			parenDepth++
			i++
		case block[i] == ')':
			if parenDepth > 0 {
				parenDepth--
			}
			i++
		case block[i] == '{':
			braceDepth++
			i++
		case block[i] == '}':
			if braceDepth > 0 {
				braceDepth--
			}
			i++
		case parenDepth == 0 && braceDepth == 0 && isSDLIdentStart(block[i]):
			start := i
			i++
			for i < len(block) && isSDLIdentChar(block[i]) {
				i++
			}
			next := skipSDLTrivia(block, i)
			if !precededBySDLDirective(block, start) && next < len(block) && (block[next] == '(' || block[next] == ':') {
				fields = append(fields, sdlField{name: block[start:i], lineOffset: line})
			}
		default:
			i++
		}
	}
	return fields
}

func precededBySDLDirective(text string, i int) bool {
	for i > 0 {
		i--
		if text[i] == ' ' || text[i] == '\t' || text[i] == '\r' || text[i] == '\n' || text[i] == ',' {
			continue
		}
		return text[i] == '@'
	}
	return false
}

func skipSDLComment(text string, i int) int {
	for i < len(text) && text[i] != '\n' {
		i++
	}
	return i
}

func skipSDLString(text string, i int) int {
	if strings.HasPrefix(text[i:], `"""`) {
		if end := strings.Index(text[i+3:], `"""`); end >= 0 {
			return i + 3 + end + 3
		}
		return len(text)
	}
	for i++; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if i < len(text) && text[i] == '"' {
			return i + 1
		}
	}
	return len(text)
}

func skipSDLTrivia(text string, i int) int {
	for i < len(text) {
		if text[i] == ',' || text[i] == ' ' || text[i] == '\t' || text[i] == '\r' || text[i] == '\n' {
			i++
			continue
		}
		if text[i] == '#' {
			i = skipSDLComment(text, i)
			continue
		}
		break
	}
	return i
}

func isSDLIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isSDLIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
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
