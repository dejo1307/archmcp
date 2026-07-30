package main

import "net/http"

// The web service calls the api service at its true runtime path — the composed one,
// not the bare path written in registerOrders.
const apiBase = "http://api:8080"

func fetchOrder() {
	http.Get(apiBase + "/api/v2/orders/{id}")
	http.Get(apiBase + "/api/v2/orders")
}

// This one is DELIBERATELY unresolvable, and the report should say so.
//
// The path is built at runtime from a value enola cannot see, so there is no path to
// match against any route. enola detects that an outbound call happened and reports it
// as unresolved rather than guessing — a wrong edge is worse than a missing one,
// because a wrong edge is acted upon.
func fetchDynamic(tenant string) {
	http.Get(apiBase + "/api/v2/" + tenant + "/orders")
}
