package web

import "layersgate/api"

// RenderReceipt is the delivery layer: it may depend inwards, on api.
func RenderReceipt(item string) string {
	return item + ": " + api.PriceOf(item)
}
