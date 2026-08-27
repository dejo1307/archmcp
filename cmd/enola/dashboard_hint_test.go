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
	if got := out.String(); got != "\nExplore this snapshot in your browser:\n  enola dashboard --open \"../repo\"\nIt starts in the background; stop it later with: enola dashboard stop\n" {
		t.Fatalf("hint = %q", got)
	}

	out.Reset()
	printDashboardHint(&out, "", "mcp-arch.yaml")
	if got := out.String(); got != "\nExplore this snapshot in your browser:\n  enola dashboard --open\nIt starts in the background; stop it later with: enola dashboard stop\n" {
		t.Fatalf("default hint = %q", got)
	}
}
