package command

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, repo, body string) {
	t.Helper()
	dir := filepath.Join(repo, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHooksConfigured(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			// The shape enola writes today.
			name: "matcher-grouped with marker",
			body: `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"enola hook stop","source":"enola"}]}]}}`,
			want: true,
		},
		{
			// The broken shape older installs left behind. It must still be RECOGNISED —
			// `doctor` reporting "not configured" for a repo that is configured, badly,
			// would hide exactly the case it exists to surface.
			name: "legacy flat entry with marker",
			body: `{"hooks":{"Stop":[{"type":"command","command":"enola hook stop","source":"enola"}]}}`,
			want: true,
		},
		{
			name: "someone else's hooks only",
			body: `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine.sh"}]}]}}`,
			want: false,
		},
		{name: "no hooks key", body: `{"permissions":{"allow":[]}}`, want: false},
		{name: "not json", body: `{`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			writeSettings(t, repo, tt.body)
			if got := hooksConfigured(repo); got != tt.want {
				t.Errorf("hooksConfigured = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHooksConfigured_NoSettingsFile(t *testing.T) {
	if hooksConfigured(t.TempDir()) {
		t.Error("a repository with no .claude/settings.json is not configured")
	}
}

// hookOutputDir is what tells doctor and install where the heartbeat lives; if it
// disagreed with the engine the report would read a file nothing writes.
func TestHookOutputDir(t *testing.T) {
	repo := t.TempDir()
	if got, want := hookOutputDir(repo), filepath.Join(repo, ".enola"); got != want {
		t.Errorf("default output dir = %q, want %q", got, want)
	}

	cfg := "repo: \".\"\noutput:\n  dir: \"custom-out\"\n"
	if err := os.WriteFile(filepath.Join(repo, "mcp-arch.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := hookOutputDir(repo), filepath.Join(repo, "custom-out"); got != want {
		t.Errorf("configured output dir = %q, want %q", got, want)
	}
}

func TestHookOutputDir_UnreadableConfigFallsBackToDefault(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "mcp-arch.yaml"), []byte("::: not yaml :::"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A diagnostic must never be the reason something breaks.
	if got, want := hookOutputDir(repo), filepath.Join(repo, ".enola"); got != want {
		t.Errorf("got %q, want the default %q", got, want)
	}
}
