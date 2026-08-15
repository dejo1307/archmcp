package api

import (
	"strconv"

	"layersgate/storage"
)

func PriceOf(item string) string {
	return strconv.Itoa(storage.ReadPrice(item))
}
