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

// RepoIdentity returns the PORTABLE identity of the repository a snapshot describes:
// its normalized remote when it has one, otherwise its checkout directory name. Empty
// only when a snapshot carries neither.
//
// It is the same two signals SameRepo compares, exposed as a value so a caller can
// RECORD an identity rather than only compare two snapshots that both happen to be in
// hand. Anything that persists a reference to a repository across machines needs that:
// an absolute path names where a checkout sits on one machine and identifies nothing
// anywhere else, so a stored path can never be reconciled with the same repository seen
// from a CI runner, from a second workstation, or from the same machine after the
// directory is moved.
//
// Deliberately NOT expressed as "the thing SameRepo compares": SameRepo answers true for
// a missing RepoPath on either side (nothing can be concluded, and inventing a mismatch
// would block a diff for no reason), which is the right answer to "are these the same?"
// and the wrong one to "what is this?".
func RepoIdentity(m SnapshotMeta) string {
	if r := remoteIdentity(m); r != "" {
		return r
	}
	return repoDirName(m.RepoPath)
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

// RepoDirName exposes repoDirName: what a repository's label was before it came from
// the remote, which a reader needs to interpret a snapshot that predates the recorded
// label (see diff.repoLabelOf).
func RepoDirName(path string) string { return repoDirName(path) }

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

// RepoLabel is the short name every fact extracted from a repository is tagged with,
// and it is part of the key a diff matches facts on (internal/diff.factKey). That makes
// it an IDENTITY question, not a display one: two snapshots of the same repository whose
// facts carry different labels share no keys at all, so the delta between them reports
// the entire graph as added and removed.
//
// It used to be `filepath.Base(repoPath)` alone, which answered that question with the
// name of a directory. A git worktree, a CI job that checks the base out beside the head,
// a second clone under another name — each produced a different label for the same code,
// while SameRepo (which prefers the remote) went on reporting them as the same repository.
// The two answers disagreed, and the gate graded a delta in which nothing matched.
//
// So the label now comes from the same signal SameRepo trusts first: the repository NAME
// from the normalized remote — the last segment of "github.com/enola-labs/enola" — falling
// back to the checkout directory name when there is no remote to read. Short and human
// either way; the full identity stays in RepoIdentity, which is what gets compared.
func RepoLabel(remote, repoPath string) string {
	if name := repoNameFromRemote(remote); name != "" {
		return name
	}
	return repoDirName(repoPath)
}

// repoNameFromRemote is the last path segment of a normalized remote: the repository's
// own name, without its host or owner. Empty when the remote is absent or degenerate —
// a remote normalizing to a bare host has no repository name in it, and inventing one
// from the host would label every such repo identically.
func repoNameFromRemote(remote string) string {
	id := NormalizeRemote(remote)
	if id == "" {
		return ""
	}
	i := strings.LastIndex(id, "/")
	if i < 0 {
		// No path at all — a host, or something that did not parse as a URL. Not a name.
		return ""
	}
	return id[i+1:]
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
