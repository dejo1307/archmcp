// Package imports links repos by import / shared-library references.
package imports

import (
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/plugin"
)

// Signal draws an edge when a repo imports a module whose target names another loaded
// repo — by npm @scope, or by a leading path segment. It is the most direct evidence
// of a dependency there is: the consumer names the provider in its own source.
type Signal struct{}

// New returns the signal.
func New() *Signal { return &Signal{} }

func (s *Signal) Name() string { return "import" }

func (s *Signal) Phase() plugin.SignalPhase { return plugin.PhaseDirectional }

// --- signal (B): import / shared-lib references ---

func (s *Signal) Contribute(in plugin.SignalInput, out plugin.EvidenceSink) {
	for _, f := range in.Facts() {
		if f.Repo == "" || f.Kind == facts.KindService {
			continue
		}
		for _, rel := range f.Relations {
			if rel.Kind != facts.RelImports && rel.Kind != facts.RelDependsOn {
				continue
			}
			provider := importProvider(rel.Target, f.Repo, in, in.TopDirs(f.Repo), in.OwnScopes(f.Repo))
			if provider == "" {
				continue
			}
			e := out.Edge(f.Repo, provider)
			e.Via(facts.ViaImport)
			e.Sample(plugin.BucketImports, rel.Target)
		}
	}
}

// importProvider maps an import target to another loaded repo, or "" if none.
// It checks candidate identifiers from the target (the @scope, then each leading
// path segment) against the normalized repo labels, skipping self-matches.
//
// ownDirs is the consumer repo's own top-level source directories. A target rooted
// at one of them is an intra-repo file/module reference whose interior path
// segments may coincide with another repo's short label (e.g. a "com/acme/app/…"
// package path vs a backend repo labeled "acme"), so it is skipped before any
// candidate matching — this is what keeps a repo's own files from fabricating a
// cross-repo edge, while still allowing genuine deep import paths (e.g. a Go
// "github.com/org/repo/pkg", whose leading "github.com" is not a source dir).
//
// ownScopes is the set of npm @scopes the consumer repo itself publishes under.
// A scope is a namespace, not a directory, so it never appears in ownDirs — yet
// "@acme/x" imported from the repo that publishes "@acme/y" is a sibling package
// of the same project, not a dependency on a repo that happens to be labeled
// "acme". Checked only for scoped targets, so a Go "github.com/org/repo/pkg" is
// untouched. The trade-off is two loaded repos publishing under one shared
// scope: an import between them is missed, the direction this linker always errs.
func importProvider(target, consumer string, in plugin.SignalInput, ownDirs, ownScopes map[string]bool) string {
	target = strings.TrimSpace(target)
	// Skip relative / absolute filesystem imports — they are intra-repo.
	if target == "" || strings.HasPrefix(target, ".") || strings.HasPrefix(target, "/") {
		return ""
	}
	if head := leadingSegment(target); head != "" && ownDirs[facts.NormalizeRepoLabel(head)] {
		return "" // intra-repo self-reference, not a cross-repo dependency
	}
	if strings.HasPrefix(target, "@") {
		if scope := leadingSegment(target); scope != "" && ownScopes[facts.NormalizeRepoLabel(scope)] {
			return "" // a sibling package the consumer repo publishes itself
		}
	}
	for _, cand := range importCandidates(target) {
		if label, ok := in.ResolveRepo(cand); ok && label != consumer {
			return label
		}
	}
	return ""
}

// leadingSegment returns the first non-empty path segment of an import target or
// module name, ignoring a leading "@" scope marker (so "@app-web/lib" -> "app-web",
// "com/acme/app" -> "com", "acme" -> "acme").
func leadingSegment(target string) string {
	for _, p := range strings.Split(strings.TrimPrefix(target, "@"), "/") {
		if p != "" {
			return p
		}
	}
	return ""
}

// importCandidates extracts the identifier tokens an import target may name a
// repo by, most-significant first: e.g. "@app-web/lib-api" ->
// ["app-web", "lib-api"], "lib-core/foo" -> ["lib-core", "foo"].
func importCandidates(target string) []string {
	t := strings.TrimPrefix(target, "@")
	parts := strings.Split(t, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
