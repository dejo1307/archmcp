package facts

import "testing"

// TestNormalizeRemote is the table the whole portable-baseline story rests on: the SAME
// repository must normalize identically however it was cloned. A CI runner using an HTTPS
// URL with an injected token and a developer using SSH are looking at one repository, and
// a normalizer that disagreed would decline to grade every CI diff.
func TestNormalizeRemote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"scp-like ssh", "git@github.com:org/repo.git", "github.com/org/repo"},
		{"scp-like without .git", "git@github.com:org/repo", "github.com/org/repo"},
		{"https", "https://github.com/org/repo.git", "github.com/org/repo"},
		{"https without .git", "https://github.com/org/repo", "github.com/org/repo"},
		{"http", "http://github.com/org/repo.git", "github.com/org/repo"},
		{"ssh url", "ssh://git@github.com/org/repo.git", "github.com/org/repo"},
		{"ssh url with port", "ssh://git@github.com:22/org/repo.git", "github.com/org/repo"},
		{"https with token", "https://x-access-token:ghs_abc123@github.com/org/repo.git", "github.com/org/repo"},
		{"https with user:pass", "https://user:pa55@gitlab.com/grp/sub/repo.git", "gitlab.com/grp/sub/repo"},
		{"uppercase host and path", "https://GitHub.com/Org/Repo.git", "github.com/org/repo"},
		{"trailing slash", "https://github.com/org/repo/", "github.com/org/repo"},
		{"nested group path", "git@gitlab.com:grp/sub/repo.git", "gitlab.com/grp/sub/repo"},
		{"surrounding whitespace", "  git@github.com:org/repo.git\n", "github.com/org/repo"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"local path remote", "/srv/git/repo.git", "/srv/git/repo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeRemote(tc.in); got != tc.want {
				t.Errorf("NormalizeRemote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalizeRemote_CloneFormsAgree is the property that actually matters, stated
// directly: every way of cloning one repository collapses to one identity.
func TestNormalizeRemote_CloneFormsAgree(t *testing.T) {
	forms := []string{
		"git@github.com:org/repo.git",
		"https://github.com/org/repo.git",
		"https://github.com/org/repo",
		"ssh://git@github.com:22/org/repo.git",
		"https://x-access-token:ghs_abc@github.com/org/repo.git",
		"https://GitHub.com/Org/Repo/",
	}
	want := NormalizeRemote(forms[0])
	for _, f := range forms[1:] {
		if got := NormalizeRemote(f); got != want {
			t.Errorf("clone form %q normalized to %q, want %q — the same repo must have one identity", f, got, want)
		}
	}
}

// TestNormalizeRemote_DistinctReposStayDistinct — collapsing everything to one identity
// would be worse than the bug being fixed: unrelated repositories would compare as the
// same and the gate would diff two unrelated graphs.
func TestNormalizeRemote_DistinctReposStayDistinct(t *testing.T) {
	distinct := []string{
		"git@github.com:org/repo.git",
		"git@github.com:org/other.git",
		"git@github.com:otherorg/repo.git",
		"git@gitlab.com:org/repo.git",
	}
	seen := map[string]string{}
	for _, r := range distinct {
		id := NormalizeRemote(r)
		if prev, dup := seen[id]; dup {
			t.Errorf("%q and %q both normalized to %q", prev, r, id)
		}
		seen[id] = r
	}
}

func metaAt(path, remote string) SnapshotMeta {
	m := SnapshotMeta{RepoPath: path}
	if remote != "" {
		m.Git = &GitInfo{Remote: remote}
	}
	return m
}

// TestSameRepo covers the decision itself. The first case is the bug this phase exists to
// fix: a baseline pinned on a CI runner and a working copy on a laptop are one repository.
func TestSameRepo(t *testing.T) {
	cases := []struct {
		name string
		a, b SnapshotMeta
		want bool
	}{
		{
			"same repo, different absolute paths, matching remotes",
			metaAt("/home/runner/work/app/app", "git@github.com:org/app.git"),
			metaAt("/Users/dev/src/app", "https://github.com/org/app.git"),
			true,
		},
		{
			"different remotes are different repos even at the same path",
			metaAt("/w/app", "git@github.com:org/app.git"),
			metaAt("/w/app", "git@github.com:org/other.git"),
			false,
		},
		{
			"no remotes: same directory name is treated as the same repo",
			metaAt("/home/runner/work/app/app", ""),
			metaAt("/Users/dev/src/app", ""),
			true,
		},
		{
			"no remotes: different directory names are different repos",
			metaAt("/w/app", ""),
			metaAt("/w/other", ""),
			false,
		},
		{
			"remote on one side only falls back to the directory name",
			metaAt("/home/runner/work/app/app", "git@github.com:org/app.git"),
			metaAt("/Users/dev/src/app", ""),
			true,
		},
		{
			"remote on one side only, names differ, so it is a mismatch",
			metaAt("/w/app", "git@github.com:org/app.git"),
			metaAt("/w/other", ""),
			false,
		},
		{
			"matching remotes outrank differing directory names",
			metaAt("/w/app", "git@github.com:org/app.git"),
			metaAt("/tmp/app-baseline-checkout", "git@github.com:org/app.git"),
			true,
		},
		{
			"an empty RepoPath concludes nothing rather than inventing a mismatch",
			metaAt("", ""),
			metaAt("/w/app", ""),
			true,
		},
		{"both empty", metaAt("", ""), metaAt("", ""), true},
		{
			"trailing separator does not create a mismatch",
			metaAt("/w/app/", ""),
			metaAt("/w/app", ""),
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameRepo(tc.a, tc.b); got != tc.want {
				t.Errorf("SameRepo = %v, want %v", got, tc.want)
			}
			// The relation must be symmetric: which snapshot is the baseline is an
			// accident of the caller, and a one-way verdict would make the gate's
			// behaviour depend on argument order.
			if got := SameRepo(tc.b, tc.a); got != tc.want {
				t.Errorf("SameRepo is not symmetric: reversed = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSameRepo_CrossPlatformSeparators — a baseline pinned on a Linux CI runner and read
// on a Windows workstation carries the separator of the machine that WROTE it, while
// filepath.Base would use the separator of the machine READING it. One direction would
// then compare a whole path against a single segment and always report a mismatch.
func TestSameRepo_CrossPlatformSeparators(t *testing.T) {
	linux := metaAt("/home/runner/work/app/app", "")
	windows := metaAt(`C:\Users\dev\src\app`, "")

	if !SameRepo(linux, windows) {
		t.Error("a Linux-written baseline and a Windows working copy of the same repo must compare as the same")
	}
	if SameRepo(linux, metaAt(`C:\Users\dev\src\other`, "")) {
		t.Error("different repo names across platforms must still be a mismatch")
	}
}

// TestSameRepo_NormalizesLegacyRawRemotes — a snapshot written before remotes were
// normalized at capture carries the raw URL. A baseline outliving the build that wrote it
// is the entire point of a portable artifact, so identity must not depend on the writer.
func TestSameRepo_NormalizesLegacyRawRemotes(t *testing.T) {
	legacy := metaAt("/old/app", "git@github.com:Org/App.git") // raw, as an older build stored it
	current := metaAt("/new/app", "github.com/org/app")        // normalized at capture

	if !SameRepo(legacy, current) {
		t.Error("a raw remote from an older snapshot must normalize to the same identity as a current one")
	}
}
