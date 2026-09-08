// Package gateway is the sanctioned boundary around the cardholder data
// environment: every other package asks it, never the vault.
package gateway

import "policyascode/cardholder"

// Tokenize hands a PAN to the vault and gives the caller the token back.
func Tokenize(token, pan string) string {
	return cardholder.Store(token, pan).Token
}

// Charge is the one operation that reads a PAN, and it never returns one.
func Charge(token string, cents int) bool {
	pan := cardholder.ReadPAN(token)
	return authorize(pan, cents)
}

func authorize(pan string, cents int) bool {
	return len(pan) > 0 && cents > 0
}
