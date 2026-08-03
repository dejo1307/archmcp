package history

import (
	"strings"
	"testing"
)

// draw renders a topology as the text a reader would see, so a golden test asserts the
// PICTURE rather than an internal structure. A lane renderer whose invariants all hold and
// whose output is unreadable has failed at the only thing it does.
func draw(t Topology) string {
	var b strings.Builder
	for _, row := range RenderGraph(t) {
		if row.Before != "" {
			b.WriteString(row.Before + "\n")
		}
		b.WriteString(row.Prefix + row.Entry.Short() + "\n")
		if row.After != "" {
			b.WriteString(row.After + "\n")
		}
	}
	return b.String()
}

// topo builds a topology from explicit edges: parents[i] lists the row indices row i
// descends from.
func topo(ids []string, parents ...[]int) Topology {
	rows := make([]Row, len(ids))
	for i, id := range ids {
		rows[i] = Row{Entry: Entry{ID: "sha256:" + id}}
		if i < len(parents) {
			rows[i].Parents = parents[i]
		}
	}
	return Topology{Rows: rows, Source: SourceGit}
}

func assertDraw(t *testing.T, name string, got, want string) {
	t.Helper()
	if strings.TrimRight(got, "\n") != strings.TrimRight(want, "\n") {
		t.Errorf("%s:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestRenderGraph_Linear(t *testing.T) {
	got := draw(topo([]string{"aaaaaaa", "bbbbbbb", "ccccccc"}, nil, []int{0}, []int{1}))
	assertDraw(t, "linear", got, `* aaaaaaa
* bbbbbbb
* ccccccc`)
}

// A branch: one revision with two children. Read downward, the lines DIVERGE — which is the
// direction this graph runs, and the opposite of `git log --graph`, where a branch converges
// as you read down toward its base.
func TestRenderGraph_Branch(t *testing.T) {
	got := draw(topo(
		[]string{"aaaaaaa", "bbbbbbb", "ccccccc"},
		nil, []int{0}, []int{0},
	))
	assertDraw(t, "branch", got, `* aaaaaaa
|\
* | bbbbbbb
  * ccccccc`)
}

// A merge: two lines converging on one revision.
func TestRenderGraph_Merge(t *testing.T) {
	got := draw(topo(
		[]string{"aaaaaaa", "bbbbbbb", "ccccccc", "ddddddd"},
		nil, []int{0}, []int{0}, []int{1, 2},
	))
	if !strings.Contains(got, "ddddddd") {
		t.Fatalf("the merge revision is missing:\n%s", got)
	}
	// The merge must be drawn once, in one column, with the converging lines shown above
	// it — not as two separate rows.
	if n := strings.Count(got, "ddddddd"); n != 1 {
		t.Errorf("the merge revision is drawn %d times:\n%s", n, got)
	}
	if !strings.Contains(got, "/") {
		t.Errorf("want converging lines drawn for the merge:\n%s", got)
	}
}

// Two roots — a history that observed two unrelated lines of work, which happens whenever
// somebody snapshots a repo whose earlier commits were never observed.
func TestRenderGraph_DisconnectedRoots(t *testing.T) {
	got := draw(topo([]string{"aaaaaaa", "bbbbbbb"}, nil, nil))
	for _, id := range []string{"aaaaaaa", "bbbbbbb"} {
		if !strings.Contains(got, id) {
			t.Errorf("root %s missing:\n%s", id, got)
		}
	}
}

// Every revision is drawn exactly once, marked `*`, in exactly one column. That is the
// property a reader relies on when scanning a column to follow one line of work — and the
// one a lane bug breaks silently.
func TestRenderGraph_EveryRevisionAppearsOnceAsAStar(t *testing.T) {
	tp := topo(
		[]string{"aaaaaaa", "bbbbbbb", "ccccccc", "ddddddd", "eeeeeee"},
		nil, []int{0}, []int{0}, []int{1, 2}, []int{3},
	)
	rows := RenderGraph(tp)
	if len(rows) != len(tp.Rows) {
		t.Fatalf("want one row per revision, got %d for %d", len(rows), len(tp.Rows))
	}
	for i, r := range rows {
		if n := strings.Count(r.Prefix, string(glyphRevision)); n != 1 {
			t.Errorf("row %d has %d revision marks in %q", i, n, r.Prefix)
		}
		if r.Entry.ID != tp.Rows[i].Entry.ID {
			t.Errorf("row %d drew the wrong revision", i)
		}
	}
}

// The graph must not keep the width of its widest moment forever: a branch that merges back
// should leave the trunk alone in column 0 again.
func TestRenderGraph_WidthCollapsesAfterAMerge(t *testing.T) {
	rows := RenderGraph(topo(
		[]string{"aaaaaaa", "bbbbbbb", "ccccccc", "ddddddd", "eeeeeee"},
		nil, []int{0}, []int{0}, []int{1, 2}, []int{3},
	))
	last := rows[len(rows)-1]
	if strings.TrimRight(last.Prefix, " ") != string(glyphRevision) {
		t.Errorf("after the merge the trunk should be alone in column 0, got %q", last.Prefix)
	}
}

func TestGraphWidth(t *testing.T) {
	rows := []GraphRow{{Prefix: "* "}, {Prefix: "* | "}}
	if got := GraphWidth(rows); got != 4 {
		t.Errorf("GraphWidth = %d, want 4", got)
	}
}

func TestRenderGraph_Empty(t *testing.T) {
	if rows := RenderGraph(Topology{}); len(rows) != 0 {
		t.Errorf("want no rows, got %d", len(rows))
	}
}
