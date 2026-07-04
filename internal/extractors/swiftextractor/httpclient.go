package swiftextractor

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// swiftPathComponent matches a URLRequest path built from a base URL, capturing
// the path literal, e.g. baseURL.appendingPathComponent("settings/feedback").
var swiftPathComponent = regexp.MustCompile(`appendingPathComponent\(\s*"([^"]*)"`)

// swiftHTTPMethod matches an explicit method assignment, e.g.
// request.httpMethod = "POST".
var swiftHTTPMethod = regexp.MustCompile(`\.httpMethod\s*=\s*"([A-Za-z]+)"`)

// methodWindow is how many lines after an appendingPathComponent call to scan
// for the associated httpMethod assignment.
const methodWindow = 5

// fileURLSignals are tokens that, when present on the same line as an
// appendingPathComponent call, mark the URL as a *file* URL rather than a network
// request — so the path is a local filesystem path, not an HTTP endpoint.
// appendingPathComponent is shared by file and network URLs, so this distinguishes
// the two without tracing the base variable across statements.
var fileURLSignals = []string{
	"fileURLWithPath", "FileManager", "temporaryDirectory",
	"cachesDirectory", "documentDirectory", "documentsDirectory",
}

// nonAPIExtensions are file extensions on a path's final segment that indicate a
// local media/document file rather than an HTTP endpoint. (.json/.md are
// intentionally absent: some APIs serve them, and the file-level network gate
// already excludes the build-tooling sources where they appear here.)
var nonAPIExtensions = map[string]bool{
	".mov": true, ".mp4": true, ".m4v": true, ".mp3": true, ".wav": true,
	".pdf": true, ".png": true, ".jpg": true, ".jpeg": true, ".heic": true,
	".gif": true, ".zip": true, ".plist": true, ".sqlite": true, ".bundle": true,
}

// extractURLSessionFacts emits a client-route fact for every URLSession request
// in a Swift source file. The path comes from baseURL.appendingPathComponent("…")
// and the HTTP method from a nearby `.httpMethod = "…"` assignment (defaulting
// to GET when none is found within methodWindow lines). Paths are base-relative
// (no /api prefix); the cross-repo linker's suffix matching reconciles that
// against the backend's full path.
func extractURLSessionFacts(src []byte, relFile string) []facts.Fact {
	return extractURLSessionFactsWithDir(src, relFile, filepath.ToSlash(filepath.Dir(relFile)))
}

