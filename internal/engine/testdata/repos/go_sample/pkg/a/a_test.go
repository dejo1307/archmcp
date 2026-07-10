package a

import "testing"

// The in-package idiom: the production function is called unqualified, so the
// test-ref pass must resolve it against the file's own package dir ("pkg/a.helper").
// This is the shape behind golf's NewRateLimiter false positive. (v100)
func TestHelperDoubles(t *testing.T) {
	if helper(2) != 4 {
		t.Fatalf("helper(2) != 4")
	}
}
