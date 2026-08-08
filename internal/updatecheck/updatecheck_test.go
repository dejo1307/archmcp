package updatecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/version"
)

// isolate points HOME at a scratch directory and gives the process a non-dev version,
// returning the cache path.
//
// Every test needs both. HOME because this package writes ~/.enola/update.json, and a
// test that skipped this would read and overwrite the running developer's real cache —
// making the suite's result depend on when they last ran a session, and silently
// changing what their own installation reports. Version because Suppressed() switches
// the whole package off for a "dev" build, which is what a test binary is.
func isolate(t *testing.T, ver string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv(DisableEnv, "")
	t.Setenv("CI", "")

	prev := version.Version
	version.Version = ver
	t.Cleanup(func() { version.Version = prev })

	return filepath.Join(home, dirName, fileName)
}

// serve stands up a manifest server and points the package at it for the test.
func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	prev := manifestURL
	manifestURL = srv.URL
	t.Cleanup(func() { manifestURL = prev })
	return srv
}

// seed writes a cache file directly, standing in for a Refresh that already happened.
func seed(t *testing.T, path string, s state) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestRefreshWritesCache(t *testing.T) {
	path := isolate(t, "0.3.2")
	serve(t, `{"version":"0.3.12","extractor_version":"v193"}`)

	Refresh(context.Background())

	s, err := read(path)
	if err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	if s.Version != "0.3.12" || s.ExtractorVersion != "v193" {
		t.Fatalf("cache = %+v, want version 0.3.12 / extractor v193", s)
	}
	if s.CheckedAt.IsZero() {
		t.Error("CheckedAt not stamped, so the TTL can never expire and every run would refetch")
	}
}

