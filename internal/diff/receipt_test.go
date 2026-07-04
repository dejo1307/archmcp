package diff

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestCompareMeta(t *testing.T) {
	base := facts.SnapshotMeta{
		RepoPath: "/repo", EnolaVersion: "1.0", IgnoreGlobHash: "h",
		Extractors: []string{"go", "typescript"},
	}

	t.Run("equivalent inputs produce no warnings", func(t *testing.T) {
		cur := base
		if c := CompareMeta(base, cur); !c.Comparable() {
			t.Errorf("expected comparable, got warnings: %v", c.Warnings)
		}
	})

	t.Run("extractor removed in current is flagged as REMOVED", func(t *testing.T) {
		cur := base
		cur.Extractors = []string{"go"} // typescript dropped from the current run
		msg := strings.Join(CompareMeta(base, cur).Warnings, " ")
		if !strings.Contains(msg, "typescript") {
			t.Fatalf("warning should name the missing extractor, got: %s", msg)
		}
		// typescript was in the baseline, not current → its facts appear as REMOVED.
		if !strings.Contains(msg, "baseline had extractor(s) not in the current run: typescript") ||
			!strings.Contains(msg, "REMOVED") {
			t.Errorf("expected a 'baseline had … REMOVED' direction, got: %s", msg)
		}
	})

	t.Run("extractor added in current is flagged as ADDED", func(t *testing.T) {
		cur := base
		cur.Extractors = []string{"go", "typescript", "openapi"} // openapi gained
		msg := strings.Join(CompareMeta(base, cur).Warnings, " ")
		// openapi is in current, not baseline → its facts appear as ADDED.
		if !strings.Contains(msg, "current run added extractor(s) not in the baseline: openapi") ||
			!strings.Contains(msg, "ADDED") {
			t.Errorf("expected a 'current added … ADDED' direction, got: %s", msg)
		}
	})

	t.Run("different enola version is flagged", func(t *testing.T) {
		cur := base
		cur.EnolaVersion = "2.0"
		if CompareMeta(base, cur).Comparable() {
			t.Error("expected a warning for differing enola versions")
		}
	})

	t.Run("empty baseline meta is a soft not-verifiable note, not a hard mismatch", func(t *testing.T) {
		// An auto-loaded baseline carries only RepoPath.
		autoLoaded := facts.SnapshotMeta{RepoPath: "/repo"}
		c := CompareMeta(autoLoaded, base)
		if c.Comparable() {
			t.Error("expected a not-verifiable note for an empty baseline")
		}
		if !strings.Contains(strings.Join(c.Warnings, " "), "predates snapshot receipts") {
			t.Errorf("expected the soft 'predates receipts' note, got: %v", c.Warnings)
		}
	})
}

func TestCompareReceipts(t *testing.T) {
	base := facts.SnapshotMeta{
		EnolaVersion: "1.0", SnapshotID: "aaa", Extractors: []string{"go"},
		FilesSeen: 100, FilesParsed: 90, ParseErrors: 0,
	}

	t.Run("rising parse errors surface as a quality regression", func(t *testing.T) {
		cur := base
		cur.SnapshotID = "bbb"
		cur.ParseErrors = 3
		rc := CompareReceipts(base, cur)
		if len(rc.QualityRegressions) == 0 {
			t.Fatal("expected a quality regression for rising parse errors")
		}
		if !strings.Contains(strings.Join(rc.QualityRegressions, " "), "parse errors rose") {
			t.Errorf("unexpected regressions: %v", rc.QualityRegressions)
		}
	})

	t.Run("dropping parsed/seen ratio is a regression", func(t *testing.T) {
		cur := base
		cur.SnapshotID = "bbb"
		cur.FilesParsed = 50 // 50% vs 90%
		rc := CompareReceipts(base, cur)
		if !strings.Contains(strings.Join(rc.QualityRegressions, " "), "parsed/seen ratio dropped") {
			t.Errorf("expected a ratio-drop regression, got: %v", rc.QualityRegressions)
		}
	})

	t.Run("identical snapshot ids short-circuit", func(t *testing.T) {
		rc := CompareReceipts(base, base)
		if !rc.Identical {
			t.Error("expected Identical=true for matching snapshot_id")
		}
	})
}