// extractURLSessionFactsWithDir is extractURLSessionFacts with an explicit module
// identity dir, so route facts declare into the file's resolved target module
// rather than its leaf directory.
func extractURLSessionFactsWithDir(src []byte, relFile, dir string) []facts.Fact {
	// Test sources build request URLs against fixtures (e.g. an "APIResponses.bundle"
	// path), which are not real endpoints — skip them so they don't emit phantom routes.
	if isSwiftTestFile(relFile) {
		return nil
	}
	// File-level gate: appendingPathComponent is also how Swift builds local file
	// URLs. Only treat it as a network call in files that actually use URLSession /
	// URLRequest, which excludes file-I/O and build-tooling sources outright.
	if !bytes.Contains(src, []byte("URLSession")) && !bytes.Contains(src, []byte("URLRequest")) {
		return nil
	}

	lines := strings.Split(string(src), "\n")
	api := swiftAPIHint(relFile)

	var out []facts.Fact
	for i, line := range lines {
		pm := swiftPathComponent.FindStringSubmatch(line)
		if pm == nil {
			continue
		}
		// Skip file URLs that happen to live in a network file (e.g. a temp file
		// written before an upload): a file-URL signal on the line, or a media/doc
		// extension on the path, means this is filesystem I/O, not an endpoint.
		if isFileURLLine(line) {
			continue
		}
		path := cleanSwiftPath(pm[1])
		if path == "" || hasNonAPIExtension(path) {
			continue
		}
		method := methodNear(lines, i)
		out = append(out, facts.Fact{
			Kind: facts.KindRoute,
			Name: path,
			File: relFile,
			Line: i + 1,
			Props: map[string]any{
				"role":      "client",
				"method":    method,
				"framework": "urlsession",
				"language":  "swift",
				"source":    "urlsession",
				"api":       api,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}
	return out
}

// methodNear returns the HTTP method assigned within methodWindow lines at or
// after idx, or "GET" when none is found (the URLSession default).
func methodNear(lines []string, idx int) string {
	end := idx + methodWindow
	if end >= len(lines) {
		end = len(lines) - 1
	}
	for j := idx; j <= end; j++ {
		if m := swiftHTTPMethod.FindStringSubmatch(lines[j]); m != nil {
			return strings.ToUpper(m[1])
		}
	}
	return "GET"
}

// cleanSwiftPath converts Swift interpolation to the {} placeholder the linker
// understands and strips any query string.
func cleanSwiftPath(p string) string {
	p = collapseSwiftInterpolation(p)
	p = strings.TrimSpace(p)
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	return p
}

// collapseSwiftInterpolation replaces each Swift string interpolation \(...) with
// a single {} placeholder, correctly handling nested parentheses (e.g.
// \(UUID().uuidString) collapses to {} rather than leaking ".uuidString)").
func collapseSwiftInterpolation(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '\\' && s[i+1] == '(' {
			depth, j := 1, i+2
			for j < len(s) && depth > 0 {
				switch s[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
				j++
			}
			b.WriteString("{}")
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// isFileURLLine reports whether a line building an appendingPathComponent path is
// rooted at a file URL rather than a network request.
func isFileURLLine(line string) bool {
	for _, sig := range fileURLSignals {
		if strings.Contains(line, sig) {
			return true
		}
	}
	return false
}

// hasNonAPIExtension reports whether the path's final segment ends in a local
// media/document file extension (so it is filesystem I/O, not an HTTP endpoint).
func hasNonAPIExtension(path string) bool {
	last := path
	if i := strings.LastIndexByte(last, '/'); i >= 0 {
		last = last[i+1:]
	}
	return nonAPIExtensions[strings.ToLower(filepath.Ext(last))]
}

// swiftAPIHint returns the source file's base name without extension (e.g.
// "EntitlementAPIService"), used as the cross-repo linker's disambiguation hint.
func swiftAPIHint(relFile string) string {
	base := filepath.Base(relFile)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// isSwiftTestFile reports whether a Swift source is test/fixture code (under a
// Tests directory or a *Test(s)/*Spec file), whose request-building is against
// fixtures, not real endpoints.
func isSwiftTestFile(relFile string) bool {
	slash := filepath.ToSlash(relFile)
	if strings.Contains(slash, "/Tests/") || strings.HasPrefix(slash, "Tests/") {
		return true
	}
	base := strings.TrimSuffix(filepath.Base(slash), filepath.Ext(slash))
	return strings.HasSuffix(base, "Test") || strings.HasSuffix(base, "Tests") ||
		strings.HasSuffix(base, "Spec") || strings.HasSuffix(base, "TestCase")
}

// --- endpoint-enum client detection (Moya TargetType-like idiom) ---

// swiftVarFinders locate the opening brace of an endpoint's computed properties.
// The idiom is convention-based (a path property + a `method` property, each a
// `switch self`), independent of any specific protocol or app name.
var swiftVarFinders = func() map[string]*regexp.Regexp {
	m := map[string]*regexp.Regexp{}
	for _, n := range []string{"urlPathComponent", "path", "urlPrefixComponent", "basePath", "method"} {
		m[n] = regexp.MustCompile(`var\s+` + n + `\s*:\s*[^{]*\{`)
	}
	return m
}()

// swiftPathProps / swiftPrefixProps are the property names carrying an endpoint's
// path and its optional version/base prefix, most specific first.
var (
	swiftPathProps   = []string{"urlPathComponent", "path"}
	swiftPrefixProps = []string{"urlPrefixComponent", "basePath"}
)

// swiftReturnString captures the first string literal a `return` yields.
var swiftReturnString = regexp.MustCompile(`return\s+"([^"]*)"`)

// swiftReturnEnumCase captures the enum case a `return` yields, e.g. `.post`.
var swiftReturnEnumCase = regexp.MustCompile(`return\s+\.([A-Za-z]+)`)

// swiftCaseDot captures each `.label` token of a `case .a, .b(let x):` line.
var swiftCaseDot = regexp.MustCompile(`\.([A-Za-z_]\w*)`)

// extractEndpointFacts emits a client-route fact for every endpoint case in a
// Swift file that defines an endpoint-enum type: a type exposing a path computed
// property (urlPathComponent/path) and a `method` computed property, each a
// `switch self` keyed by enum case. Path, version prefix, and method are joined
// per case label. The version prefix is resolved in this order: the type's own
// per-case value, its switch `default:` branch, its single-value computed prefix,
// and finally defaultPrefix (the repo-wide protocol-extension default, e.g. "v2")
// when the type declares no prefix property at all. Returns nil for non-idiom files.
func extractEndpointFacts(src []byte, relFile, dir, defaultPrefix string) []facts.Fact {
	if isSwiftTestFile(relFile) {
		return nil
	}
	text := string(src)
	pathBody, pathIdx := firstPropBody(text, swiftPathProps)
	if pathBody == "" {
		return nil
	}
	methodBody, _ := firstPropBody(text, []string{"method"})
	if methodBody == "" {
		return nil // a path property without a method is not the endpoint idiom
	}
	pathByCase := switchReturns(pathBody, swiftReturnString)
	if len(pathByCase) == 0 {
		return nil
	}
	methodByCase := switchReturns(methodBody, swiftReturnEnumCase)
	resolvePrefix := prefixResolver(text, defaultPrefix)

	api := swiftAPIHint(relFile)
	line := 1 + strings.Count(text[:pathIdx], "\n")

	var out []facts.Fact
	seen := map[string]bool{}
	for label, rawPath := range pathByCase {
		if label == swiftDefaultLabel {
			continue // a default: path branch has no concrete case to pair — skip
		}
		path := cleanSwiftPath(rawPath)
		if path == "" || hasNonAPIExtension(path) {
			continue
		}
		if prefix := resolvePrefix(label); prefix != "" {
			path = joinURLPath(prefix, path)
		}
		method := "GET"
		verb := methodByCase[label]
		if verb == "" {
			verb = methodByCase[swiftDefaultLabel]
		}
		if v := mapSwiftMethod(verb); v != "" {
			method = v
		}
		key := method + "\x00" + path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, facts.Fact{
			Kind: facts.KindRoute,
			Name: path,
			File: relFile,
			Line: line,
			Props: map[string]any{
				"role":      "client",
				"method":    method,
				"framework": "apiendpoint",
				"language":  "swift",
				"source":    "swift-endpoint",
				"api":       api,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}
	return out
}

// prefixResolver returns a function mapping an enum-case label to the endpoint's
// resolved version prefix (already cleaned). It reads the type's urlPrefixComponent
// property in either form — a `switch self` (per-case + optional `default:`) or a
// single-value computed body — and falls back to defaultPrefix only when the type
// declares no prefix property at all, so an explicit per-type prefix is never
// overridden.
func prefixResolver(text, defaultPrefix string) func(string) string {
	body, _ := firstPropBody(text, swiftPrefixProps)
	if body == "" {
		clean := resolvePrefixValue(defaultPrefix)
		return func(string) string { return clean }
	}
	if containsSwitch(body) {
		byCase := switchReturns(body, swiftReturnString)
		return func(label string) string {
			v, ok := byCase[label]
			if !ok {
				v = byCase[swiftDefaultLabel]
			}
			return resolvePrefixValue(v)
		}
	}
	// Single-value computed prefix (implicit return), applies to every case.
	clean := resolvePrefixValue(firstQuoted(body))
	return func(string) string { return clean }
}

// firstPropBody returns the brace-delimited body of the first computed property
// named one of `names`, plus the byte index where its declaration begins. Returns
// ("", 0) if none is present.
func firstPropBody(text string, names []string) (string, int) {
	for _, name := range names {
		re := swiftVarFinders[name]
		if re == nil {
			continue
		}
		loc := re.FindStringIndex(text)
		if loc == nil {
			continue
		}
		if body, ok := braceBody(text, loc[1]-1); ok {
			return body, loc[0]
		}
	}
	return "", 0
}

// braceBody returns the text between the brace at open and its matching close.
// Swift string interpolation uses `\(…)` (not braces) and these property bodies
// are plain switches, so simple depth counting is sufficient.
func braceBody(text string, open int) (string, bool) {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[open+1 : i], true
			}
		}
	}
	return "", false
}

// swiftDefaultLabel is the sentinel key under which switchReturns records a
// `default:` branch's value, so callers can fall back to it for any case the
// switch does not name explicitly.
const swiftDefaultLabel = "\x00default"

// switchReturns maps each enum-case label in a `switch self` body to the value its
// branch returns, extracted by valueRe (group 1). Grouped cases ("case .a, .b:")
// share the value; a `default:` branch is recorded under swiftDefaultLabel; the
// first return wins per label.
func switchReturns(body string, valueRe *regexp.Regexp) map[string]string {
	out := map[string]string{}
	if body == "" {
		return out
	}
	var cur []string
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "case "):
			cur = caseLabels(t)
		case strings.HasPrefix(t, "default:"), strings.HasPrefix(t, "default :"):
			cur = []string{swiftDefaultLabel}
		}
		if m := valueRe.FindStringSubmatch(t); m != nil {
			for _, lbl := range cur {
				if _, ok := out[lbl]; !ok {
					out[lbl] = m[1]
				}
			}
		}
	}
	return out
}

// caseLabels extracts the enum-case labels of a `case .a, .b(let x):` line, up to
// the trailing colon (so a `.label` inside a where-clause value is not captured).
func caseLabels(line string) []string {
	if i := strings.IndexByte(line, ':'); i >= 0 {
		line = line[:i]
	}
	var out []string
	for _, m := range swiftCaseDot.FindAllStringSubmatch(line, -1) {
		out = append(out, m[1])
	}
	return out
}

// mapSwiftMethod maps an HTTPMethod enum case (get/post/…) to a verb, or "" if
// unrecognized (the caller defaults to GET).
func mapSwiftMethod(v string) string {
	switch strings.ToLower(v) {
	case "get", "post", "put", "patch", "delete", "head", "options":
		return strings.ToUpper(v)
	}
	return ""
}

// joinURLPath joins a prefix and path with a single slash, trimming surrounding
// slashes (e.g. "v2" + "orders.json" -> "v2/orders.json").
func joinURLPath(a, b string) string {
	a, b = strings.Trim(a, "/"), strings.Trim(b, "/")
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "/" + b
}

// swiftFirstQuoted captures the first double-quoted string literal in a body.
var swiftFirstQuoted = regexp.MustCompile(`"([^"]*)"`)

// swiftVersionInterp matches a Swift interpolation whose expression names a
// version constant, e.g. `\(APIVersion.v3)` or `\(.v2)`, capturing the digits.
// Prefix segments interpolate a version enum rather than a runtime value, so this
// resolves to the version token instead of the generic {} path-parameter placeholder.
var swiftVersionInterp = regexp.MustCompile(`\\\([^)]*?\bv(\d+)\b[^)]*?\)`)

// firstQuoted returns the first double-quoted string literal in s, or "".
func firstQuoted(s string) string {
	if m := swiftFirstQuoted.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// containsSwitch reports whether a property body is a `switch self` (vs a
// single-value computed body).
func containsSwitch(body string) bool {
	return strings.Contains(body, "switch") || strings.Contains(body, "case ")
}

// resolvePrefixValue turns a raw urlPrefixComponent literal into a clean path
// prefix: a version-constant interpolation `\(…vN…)` becomes `vN` (so
// "core/\(APIVersion.v3)" -> "core/v3"), then cleanSwiftPath collapses any other
// interpolation and strips a query string.
func resolvePrefixValue(s string) string {
	if s == "" {
		return ""
	}
	s = swiftVersionInterp.ReplaceAllString(s, "v$1")
	return cleanSwiftPath(s)
}

// swiftPrefixRequirement matches the endpoint protocol's urlPrefixComponent
// requirement (`var urlPrefixComponent: <Type> { get }`), which uniquely marks the
// file that declares the endpoint protocol — where the repo-wide default lives.
var swiftPrefixRequirement = regexp.MustCompile(`urlPrefixComponent\s*:\s*[\w.]+\s*\{\s*get\b`)

// detectDefaultURLPrefix returns the repo-wide default urlPrefixComponent value
// (e.g. "v2") declared in the endpoint protocol's extension, or "" if none. It
// looks only in the file that declares the protocol requirement, so a concrete
// type's single-value override (e.g. `{ "core/v3" }`) cannot be mistaken for the
// default. Meant to run once per repo before the per-file walk.
func detectDefaultURLPrefix(repoPath string, files []string) string {
	for _, relFile := range files {
		if !isSwiftFile(relFile) || isSwiftTestFile(relFile) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			continue
		}
		text := string(src)
		if !swiftPrefixRequirement.MatchString(text) {
			continue
		}
		if v := defaultPrefixLiteral(text); v != "" {
			return v
		}
	}
	return ""
}

// defaultPrefixLiteral scans every urlPrefixComponent computed property in a
// protocol-declaring file and returns the first single-value string literal it
// finds (the extension default), skipping the `{ get }` requirement and any
// `switch`-based body.
func defaultPrefixLiteral(text string) string {
	re := swiftVarFinders["urlPrefixComponent"]
	for _, loc := range re.FindAllStringIndex(text, -1) {
		body, ok := braceBody(text, loc[1]-1)
		if !ok || containsSwitch(body) {
			continue
		}
		if v := firstQuoted(body); v != "" {
			return resolvePrefixValue(v)
		}
	}
	return ""
}
