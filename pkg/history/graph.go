package history

import "strings"

// The graph column, drawn with git's vocabulary: `*` is a revision, `|` a line continuing
// past it, `/` and `\` a line moving between columns.
//
// It is drawn OLDEST FIRST, the same direction `enola log` already reads, which is the
// opposite of `git log --graph`. The trade is deliberate. Git reads newest-first because a
// changelog answers "what landed recently"; a history of architecture is read to answer
// "how did it get like this", and that question runs forward. So lines DIVERGE downward
// where work branched and CONVERGE downward where it merged — the shape a reader following
// the story is tracing anyway.
//
// Columns are two characters wide, and the odd positions BETWEEN them carry the diagonals.
// That is what makes `|\` read as one line leaving column 0 rather than as two unrelated
// marks, and it is why the row types below build a character grid rather than joining cells.
const (
	glyphRevision = '*'
	glyphLine     = '|'
	glyphConverge = '/'
	glyphDiverge  = '\\'
)

// GraphRow is one revision's place in the drawing.
type GraphRow struct {
	Entry Entry
	// Before is a connector drawn ABOVE the revision, where lines converge into it. Empty
	// unless this revision is a merge.
	Before string
	// Prefix is the graph column for the revision's own row, e.g. "* | ".
	Prefix string
	// After is a connector drawn BELOW the revision, where lines diverge from it. Empty
	// unless work branched here.
	After string
}

// RenderGraph lays a topology out in columns.
//
// The algorithm is git's, mirrored. Git walks newest-first and each lane holds the commit
// it is waiting to draw, replacing it with that commit's parents. Walking oldest-first the
// relation is identical with the arrow reversed: a lane holds a revision whose CHILDREN are
// still to be drawn, and drawing a revision replaces its lane with its children.
//
// Convergence is drawn above the merge and divergence below the branch point, because in
// this direction those are the rows where the lines actually move. Putting both below —
// which is what a single connector field invites — draws a merge's incoming lines after the
// revision they arrived at, so the picture shows them joining something that already
// happened.
func RenderGraph(t Topology) []GraphRow {
	rows := make([]GraphRow, 0, len(t.Rows))

	// lanes[i] is the row index whose turn to be drawn that column is waiting for; -1 for
	// a free column.
	var lanes []int

	// children inverts the parent links once, rather than scanning per row.
	children := make([][]int, len(t.Rows))
	for i, r := range t.Rows {
		for _, p := range r.Parents {
			children[p] = append(children[p], i)
		}
	}

	for i, r := range t.Rows {
		var incoming []int
		for col, waiting := range lanes {
			if waiting == i {
				incoming = append(incoming, col)
			}
		}

		// A revision joins the leftmost column already waiting for it, so a merge lands on
		// the line a reader has been following rather than skipping to a fresh one. Only
		// allocate when nothing is waiting — freeLane can extend the lane set, and calling
		// it speculatively would widen the graph for a column never used.
		col := -1
		if len(incoming) > 0 {
			col = incoming[0]
		}
		if col < 0 {
			col = freeLane(&lanes)
		}

		row := GraphRow{Entry: r.Entry}

		// Converging lines are drawn while they still exist, then released so the
		// revision's own row does not also show them.
		if len(incoming) > 1 {
			row.Before = connector(lanes, col, incoming[1:], glyphConverge)
			for _, c := range incoming[1:] {
				lanes[c] = -1
			}
		}

		row.Prefix = revisionPrefix(lanes, col, i)

		lanes[col] = -1
		var claimed []int
		for _, child := range children[i] {
			c := freeLane(&lanes)
			lanes[c] = child
			claimed = append(claimed, c)
		}
		if len(claimed) > 1 {
			row.After = connector(lanes, col, claimed[1:], glyphDiverge)
		}

		trimLanes(&lanes)
		rows = append(rows, row)
	}
	return rows
}

// freeLane returns the index of a reusable column, extending the set if none is free.
// Leftmost-first, so the trunk stays in column 0 and short-lived branches sit to its right
// — the convention git's output has, and the reason a reader can follow "the main line"
// without being told which it is.
func freeLane(lanes *[]int) int {
	for i, v := range *lanes {
		if v == -1 {
			return i
		}
	}
	*lanes = append(*lanes, -1)
	return len(*lanes) - 1
}

// revisionPrefix draws the row carrying the revision: `*` in its own column, `|` in every
// other column still carrying a line.
func revisionPrefix(lanes []int, col, self int) string {
	cells := grid(laneWidth(lanes, col))
	for i, v := range lanes {
		if v != -1 && v != self && i != col {
			cells[2*i] = glyphLine
		}
	}
	cells[2*col] = glyphRevision
	return string(cells)
}

// connector draws a row in which lines move between columns: a `|` for every column holding
// a line that is staying put, and glyph in the gap beside each column that is moving.
//
// The diagonal goes in the gap to the LEFT of the moving column (position 2c-1), which is
// what makes it point back toward the column it came from or is heading to. Drawing it in
// the moving column's own slot would leave a mark with nothing to connect it to.
func connector(lanes []int, col int, moving []int, glyph byte) string {
	width := laneWidth(lanes, col)
	for _, c := range moving {
		if 2*c+1 > width {
			width = 2*c + 1
		}
	}
	cells := grid(width)

	isMoving := make(map[int]bool, len(moving))
	for _, c := range moving {
		isMoving[c] = true
	}
	for i, v := range lanes {
		if v != -1 && !isMoving[i] {
			cells[2*i] = glyphLine
		}
	}
	cells[2*col] = glyphLine
	for _, c := range moving {
		if pos := 2*c - 1; pos > 0 && pos < len(cells) {
			cells[pos] = glyph
		}
	}
	return strings.TrimRight(string(cells), " ")
}

// laneWidth is the character width covering every lane plus col: two per column.
func laneWidth(lanes []int, col int) int {
	n := len(lanes)
	if col+1 > n {
		n = col + 1
	}
	return 2 * n
}

func grid(width int) []byte {
	cells := make([]byte, width)
	for i := range cells {
		cells[i] = ' '
	}
	return cells
}

// trimLanes drops trailing free columns so the graph does not keep the width of its widest
// moment forever.
func trimLanes(lanes *[]int) {
	l := *lanes
	for len(l) > 0 && l[len(l)-1] == -1 {
		l = l[:len(l)-1]
	}
	*lanes = l
}

// GraphWidth is the widest prefix in a set of rows, so callers can align what follows.
func GraphWidth(rows []GraphRow) int {
	w := 0
	for _, r := range rows {
		if len(r.Prefix) > w {
			w = len(r.Prefix)
		}
	}
	return w
}
