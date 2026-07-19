package main

import (
	"net/http"

	"github.com/gorilla/mux"
)

// The api service serves one route, but registers it on a "/v1" subrouter that is
// PASSED INTO registerThings — so the full path ("/v1/things/{id}") is only
// recovered by the extractor's interprocedural subrouter-prefix composition (v125).
// Before that, this file stored the bare "/things/{id}" and the consumer's
// hardcoded internal-host call could not resolve to it.
func main() {
	r := mux.NewRouter()
	v1 := r.PathPrefix("/v1").Subrouter()
	registerThings(v1)
	http.ListenAndServe(":8080", r)
}

// registerThings receives the mounted subrouter and registers bare leaf paths on
// it; the "/v1" prefix must be composed across this call boundary.
func registerThings(r *mux.Router) {
	r.HandleFunc("/things/{id}", getThing).Methods("GET")
}

func getThing(w http.ResponseWriter, req *http.Request) {}
