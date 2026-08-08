package command

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/updatecheck"
	"github.com/enola-labs/enola/internal/version"
	"github.com/enola-labs/enola/pkg/cli"
)

// seedUpdateCache isolates HOME and optionally plants a manifest advertising a newer
// release. Pass "" for latest to leave the cache absent, which is the state a fresh
// install — and every offline machine — stays in.
func seedUpdateCache(t *testing.T, latest, extractorVersion string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(updatecheck.DisableEnv, "")
	t.Setenv("CI", "")

	prev := version.Version
	version.Version = "0.3.2"
	t.Cleanup(func() { version.Version = prev })

	if latest == "" {
		return
	}
	dir := filepath.Join(home, ".enola")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"checked_at":        time.Now().UTC(),
		"version":           latest,
		"extractor_version": extractorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "update.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// runDoctorCapturing runs `doctor` with the given args and returns what it wrote to
// stdout. Doctor reports rather than gates, so it returns normally and this is safe.
func runDoctorCapturing(t *testing.T, args ...string) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = prev }()

	New(cli.Binary{Name: "enola"}, "upgrade").Doctor(args)

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// `doctor` is the one place the update state is reported unconditionally, so its JSON
// has to carry it — a monitoring script that can see "hooks never fired" but not "this
// binary is nine releases behind" is missing half of what makes a setup unhealthy.
func TestDoctorJSONCarriesUpdateState(t *testing.T) {
	t.Run("update available", func(t *testing.T) {
		seedUpdateCache(t, "0.3.12", "v999")
		repo := t.TempDir()

		var got struct {
			Update updatecheck.Notice `json:"update"`
		}
		if err := json.Unmarshal([]byte(runDoctorCapturing(t, "--json", repo)), &got); err != nil {
			t.Fatalf("doctor --json is not valid JSON: %v", err)
		}
		if !got.Update.Available {
			t.Fatalf("update.available = false, want true: %+v", got.Update)
		}
		if got.Update.Latest != "0.3.12" || got.Update.Current != "0.3.2" {
			t.Errorf("update = %+v, want 0.3.2 -> 0.3.12", got.Update)
		}
		if !got.Update.ExtractorMoved {
			t.Error("update.extractor_moved = false, but the cached manifest reports different extractors")
		}
	})

	t.Run("nothing known", func(t *testing.T) {
		seedUpdateCache(t, "", "")
		repo := t.TempDir()

		var got map[string]any
		if err := json.Unmarshal([]byte(runDoctorCapturing(t, "--json", repo)), &got); err != nil {
			t.Fatalf("doctor --json is not valid JSON: %v", err)
		}
		// Present but false, rather than absent: a consumer must be able to tell "no
		// update" from "this build of doctor does not report updates".
		update, ok := got["update"].(map[string]any)
		if !ok {
			t.Fatalf("doctor --json has no update object: %v", got["update"])
		}
		if update["available"] != false {
			t.Errorf("update.available = %v with no cached manifest, want false", update["available"])
		}
	})
}

// The text report states the answer either way. "Up to date" is worth saying out loud
// in the command someone runs to ask whether their setup is healthy — leaving it to be
// inferred from silence is what makes people re-check by hand.
func TestDoctorTextReportsUpdates(t *testing.T) {
	t.Run("update available", func(t *testing.T) {
		seedUpdateCache(t, "0.3.12", "v999")
		out := runDoctorCapturing(t, t.TempDir())

		if !strings.Contains(out, "Updates") {
			t.Fatalf("doctor has no Updates section:\n%s", out)
		}
		if !strings.Contains(out, "v0.3.12 is available") {
			t.Errorf("Updates section does not name the available release:\n%s", out)
		}
		if !strings.Contains(out, "enola upgrade") {
			t.Errorf("Updates section does not say how to upgrade:\n%s", out)
		}
		if !strings.Contains(out, "Extractors changed since your build") {
			t.Errorf("Updates section omits the extractor escalation:\n%s", out)
		}
	})

	t.Run("up to date", func(t *testing.T) {
		seedUpdateCache(t, "0.3.2", "v999") // same version as the running build
		out := runDoctorCapturing(t, t.TempDir())

		if !strings.Contains(out, "up to date") {
			t.Errorf("Updates section does not state that the build is current:\n%s", out)
		}
		if strings.Contains(out, "is available") {
			t.Errorf("Updates section advertises an upgrade to the version already running:\n%s", out)
		}
	})

	// A dev build is ahead of the last release, not behind it. Saying "up to date"
	// would be a claim nothing checked, so it says what actually happened instead.
	t.Run("dev build", func(t *testing.T) {
		seedUpdateCache(t, "0.3.12", "v999")
		version.Version = "dev"

		out := runDoctorCapturing(t, t.TempDir())
		if !strings.Contains(out, "not checked") {
			t.Errorf("Updates section does not explain why a dev build has no answer:\n%s", out)
		}
		if strings.Contains(out, "is available") {
			t.Errorf("told a dev build to upgrade to a release it is ahead of:\n%s", out)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		seedUpdateCache(t, "0.3.12", "v999")
		t.Setenv(updatecheck.DisableEnv, "1")

		out := runDoctorCapturing(t, t.TempDir())
		if !strings.Contains(out, "not checked") {
			t.Errorf("Updates section does not report that checks are disabled:\n%s", out)
		}
		if strings.Contains(out, "is available") {
			t.Errorf("reported an update despite %s being set:\n%s", updatecheck.DisableEnv, out)
		}
	})
}
