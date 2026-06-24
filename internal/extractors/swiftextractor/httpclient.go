package swiftextractor

import (
	"bytes"
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
	".gif": true, ".zip": true, ".plist": true, ".sqlite": true,
}

// extractURLSessionFacts emits a client-route fact for every URLSession request
// in a Swift source file. The path comes from baseURL.appendingPathComponent("…")
// and the HTTP method from a nearby `.httpMethod = "…"` assignment (defaulting
// to GET when none is found within methodWindow lines). Paths are base-relative
// (no /api prefix); the cross-repo linker's suffix matching reconciles that
// against the backend's full path.
func extractURLSessionFacts(src []byte, relFile string) []facts.Fact {
	// File-level gate: appendingPathComponent is also how Swift builds local file
	// URLs. Only treat it as a network call in files that actually use URLSession /
	// URLRequest, which excludes file-I/O and build-tooling sources outright.
	if !bytes.Contains(src, []byte("URLSession")) && !bytes.Contains(src, []byte("URLRequest")) {
		return nil
	}

	lines := strings.Split(string(src), "\n")
	dir := filepath.ToSlash(filepath.Dir(relFile))
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
