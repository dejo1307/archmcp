// Package legacy is the card storage the 3.2.1 policy allowed and the current
// one does not. It is anchored by the superseded page, which is what makes it
// findable: the law says code under a retired decision must be gone.
package legacy

// Row is a stored card, kept the way the previous standard permitted.
type Row struct {
	Last4  string
	Expiry string
}

var rows []Row

// Append records a card row.
func Append(r Row) {
	rows = append(rows, r)
}
