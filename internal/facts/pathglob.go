package facts

import (
	"path/filepath"
	"slices"
	"strings"
)

// matchAnyGlob reports whether a forward-slash path matches any of the patterns.
// It is the single matcher behind both the ignore list and the test globs, so a
// file the two lists disagree about cannot exist: an ignored file that stops being
// a test necessarily stops being ignored.
func MatchAnyGlob(relPath string, patterns []string) bool {
	_, ok := MatchGlob(relPath, patterns)
	return ok
}

// matchGlob returns the first pattern that matches relPath. The receipt records it
// beside the skipped path, so "why is this file missing from the graph?" is a
// lookup rather than an investigation. Supported forms:
//
//	vendor/**                 anchored directory prefix
//	**/build/**               a directory named "build" at any depth
//	**/*.Tests/**             a directory whose NAME matches a glob, at any depth
//	**/*_test.go              a basename glob at any depth
//	**/spec/**/*_spec.rb      a basename glob under a directory named "spec"
//	**/*.Tests/**/*.cs        a basename glob under a glob-named directory
//
// The last form is the only one that constrains directory and filename together;
// see matchDirScopedGlob for why the Ruby test globs need it.
//
// The directory segment may itself be a glob. A literal is the common case and is
// unaffected — filepath.Match on a pattern with no metacharacters is equality — but
// .NET needs the general form: the dominant solution layout puts a test project in
// `MyApp.Tests/` beside `MyApp/` rather than under a `tests/` directory, so no
// literal segment names it.
func MatchGlob(relPath string, patterns []string) (string, bool) {
	for _, pattern := range patterns {
		// "<prefix>/**/<fileglob>". Handled first and exclusively: the branches
		// below would match such a pattern only when exactly one directory sits
		// between prefix and file, which is an artifact of filepath.Match reading
		// "**" as "*", not a rule anyone intended.
		if i := strings.Index(pattern, "/**/"); i >= 0 {
			prefix, fileGlob := pattern[:i], pattern[i+len("/**/"):]
			if !strings.Contains(fileGlob, "/") {
				if matchDirScopedGlob(relPath, prefix, fileGlob) {
					return pattern, true
				}
				continue
			}
		}
		if strings.HasPrefix(pattern, "**/") && strings.HasSuffix(pattern, "/**") {
			seg := strings.TrimSuffix(strings.TrimPrefix(pattern, "**/"), "/**")
			if seg != "" && !strings.Contains(seg, "/") {
				for _, part := range strings.Split(relPath, "/") {
					if matchSegment(seg, part) {
						return pattern, true
					}
				}
			}
		}
		if strings.HasSuffix(pattern, "/**") {
			dirPrefix := strings.TrimSuffix(pattern, "/**")
			if relPath == dirPrefix || strings.HasPrefix(relPath, dirPrefix+"/") {
				return pattern, true
			}
		}
		if m, err := filepath.Match(pattern, relPath); err == nil && m {
			return pattern, true
		}
		if strings.HasPrefix(pattern, "**/") {
			sub := strings.TrimPrefix(pattern, "**/")
			if m, err := filepath.Match(sub, filepath.Base(relPath)); err == nil && m {
				return pattern, true
			}
			if m, err := filepath.Match(sub, relPath); err == nil && m {
				return pattern, true
			}
		}
	}
	return "", false
}

// matchSegment compares one path segment against a pattern segment. A pattern
// with no metacharacters costs an equality check inside filepath.Match, so the
// literal case — every pattern that existed before .NET — behaves exactly as it
// did. An unparseable pattern yields ErrBadPattern and therefore no match, which
// is the safe direction: a malformed ignore entry hides nothing.
func matchSegment(pattern, part string) bool {
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern == part
	}
	m, err := filepath.Match(pattern, part)
	return err == nil && m
}

// matchDirScopedGlob reports whether relPath's basename matches fileGlob AND
// prefix names one of its ancestor directories ("**/<seg>" for a segment at any
// depth, otherwise an anchored literal path).
//
// A filename alone cannot classify a Ruby test. `lib/foo_test.rb` is one and
// `app/jobs/cache_warmup_ab_test.rb` is a production A/B-test job, yet both end in
// the token `test`; matching on the suffix deleted the latter from the graph
// entirely. Ruby settles it by convention — RSpec requires spec/, Minitest defaults
// to test/ — so the directory segment is the signal, and this predicate lets a
// single pattern demand both halves.
//
// Because every element of dirSegs is by construction an ancestor of the basename,
// segment equality alone places the file under the directory: no depth bookkeeping,
// and "spec/user_spec.rb" (zero intervening directories) falls out for free.
func matchDirScopedGlob(relPath, prefix, fileGlob string) bool {
	segs := strings.Split(relPath, "/")
	if len(segs) < 2 {
		return false // no directory component, so no prefix can name an ancestor
	}
	dirSegs, base := segs[:len(segs)-1], segs[len(segs)-1]

	if m, err := filepath.Match(fileGlob, base); err != nil || !m {
		return false
	}
	if seg, ok := strings.CutPrefix(prefix, "**/"); ok {
		if seg == "" || strings.Contains(seg, "/") {
			return false
		}
		return slices.ContainsFunc(dirSegs, func(part string) bool {
			return matchSegment(seg, part)
		})
	}
	if prefix == "**" {
		return true // any directory
	}
	return strings.HasPrefix(relPath, prefix+"/")
}
