package facts

import "strings"

// NormalizeRemote reduces a git remote URL to a comparable repository identity:
// host plus path, lowercased, with the scheme, any credentials, any port and a trailing
// ".git" removed. "" for input it cannot make sense of.
//
//	git@github.com:org/repo.git            -> github.com/org/repo
//	https://github.com/org/repo.git        -> github.com/org/repo
//	https://x-token:abc@github.com/org/repo -> github.com/org/repo
//	ssh://git@github.com:22/org/repo       -> github.com/org/repo
//
// The point is that the SAME repository must normalize identically however it was
// cloned — a CI runner using an HTTPS URL with a token and a developer using SSH are
// looking at one repository, and a comparison that says otherwise would decline to grade
// every CI diff.
//
// Lowercasing the path as well as the host is deliberate. Hosts are case-insensitive by
// spec; repository paths are case-insensitive on the major forges, so "Org/Repo" and
// "org/repo" are one repository, and treating them as two would produce exactly the
// false mismatch this function exists to prevent. The cost is that a forge which does
// distinguish them would see two repositories as one — a far rarer situation, and one
// that fails toward comparing rather than toward refusing.
func NormalizeRemote(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// scheme://[user[:pass]@]host[:port]/path
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if at := strings.LastIndex(s, "@"); at >= 0 {
			s = s[at+1:]
		}
		// Strip a port, but only from the host segment — a path may legitimately
		// contain a colon.
		if slash := strings.Index(s, "/"); slash >= 0 {
			host, path := s[:slash], s[slash:]
			if c := strings.LastIndex(host, ":"); c >= 0 {
				host = host[:c]
			}
			s = host + path
		}
	} else if at := strings.LastIndex(s, "@"); at >= 0 {
		// scp-like: [user@]host:path — the colon separates host from path, and unlike
		// the URL form it is NOT a port, so it becomes the path separator.
		s = s[at+1:]
		if c := strings.Index(s, ":"); c >= 0 {
			s = s[:c] + "/" + s[c+1:]
		}
	}

	s = strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
	s = strings.TrimRight(s, "/")
	return strings.ToLower(s)
}

// SameRepo reports whether two snapshots describe the same repository, without relying
// on the absolute path they were taken at.
//
// A baseline pinned on a CI runner at /home/runner/work/app/app and a working copy at
// /Users/dev/src/app are the same repository, and comparing absolute paths said they were
// not — which made every restored baseline artifact decline to grade.
//
// Two signals, strongest first:
//
//  1. Normalized remotes, when BOTH sides have one. This is a real identity: it survives
//     relocation, re-cloning and a shallow checkout.
//  2. Otherwise the checkout directory name. Weaker — two unrelated repositories both
//     checked out as "api" look alike — but it is what is available for a repo with no
//     remote, and it is the case the absolute-path comparison already got wrong.
//
// A missing RepoPath on either side yields true: nothing can be concluded, and inventing
// a mismatch out of absent data would block a diff for no reason.
func SameRepo(a, b SnapshotMeta) bool {
	if ra, rb := remoteIdentity(a), remoteIdentity(b); ra != "" && rb != "" {
		return ra == rb
	}
	if a.RepoPath == "" || b.RepoPath == "" {
		return true
	}
	return repoDirName(a.RepoPath) == repoDirName(b.RepoPath)
}

// remoteIdentity returns the normalized remote recorded on a snapshot, if any.
func remoteIdentity(m SnapshotMeta) string {
	if m.Git == nil {
		return ""
	}
	// Normalized at capture, but normalize again: a snapshot written by an older build
	// carries the raw URL, and a baseline outliving the build that wrote it is the whole
	// point of a portable artifact.
	return NormalizeRemote(m.Git.Remote)
}

// repoDirName is the last path segment of a repository path, accepting either separator.
// filepath.Base alone is not enough here: a baseline pinned on a Linux CI runner and read
// on a Windows workstation (or the reverse) carries the separator of the machine that
// WROTE it, and filepath.Base uses the separator of the machine reading it — so one of
// the two directions would compare a whole path against a single segment and always
// report a mismatch.
func repoDirName(path string) string {
	p := strings.TrimRight(path, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// NormalizeRepoLabel lowercases a repo label and strips '-' and '_', so "app-web",
// "app_web" and "AppWeb" all compare equal.
//
// It lives here rather than in the linker because two callers need it that cannot share
// a linker-internal helper: the cross-repo signals, which resolve an import scope or a
// topic prefix to a loaded repo, and the standalone unused-route passes, which run from
// a binder with no linker context at all.
func NormalizeRepoLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}
