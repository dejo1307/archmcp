// Package checkout is ordinary application code. It charges through the
// gateway and never learns what a PAN is.
package checkout

import "policyascode/gateway"

// Pay charges the card behind a token.
func Pay(token string, cents int) bool {
	return gateway.Charge(token, cents)
}
