package main

import (
	"context"
	"net/http"
)

// extBase is a third-party host with no loaded server: tagged external, host known.
const extBase = "https://api.example.com"

// internalBase is a hardcoded INTERNAL host that IS a loaded repo. The call to it
// must resolve to the api service AND be tagged external — the linker attempts the
// server match before falling back to the external bucket (GAP-LK-02, v101).
const internalBase = "http://api:8080"

// Zepto mirrors golf's region-switch idiom: a struct field bound to several
// absolute literals that disagree on host — external, but no single host.
type Zepto struct{ baseURL string }

func NewZepto(region string) *Zepto {
	var baseURL string
	switch region {
	case "eu":
		baseURL = "https://api.zeptomail.eu/v1.1"
	default:
		baseURL = "https://api.zeptomail.com/v1.1"
	}
	return &Zepto{baseURL: baseURL}
}

// Options is injected from config; BaseURL has no string-literal binding, so the
// call below stays an internal client route and remains an unresolved edge.
type Options struct{ BaseURL string }

type Client struct {
	zepto   *Zepto
	options Options
}

func (c *Client) run(ctx context.Context) {
	http.Get(extBase + "/v1/widgets")
	http.NewRequestWithContext(ctx, "GET", internalBase+"/v1/things/{id}", nil)
	http.NewRequest("POST", c.zepto.baseURL+"/v3/messages", nil)
	http.Get(c.options.BaseURL + "/v1/internal")
}
