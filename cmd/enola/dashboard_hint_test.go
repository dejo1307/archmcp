package main

import (
	"bytes"
	"testing"
)

func TestDashboardHintTarget(t *testing.T) {
	if got := dashboardHintTarget("../repo", "cluster.yaml"); got != "../repo" {
		t.Fatalf("repo target = %q", got)
	}
	if got := dashboardHintTarget("", "cluster.yaml"); got != "cluster.yaml" {
		t.Fatalf("config target = %q", got)
	}
	if got := dashboardHintTarget("", "mcp-arch.yaml"); got != "" {
		t.Fatalf("default target = %q", got)
	}
}

func TestPrintDashboardHint(t *testing.T) {
	var out bytes.Buffer
	printDashboardHint(&out, "../repo", "mcp-arch.yaml")
	if got := out.String(); got != "\nExplore this snapshot: enola dashboard \"../repo\"\n" {
		t.Fatalf("hint = %q", got)
	}

	out.Reset()
	printDashboardHint(&out, "", "mcp-arch.yaml")
	if got := out.String(); got != "\nExplore this snapshot: enola dashboard\n" {
		t.Fatalf("default hint = %q", got)
	}
}
