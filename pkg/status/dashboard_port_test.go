package status

import (
	"testing"
	"time"
)

// TestDashboardPortRoundTrip verifies the dashboard port set on the tracker is
// persisted via PersistStartup and resurfaces through AggregateServer/
// ServerSnapshot — the path that lets a separate --status process print the URL.
func TestDashboardPortRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tr := NewTracker("/tmp/example-repo")
	tr.SetStartTime(time.Now())
	tr.SetDashboardPort(45678)
	tr.PersistStartup()

	dir, err := usageDir()
	if err != nil {
		t.Fatalf("usageDir: %v", err)
	}

	ss := AggregateServer(dir)
	if !ss.Found {
		t.Fatal("AggregateServer.Found = false, want true (PersistStartup should have written a file)")
	}
	if ss.DashboardPort != 45678 {
		t.Errorf("AggregateServer.DashboardPort = %d, want 45678", ss.DashboardPort)
	}
	if !ss.Alive {
		t.Error("AggregateServer.Alive = false, want true (current process PID)")
	}

	// ServerSnapshot resolves usageDir itself and must agree.
	if got := ServerSnapshot().DashboardPort; got != 45678 {
		t.Errorf("ServerSnapshot.DashboardPort = %d, want 45678", got)
	}
}
