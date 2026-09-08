// Package analytics is out of PCI scope by declaration: it reports on orders
// and must never be able to reach the vault, however indirectly.
package analytics

// Revenue totals a day's orders. It counts cents, never cards.
func Revenue(amounts []int) int {
	total := 0
	for _, cents := range amounts {
		total += cents
	}
	return total
}
