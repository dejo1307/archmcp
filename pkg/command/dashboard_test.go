package command

import (
	"testing"

	"github.com/enola-labs/enola/pkg/status"
)

func TestDashboardInstancesExcludeMCPServers(t *testing.T) {
	instances := []status.Instance{
		{PID: 10, Binary: "enola"},
		{PID: 11, Binary: "enola dashboard"},
		{PID: 12, Binary: "enola-enterprise dashboard"},
	}
	got := dashboardInstances(instances)
	if len(got) != 2 || got[0].PID != 11 || got[1].PID != 12 {
		t.Fatalf("dashboardInstances() = %+v", got)
	}
}

func TestDashboardServingInstancesIncludesStandaloneAndMCP(t *testing.T) {
	instances := []status.Instance{
		{PID: 10, Binary: "enola", DashboardPort: 7171},
		{PID: 11, Binary: "enola dashboard", DashboardPort: 7172},
		{PID: 12, Binary: "enola"},
	}
	got := dashboardServingInstances(instances)
	if len(got) != 2 || got[0].PID != 10 || got[1].PID != 11 {
		t.Fatalf("dashboardServingInstances() = %+v", got)
	}
	if got := dashboardKind(instances[0]); got != "MCP server" {
		t.Fatalf("dashboardKind(MCP) = %q", got)
	}
	if got := dashboardKind(instances[1]); got != "standalone" {
		t.Fatalf("dashboardKind(standalone) = %q", got)
	}
}
