package main

import "net/http"

// An API-compatible reimplementation of the acme backend: the same paths, so
// acme's frontend calls match here by shape. They must still resolve to acme's
// own server rather than drawing a cross-repo edge to this repo.
func main() {
	http.HandleFunc("/api/v1/search/results", results)
	http.ListenAndServe(":8000", nil)
}

func results(w http.ResponseWriter, req *http.Request) {}
