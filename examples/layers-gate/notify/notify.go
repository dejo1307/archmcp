package notify

import "fmt"

// SendReceipt is delivery too — the same layer as web, and the one storage is about
// to reach into.
func SendReceipt(to, body string) {
	fmt.Println(to, body)
}
