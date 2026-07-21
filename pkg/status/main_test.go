package status

import (
	"os"
	"testing"
)

// TestMain sandboxes the home directory for the entire package test binary, so
// no test can ever write usage files into the developer's real ~/.enola/usage/.
// Individual tests may still call t.Setenv("HOME", ...) to point at their own
// temp dir; this is a backstop for any that forget.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "enola-status-test-home-")
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
