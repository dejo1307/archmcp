package facts

import (
	"github.com/enola-labs/enola/internal/factpath"
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
//	**/src/test/**/*.scala    a basename glob under a directory PATH at any depth
//
// The last two are the only forms that constrain directory and filename together;
// see matchDirScopedGlob for why the Ruby test globs need it and why Scala's need
// two directory segments rather than one.
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
		if m, err := factpath.Match(pattern, relPath); err == nil && m {
			return pattern, true
		}
		if strings.HasPrefix(pattern, "**/") {
			sub := strings.TrimPrefix(pattern, "**/")
			if m, err := factpath.Match(sub, filepath.Base(relPath)); err == nil && m {
				return pattern, true
			}
			if m, err := factpath.Match(sub, relPath); err == nil && m {
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
	m, err := factpath.Match(pattern, part)
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
// A prefix after "**/" may name SEVERAL consecutive segments, which Scala needs and
// a single segment cannot express. sbt puts test sources under `src/test/`, but a
// directory merely NAMED `test` is routinely production: zio's `test-magnolia`
// module compiles `src/main/scala-3/zio/test/magnolia/*.scala`, and across the
// benchmark corpus a one-segment `**/test/**/*.scala` would have deleted 183
// production files — 175 of them ZIO's own test LIBRARY, whose package is literally
// `zio.test`. Demanding the pair `src/test` distinguishes the source set from the
// name, and leaves every existing one-segment pattern behaving exactly as before
// (a single segment is the length-1 case of the same scan).
//
// Because every element of dirSegs is by construction an ancestor of the basename,
// segment matching alone places the file under the directory: no depth bookkeeping,
// and "spec/user_spec.rb" (zero intervening directories) falls out for free.
func matchDirScopedGlob(relPath, prefix, fileGlob string) bool {
	segs := strings.Split(relPath, "/")
	if len(segs) < 2 {
		return false // no directory component, so no prefix can name an ancestor
	}
	dirSegs, base := segs[:len(segs)-1], segs[len(segs)-1]

	if m, err := factpath.Match(fileGlob, base); err != nil || !m {
		return false
	}
	if seg, ok := strings.CutPrefix(prefix, "**/"); ok {
		if seg == "" {
			return false
		}
		want := strings.Split(seg, "/")
		if len(want) == 1 {
			return slices.ContainsFunc(dirSegs, func(part string) bool {
				return matchSegment(want[0], part)
			})
		}
		return containsSegmentRun(dirSegs, want)
	}
	if prefix == "**" {
		return true // any directory
	}
	return strings.HasPrefix(relPath, prefix+"/")
}

// containsSegmentRun reports whether want appears as a run of CONSECUTIVE segments
// anywhere in dirSegs. Consecutive rather than merely present in order: `src/test`
// must mean a test source set, not a `src` directory that happens to have a `test`
// somewhere beneath it — which is the distinction that keeps zio's
// `src/main/scala-3/zio/test/` out of the test globs.
func containsSegmentRun(dirSegs, want []string) bool {
	if len(want) == 0 || len(want) > len(dirSegs) {
		return false
	}
	for i := 0; i+len(want) <= len(dirSegs); i++ {
		matched := true
		for j, w := range want {
			if !matchSegment(w, dirSegs[i+j]) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