// The TTL is the whole reason this is cheap enough to call on every session start.
func TestRefreshHonoursTTL(t *testing.T) {
	path := isolate(t, "0.3.2")

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"version":"0.9.9"}`))
	}))
	defer srv.Close()
	prev := manifestURL
	manifestURL = srv.URL
	defer func() { manifestURL = prev }()

	seed(t, path, state{
		CheckedAt: time.Now().UTC().Add(-time.Hour),
		Manifest:  Manifest{Version: "0.3.5"},
	})

	Refresh(context.Background())
	if hits != 0 {
		t.Fatalf("fetched %d time(s) within the TTL; the check must not hit the network again", hits)
	}

	// Past the TTL it must fetch again, or the notice would freeze at whatever the
	// first check happened to find.
	seed(t, path, state{
		CheckedAt: time.Now().UTC().Add(-ttl - time.Minute),
		Manifest:  Manifest{Version: "0.3.5"},
	})
	Refresh(context.Background())
	if hits != 1 {
		t.Fatalf("fetched %d time(s) after the TTL expired, want 1", hits)
	}
}

// A failed fetch must leave the previous answer alone. Overwriting it with nothing
// would turn one bad request into a silence that lasts a full TTL.
func TestRefreshKeepsCacheOnFailure(t *testing.T) {
	path := isolate(t, "0.3.2")
	seed(t, path, state{
		CheckedAt: time.Now().UTC().Add(-ttl - time.Minute),
		Manifest:  Manifest{Version: "0.3.12", ExtractorVersion: "v193"},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	prev := manifestURL
	manifestURL = srv.URL
	defer func() { manifestURL = prev }()

	Refresh(context.Background())

	s, err := read(path)
	if err != nil || s.Version != "0.3.12" {
		t.Fatalf("cache = %+v (err %v), want the previous answer preserved", s, err)
	}
}

// A manifest that decodes but says nothing useful must be rejected at the door, not
// cached and then puzzled over on every read.
func TestRefreshRejectsManifestWithoutVersion(t *testing.T) {
	path := isolate(t, "0.3.2")
	serve(t, `{"extractor_version":"v193"}`)

	Refresh(context.Background())

	if _, err := read(path); err == nil {
		t.Error("cached a manifest with no version")
	}
}

func TestRefreshSuppressed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(t *testing.T)
	}{
		{"opt-out", func(t *testing.T) { t.Setenv(DisableEnv, "1") }},
		{"CI", func(t *testing.T) { t.Setenv("CI", "true") }},
		{"dev build", func(t *testing.T) { version.Version = "dev" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := isolate(t, "0.3.2")
			var hits int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
				_, _ = w.Write([]byte(`{"version":"0.9.9"}`))
			}))
			defer srv.Close()
			prev := manifestURL
			manifestURL = srv.URL
			defer func() { manifestURL = prev }()

			tc.apply(t)

			Refresh(context.Background())
			if hits != 0 {
				t.Errorf("made %d request(s) while suppressed", hits)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("wrote a cache file while suppressed")
			}
			if HumanLine("v1") != "" || AgentLine("v1") != "" {
				t.Error("produced a notice while suppressed")
			}
		})
	}
}

// Concurrent refreshes must collapse to one request. Several agent terminals starting
// at once is the normal case, not an edge case.
func TestRefreshIsSingleFlight(t *testing.T) {
	isolate(t, "0.3.2")

	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond) // hold the lock long enough for the others to arrive
		_, _ = w.Write([]byte(`{"version":"0.3.12","extractor_version":"v193"}`))
	}))
	defer srv.Close()
	prev := manifestURL
	manifestURL = srv.URL
	defer func() { manifestURL = prev }()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Refresh(context.Background())
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Errorf("made %d requests concurrently, want 1", hits)
	}
}

func TestForReadsCache(t *testing.T) {
	cases := []struct {
		name             string
		current          string
		cached           Manifest
		extractorVersion string
		want             Notice
	}{
		{
			name:             "newer release, extractors moved",
			current:          "0.3.2",
			cached:           Manifest{Version: "0.3.12", ExtractorVersion: "v193"},
			extractorVersion: "v180",
			want:             Notice{Available: true, Current: "0.3.2", Latest: "0.3.12", ExtractorMoved: true},
		},
		{
			name:             "newer release, extractors unchanged",
			current:          "0.3.11",
			cached:           Manifest{Version: "0.3.12", ExtractorVersion: "v193"},
			extractorVersion: "v193",
			want:             Notice{Available: true, Current: "0.3.11", Latest: "0.3.12"},
		},
		{
			name:             "up to date",
			current:          "0.3.12",
			cached:           Manifest{Version: "0.3.12", ExtractorVersion: "v193"},
			extractorVersion: "v193",
			want:             Notice{},
		},
		{
			// Possible with a manual install of an unreleased build; must not nag.
			name:             "ahead of the latest release",
			current:          "0.4.0",
			cached:           Manifest{Version: "0.3.12"},
			extractorVersion: "v193",
			want:             Notice{},
		},
		{
			// The escalation is a claim about data. Without both sides it is unproven,
			// and an unproven escalation must not be made.
			name:             "extractor version unknown locally",
			current:          "0.3.2",
			cached:           Manifest{Version: "0.3.12", ExtractorVersion: "v193"},
			extractorVersion: "",
			want:             Notice{Available: true, Current: "0.3.2", Latest: "0.3.12"},
		},
		{
			name:             "extractor version absent from manifest",
			current:          "0.3.2",
			cached:           Manifest{Version: "0.3.12"},
			extractorVersion: "v180",
			want:             Notice{Available: true, Current: "0.3.2", Latest: "0.3.12"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := isolate(t, tc.current)
			seed(t, path, state{CheckedAt: time.Now().UTC(), Manifest: tc.cached})

			if got := For(tc.extractorVersion); got != tc.want {
				t.Errorf("For() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// No cache and an unreadable cache are the states a fresh install and a disk problem
// leave behind. Both must read as "nothing to say", never as an error or a notice.
func TestForWithoutUsableCache(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		isolate(t, "0.3.2")
		if got := For("v193"); got.Available {
			t.Errorf("For() = %+v with no cache, want silence", got)
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		path := isolate(t, "0.3.2")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := For("v193"); got.Available {
			t.Errorf("For() = %+v with a corrupt cache, want silence", got)
		}
	})
}

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.3.2", "0.3.12", true},   // not a string comparison: "0.3.12" < "0.3.2" lexically
		{"0.3.12", "0.3.2", false},  // and the reverse must not fire either
		{"0.3.12", "0.3.12", false},
		{"0.9.9", "1.0.0", true},
		{"1.0.0", "0.9.9", false},
		{"0.3.2", "0.4.0", true},

		// Fails closed: an unparseable version on either side yields no notice, because
		// the alternative is telling somebody forever to upgrade to something they
		// cannot reach.
		{"dev", "0.3.12", false},
		{"0.3.2", "", false},
		{"0.3.2", "latest", false},
		{"0.3.2", "0.3", false},
		{"0.3.2", "0.3.12.1", false},
		{"0.3.2", "0.3.13-rc1", false},
		{"0.3.2", "0.3.x", false},

		// A "v" prefix on either side is tolerated; the tags carry one and the manifest
		// does not.
		{"v0.3.2", "v0.3.12", true},
	}
	for _, tc := range cases {
		if got := newer(tc.current, tc.latest); got != tc.want {
			t.Errorf("newer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

// THE INVARIANT THE TWO SURFACES EXIST TO PROTECT.
//
// The human is at a shell and typed a command, so their line ends in an imperative. The
// agent is mid-task inside somebody else's session, and an agent that reads "run enola
// upgrade" will run it: on a machine it was not asked to modify, achieving nothing
// visible, because replacing the binary leaves the already-running server on the old
// inode.
//
// "The agent string currently doesn't say run" is not a property that survives editing
// unless something enforces it, and the two strings sit next to each other in a file
// where copying one into the other is the obvious edit. Hence this.
func TestAgentLineNeverTellsTheAgentToUpgrade(t *testing.T) {
	path := isolate(t, "0.3.2")
	seed(t, path, state{
		CheckedAt: time.Now().UTC(),
		Manifest:  Manifest{Version: "0.3.12", ExtractorVersion: "v193"},
	})

	agent := AgentLine("v180")
	if agent == "" {
		t.Fatal("expected a notice")
	}
	for _, imperative := range []string{
		"run `enola upgrade`",
		"run enola upgrade",
		"`enola upgrade`",
		"enola upgrade",
	} {
		if strings.Contains(strings.ToLower(agent), imperative) {
			t.Errorf("agent notice names the upgrade command (%q), which an agent will act on:\n%s", imperative, agent)
		}
	}
	if !strings.Contains(agent, "Mention this to the user") {
		t.Errorf("agent notice does not hand the decision to the user:\n%s", agent)
	}
	if !strings.Contains(agent, "Do not upgrade enola yourself") {
		t.Errorf("agent notice does not tell the agent to keep its hands off:\n%s", agent)
	}

	// The human half must keep the imperative, or the split has quietly collapsed the
	// other way and nobody is being told what to do.
	human := HumanLine("v180")
	if !strings.Contains(human, "Run `enola upgrade`.") {
		t.Errorf("human notice lost its imperative:\n%s", human)
	}
	if !strings.Contains(human, "extractors changed") {
		t.Errorf("human notice dropped the extractor escalation:\n%s", human)
	}

	// …and drops that clause when the extractors did not move, so the strong claim is
	// only ever made when it is true.
	if got := HumanLine("v193"); strings.Contains(got, "extractors changed") {
		t.Errorf("human notice claims the extractors moved when they did not:\n%s", got)
	}
	if got := AgentLine("v193"); strings.Contains(got, "Extractors changed") {
		t.Errorf("agent notice claims the extractors moved when they did not:\n%s", got)
	}
}

func TestFprintWritesNothingWhenSilent(t *testing.T) {
	isolate(t, "0.3.2") // no cache seeded

	var sb strings.Builder
	Fprint(&sb, "v193")
	if sb.String() != "" {
		t.Errorf("Fprint wrote %q with nothing to report; callers rely on it being a no-op", sb.String())
	}
}
