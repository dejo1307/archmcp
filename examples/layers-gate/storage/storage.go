package storage

// ReadPrice is the innermost layer: it depends on nothing above it.
func ReadPrice(item string) int {
	return len(item)
}
