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
