package main

import (
	"net/http"

	"github.com/gorilla/mux"
)

// The api service serves one route; the consumer's hardcoded internal-host call
// must resolve to it even though that call is also tagged external.
func main() {
	r := mux.NewRouter()
	r.HandleFunc("/v1/things/{id}", getThing).Methods("GET")
	http.ListenAndServe(":8080", r)
}

func getThing(w http.ResponseWriter, req *http.Request) {}
