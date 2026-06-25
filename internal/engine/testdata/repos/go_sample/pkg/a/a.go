package a

import "example.com/gosample/pkg/b"

// Alpha calls into package b, forming one side of a deliberate a<->b import
// cycle so the cycles explainer (Tarjan SCC) has something to detect.
func Alpha() {
	b.Beta()
}
