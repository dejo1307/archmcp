package main

import (
	"example.com/gosample/pkg/a"
	"example.com/gosample/pkg/b"
)

// main is the entry point; it exercises both packages so the snapshot has a
// clear root with outgoing imports and calls.
func main() {
	a.Alpha()
	b.Beta()
}
