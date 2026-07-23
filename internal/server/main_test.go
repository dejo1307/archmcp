package server

import (
	"os"
	"testing"
)

// TestMain sandboxes the home directory for the entire package test binary. The
// generate_snapshot handler writes the machine-wide and per-workspace graph
// receipts under ~/.enola, so without this a test run would overwrite the
// developer's own receipts with temp-dir paths — and a real enola server started
// afterwards would read them.
//
// Individual tests may still call t.Setenv("HOME", ...) for their own temp dir;
// this is the backstop for any that do not.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "enola-server-test-home-")
	if err != nil {
		panic(err)
	}
	for _, key := range []string{
		"HOME",        // unix/darwin: os.UserHomeDir reads $HOME
		"USERPROFILE", // windows
	} {
		if err := os.Setenv(key, tmp); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}
