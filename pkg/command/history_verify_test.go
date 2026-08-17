package command

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/pkg/history"
)

func problemReport(kind string) history.ShareVerifyReport {
	return history.ShareVerifyReport{
		Dir:       "/store",
		Sources:   []string{"machine-a"},
		Revisions: 3,
		Verified:  2,
		Problems: []history.ShareProblem{
			{Kind: kind, Source: "machine-a", ID: "sha256:deadbeef", Detail: "the " + kind + " detail"},
		},
	}
}

func TestVerifyExitCodeIsOneForEveryProblemKind(t *testing.T) {
	for _, kind := range []string{"chain-break", "gap", "tampered", "divergent", "damaged"} {
		if got := verifyExitCode(problemReport(kind)); got != 1 {
			t.Errorf("a %s problem must exit 1, got %d", kind, got)
		}
	}
}

func TestVerifyExitCodeIsZeroWhenClean(t *testing.T) {
	rep := history.ShareVerifyReport{Dir: "/store", Revisions: 3, Verified: 3}
	if got := verifyExitCode(rep); got != 0 {
		t.Errorf("a clean store must exit 0, got %d", got)
	}
}

func TestRenderVerifyLeadsWithTheProblems(t *testing.T) {
	out := renderVerify(problemReport("tampered"))
	if !strings.HasPrefix(out, "1 problem(s):") {
		t.Fatalf("problems must print first:\n%s", out)
	}
	problem := strings.Index(out, "the tampered detail")
	summary := strings.Index(out, "revisions  3")
	if problem < 0 || summary < 0 || problem > summary {
		t.Errorf("the summary precedes the problems it summarizes:\n%s", out)
	}
	if strings.Contains(out, "Every chain verifies") {
		t.Errorf("a broken store must not read as verified:\n%s", out)
	}
}

func TestRenderVerifyCleanKeepsTheVerdictLast(t *testing.T) {
	out := renderVerify(history.ShareVerifyReport{
		Dir: "/store", Sources: []string{"machine-a"}, Revisions: 3, Verified: 3,
	})
	if !strings.HasPrefix(out, "/store") {
		t.Fatalf("a clean report leads with the store:\n%s", out)
	}
	if !strings.HasSuffix(out, "Every chain verifies: no gaps, no tampering.\n") {
		t.Errorf("the clean verdict must close the report:\n%s", out)
	}
	if strings.Contains(out, "problem(s)") {
		t.Errorf("a clean report claims problems:\n%s", out)
	}
}
