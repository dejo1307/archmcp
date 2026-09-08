// Package cardholder is the cardholder data environment: the only code in this
// module that holds a primary account number in the clear.
package cardholder

// Card is a stored primary account number and the token that stands in for it
// everywhere else in the module.
type Card struct {
	Token string
	PAN   string
}

var vault = map[string]Card{}

// Store puts a PAN in the vault and returns the token that replaces it.
func Store(token, pan string) Card {
	card := Card{Token: token, PAN: pan}
	vault[token] = card
	return card
}

// ReadPAN returns the raw primary account number behind a token.
func ReadPAN(token string) string {
	return vault[token].PAN
}
