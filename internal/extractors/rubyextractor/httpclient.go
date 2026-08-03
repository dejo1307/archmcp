package rubyextractor

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// rubyClientCall matches an outbound HTTP-client call whose first argument is a
// string literal, capturing the receiver, the HTTP verb, and the path literal in
// one of two groups (double- or single-quote — RE2 has no backreferences). It
// matches both parenthesized and bare-argument forms, e.g.
//
//	SvcCheckoutClient.post('purchase/build')
//	conn.get "users/123"
//
// The leading (?:^|[^.\w@$]) ensures the receiver is not itself the tail of a
// longer method chain (e.g. record.posts.get), which cuts ActiveRecord noise.
var rubyClientCall = regexp.MustCompile(`(?:^|[^.\w@$])([A-Za-z_][A-Za-z0-9_]*(?:::[A-Za-z_][A-Za-z0-9_]*)*)\.(get|post|put|patch|delete|head)\b\s*\(?\s*(?:"([^"]*)"|'([^']*)')`)

// rubyInterpolation matches a Ruby string interpolation, e.g. #{id}.
var rubyInterpolation = regexp.MustCompile(`#\{[^}]*\}`)

// rubyEnvVar matches an environment-variable read, capturing the variable name,
// e.g. ENV['CORE_HTTP_CLIENT_BASE_URL'] or ENV.fetch("XENDO_URL").
var rubyEnvVar = regexp.MustCompile(`ENV(?:\.fetch)?\s*[\(\[]\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`)

// httpClientReceivers are the lowercased base receiver names (after stripping any
// "::"-scope) that denote a hand-written HTTP client, in addition to any constant
// whose name ends in "Client" (e.g. SvcCheckoutClient).
var httpClientReceivers = map[string]bool{
	"conn": true, "connection": true, "http": true,
	"client": true, "faraday": true, "req": true, "request": true,
}

// extractRubyHTTPClientFacts emits a client-route fact for every hand-written
// HTTP-client call (Faraday / Net::HTTP / wrapper clients) in a Ruby source
// file. These represent outbound calls the app makes, so the cross-repo linker
// can match them (by method + path suffix) to the service route that serves
// them. Paths are emitted as written; the linker's suffix matching reconciles
// base-path/prefix differences.
func extractRubyHTTPClientFacts(src []byte, relFile string) []facts.Fact {
	api := rubyAPIHint(relFile)
	envHint := envVarHint(string(src)) // file-level fallback

	var out []facts.Fact
	for i, line := range strings.Split(string(src), "\n") {
		m := rubyClientCall.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		recv := m[1]
		if !isHTTPClientReceiver(recv) {
			continue
		}
		raw := m[3]
		if raw == "" {
			raw = m[4]
		}
		path, ok := cleanRubyPath(raw)
		if !ok {
			continue
		}
		hint := hintFromReceiver(recv)
		if hint == "" {
			if h := envVarHint(line); h != "" {
				hint = h
			} else {
				hint = envHint
			}
		}
		out = append(out, facts.Fact{
			Kind: facts.KindRoute,
			Name: path,
			File: relFile,
			Line: i + 1,
			Props: map[string]any{
				facts.PropRole:   facts.RoleClient,
				"method":         strings.ToUpper(m[2]),
				"framework":      rubyFramework(recv),
				"language":       "ruby",
				facts.PropSource: facts.RouteSourceRubyHTTPClient,
				"api":            api,
				"target_hint":    hint,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: filepath.ToSlash(filepath.Dir(relFile))}},
		})
	}
	return out
}

// isHTTPClientReceiver reports whether a receiver token names an HTTP client: a
// constant ending in "Client", or a known client variable name. The base name
// after the last "::" is used so Net::HTTP and similar scoped receivers match.
func isHTTPClientReceiver(recv string) bool {
	base := recv
	if i := strings.LastIndex(base, "::"); i >= 0 {
		base = base[i+2:]
	}
	if strings.HasSuffix(base, "Client") {
		return true
	}
	return httpClientReceivers[strings.ToLower(base)]
}

// rubyFramework classifies the client framework from the receiver token.
func rubyFramework(recv string) string {
	base := recv
	if i := strings.LastIndex(base, "::"); i >= 0 {
		base = base[i+2:]
	}
	switch {
	case strings.EqualFold(base, "HTTP") || strings.Contains(recv, "Net::HTTP"):
		return "net-http"
	case strings.EqualFold(base, "faraday") || strings.EqualFold(base, "conn") || strings.EqualFold(base, "connection"):
		return "faraday"
	default:
		return "http-client"
	}
}

// cleanRubyPath turns a client call's URL literal into a matchable route path, or
// returns ok=false when it is not a backend path (external, empty, or fully
// dynamic). It skips absolute URLs, drops the query string, and collapses Ruby
// interpolations to the {} placeholder. Mirrors tsextractor.cleanTSPath.
func cleanRubyPath(raw string) (string, bool) {
	p := strings.TrimSpace(raw)
	if strings.HasPrefix(p, "http") {
		return "", false // absolute URL → third-party, not a cross-repo backend path
	}
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	p = rubyInterpolation.ReplaceAllString(p, "{}")
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return "", false
	}
	// Require at least one concrete (non-placeholder) segment so a fully dynamic
	// path (e.g. just "#{path}") is skipped.
	for _, seg := range strings.Split(p, "/") {
		if seg != "" && seg != "{}" {
			return p, true
		}
	}
	return "", false
}

// hintFromReceiver derives a provider hint from a wrapper-client constant by
// stripping a trailing "Client" and lowercasing, e.g. SvcCheckoutClient ->
// "svccheckout". Returns "" for plain variable receivers (conn, client, http).
func hintFromReceiver(recv string) string {
	base := recv
	if i := strings.LastIndex(base, "::"); i >= 0 {
		base = base[i+2:]
	}
	if !strings.HasSuffix(base, "Client") || base == "Client" {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(base, "Client"))
}

// envVarHint returns a provider hint derived from the first base-URL env var name
// found in s, or "" if none. See stripURLVarSuffix for the suffix rule.
func envVarHint(s string) string {
	if m := rubyEnvVar.FindStringSubmatch(s); m != nil {
		return stripURLVarSuffix(m[1])
	}
	return ""
}

// urlVarSuffixes are env-var name suffixes stripped (longest first) to recover
// the target-service token, e.g. CORE_HTTP_CLIENT_BASE_URL -> "core",
// XENDO_URL -> "xendo".
var urlVarSuffixes = []string{
	"_HTTP_CLIENT_BASE_URL", "_CLIENT_BASE_URL", "_SERVICE_URL",
	"_BASE_URL", "_API_URL", "_URL", "_HOST", "_ENDPOINT",
}

// stripURLVarSuffix removes the longest matching base-URL suffix from an env-var
// name and lowercases the remainder (dropping underscores), yielding a provider
// hint. Returns "" if nothing is left after stripping.
func stripURLVarSuffix(name string) string {
	for _, suf := range urlVarSuffixes {
		if strings.HasSuffix(name, suf) && len(name) > len(suf) {
			name = name[:len(name)-len(suf)]
			break
		}
	}
	return strings.ReplaceAll(strings.ToLower(name), "_", "")
}

// rubyAPIHint returns the source file's base name without extension, used as the
// cross-repo linker's disambiguation hint.
func rubyAPIHint(relFile string) string {
	base := filepath.Base(relFile)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
