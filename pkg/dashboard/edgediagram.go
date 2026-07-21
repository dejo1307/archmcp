package dashboard

import (
	"math"
	"sort"
)

// Diagram geometry. The viewBox is fixed; the SVG scales responsively in CSS.
// Radius/center leave a margin on every side for the node labels that splay
// outward from the ring.
const (
	diagramWidth   = 640.0
	diagramHeight  = 460.0
	diagramRadius  = 150.0
	diagramNodeR   = 5.0
	diagramCurve   = 0.14 // control-point offset as a fraction of the chord length
	diagramLabelPx = 8.0  // gap between a node and its label
)

// diagramNode is one service node placed on the ring, with the text-anchor its
// outward-splayed label should use.
type diagramNode struct {
	Name   string
	X, Y   float64
	LabelX float64
	Anchor string // "start" | "end"
}

// diagramEdge is one directed consumer→provider edge as a quadratic curve whose
// endpoints sit on the node rims (so the arrowhead lands on the provider's edge,
// not its centre). The control point is offset perpendicular to the chord so a
// bidirectional pair draws as two distinct arcs rather than one overlapping line.
type diagramEdge struct {
	X1, Y1 float64
	CX, CY float64
	X2, Y2 float64
}

// diagramView is the full node-link layout the template renders as inline SVG.
type diagramView struct {
	Width, Height float64
	Nodes         []diagramNode
	Edges         []diagramEdge
}

// buildEdgeDiagram lays the services out on a circle and routes each cross-repo
// edge as a rim-to-rim curve, returning nil when there is nothing to draw. The
// layout is a pure function of the (already-sorted) inputs — deterministic, so
// the rendered SVG is stable across refreshes and unit-testable.
func buildEdgeDiagram(services []serviceRow, edges []edgeRow) *diagramView {
	if len(services) == 0 || len(edges) == 0 {
		return nil
	}

	// Nodes on a circle, starting at the top and going clockwise, in the input's
	// sorted order. Record each centre so edges can be routed rim-to-rim.
	names := make([]string, 0, len(services))
	for _, s := range services {
		names = append(names, s.Name)
	}
	sort.Strings(names) // defensive: layout must not depend on caller ordering

	cx, cy := diagramWidth/2, diagramHeight/2
	type pt struct{ x, y float64 }
	center := map[string]pt{}
	view := &diagramView{Width: diagramWidth, Height: diagramHeight}

	n := len(names)
	for i, name := range names {
		theta := -math.Pi/2 + 2*math.Pi*float64(i)/float64(n)
		x := cx + diagramRadius*math.Cos(theta)
		y := cy + diagramRadius*math.Sin(theta)
		center[name] = pt{x, y}

		anchor := "start"
		labelX := x + diagramNodeR + diagramLabelPx
		if math.Cos(theta) < -1e-9 { // left half → label ends at the node
			anchor = "end"
			labelX = x - diagramNodeR - diagramLabelPx
		}
		view.Nodes = append(view.Nodes, diagramNode{
			Name: name, X: x, Y: y, LabelX: labelX, Anchor: anchor,
		})
	}

	for _, e := range edges {
		a, okA := center[e.Consumer]
		b, okB := center[e.Provider]
		if !okA || !okB || e.Consumer == e.Provider {
			continue // endpoint not a known node (or a self-loop) → nothing to draw
		}
		dx, dy := b.x-a.x, b.y-a.y
		dist := math.Hypot(dx, dy)
		if dist < 1e-9 {
			continue
		}
		ux, uy := dx/dist, dy/dist
		// Trim both ends to the node rim.
		x1, y1 := a.x+ux*diagramNodeR, a.y+uy*diagramNodeR
		x2, y2 := b.x-ux*(diagramNodeR+3), b.y-uy*(diagramNodeR+3) // +3 leaves room for the arrowhead
		// Control point: chord midpoint pushed perpendicular (left of the direction
		// of travel), so A→B and B→A bow to opposite sides and stay distinct.
		mx, my := (x1+x2)/2, (y1+y2)/2
		off := dist * diagramCurve
		cxp, cyp := mx-uy*off, my+ux*off
		view.Edges = append(view.Edges, diagramEdge{
			X1: x1, Y1: y1, CX: cxp, CY: cyp, X2: x2, Y2: y2,
		})
	}

	return view
}
