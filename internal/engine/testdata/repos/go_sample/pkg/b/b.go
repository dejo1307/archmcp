package b

import "example.com/gosample/pkg/a"

// Beta calls back into package a, closing the a<->b import cycle.
func Beta() {
	a.Alpha()
}
