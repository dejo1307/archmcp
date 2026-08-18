package rubyextractor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/factpath"
	"github.com/enola-labs/enola/internal/facts"
)

// A view names the partial it renders as a string literal — `render
// "accounts/help_contact"` — and Rails resolves that name against the views
// tree at request time. No AST resolver follows it, so view-to-view
// composition is invisible to the graph: the file_ref pass records only that
// `render` was called, never what it rendered. This pass records the literal
// target the same way the Stimulus pass records controller bindings: the
// declaration always becomes a named fact, and the fact links to the partial
// file only when Rails' lookup convention (an underscore-prefixed template in
// the named directory, or beside the rendering view) finds it on disk.
// Anything short of a plain literal — `render @post`, interpolation, a
// variable — emits nothing, and a literal whose partial does not exist stays
// name-only: a declared render the graph could not ground, never a guessed
// edge.

var (
	// renderLiteralRe captures the first argument of a render call when it is a
	// plain quoted string, with or without parentheses and the partial: keyword.
	// Bounded on purpose: one literal, no expression forms, and the capture
	// excludes quotes so an interpolated name never matches as a whole.
	renderLiteralRe = regexp.MustCompile(`\brender\s*\(?\s*(?:partial:\s*)?(?:"([^"]*)"|'([^']*)')`)
	// renderNameRe admits the template names Rails' lookup accepts: path
	// segments of word characters, dots and dashes. Anything else — an embedded
	// `#{`, a space, an empty string — declares nothing.
	renderNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_][A-Za-z0-9_.-]*)*$`)
)

// partialExtensions is the fixed probe order for a partial's file, so the
// linked target never depends on directory iteration. HTML first because it is
// the overwhelmingly common format, then the other Rails template kinds this
// extractor already reads.
var partialExtensions = []string{
	".html.erb", ".text.erb", ".turbo_stream.erb", ".erb",
	".html.slim", ".slim", ".html.haml", ".haml",
}

// extractRenderTargets scans one view template for literal render targets and
// emits one fact per declared target, sorted by target so the file's facts are
// a function of what it declares, not of markup order.
func extractRenderTargets(repoPath, relFile string, src []byte) []facts.Fact {
	embedded := extractEmbeddedRuby(src, relFile)
	if len(embedded) == 0 {
		return nil
	}
	declared := map[string]bool{}
	for _, m := range renderLiteralRe.FindAllStringSubmatch(string(embedded), -1) {
		target := m[1] + m[2]
		if renderNameRe.MatchString(target) {
			declared[target] = true
		}
	}
	targets := make([]string, 0, len(declared))
	for target := range declared {
		targets = append(targets, target)
	}
	sort.Strings(targets)

	var out []facts.Fact
	for _, target := range targets {
		fact := facts.Fact{
			Kind: facts.KindDependency,
			Name: fmt.Sprintf("render: %s -> %s", relFile, target),
			File: relFile,
			Props: map[string]any{
				"language":         "ruby",
				"framework":        "rails",
				"resolution_level": "literal-declared",
			},
		}
		if partial := partialFile(repoPath, relFile, target); partial != "" {
			fact.Relations = []facts.Relation{{Kind: facts.RelDependsOn, Target: partial}}
		}
		out = append(out, fact)
	}
	return out
}

// partialFile maps a literal render target to the partial path Rails' lookup
// would try and returns it only when that file exists. A target with a
// directory renders from the views root the calling template lives under; a
// bare name renders from the calling template's own directory. A template
// outside any views tree has no root to resolve against, so its targets stay
// name-only.
func partialFile(repoPath, relFile, target string) string {
	dir := factpath.Dir(relFile)
	slash := strings.LastIndex(target, "/")
	if slash >= 0 {
		idx := strings.LastIndex(filepath.ToSlash(relFile), "/views/")
		if idx < 0 {
			return ""
		}
		root := relFile[:idx+len("/views/")]
		dir = filepath.Join(root, target[:slash])
		target = target[slash+1:]
	}
	for _, ext := range partialExtensions {
		candidate := filepath.Join(dir, "_"+target+ext)
		if _, err := os.Stat(filepath.Join(repoPath, candidate)); err == nil {
			return filepath.ToSlash(candidate)
		}
	}
	return ""
}
