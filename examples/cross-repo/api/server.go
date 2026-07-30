package main

import (
	"net/http"

	"github.com/gorilla/mux"
)

// The route below is registered as "/orders/{id}" — but it is NOT served there.
//
// main mounts a subrouter at "/api/v2" and hands it to registerOrders, so the path a
// client must actually call is "/api/v2/orders/{id}". Recovering that requires
// composing the prefix ACROSS THE FUNCTION BOUNDARY: the prefix and the leaf path are
// never written together in one place, and neither file alone contains the answer.
func main() {
	r := mux.NewRouter()

	v2 := r.PathPrefix("/api/v2").Subrouter()
	registerOrders(v2)

	http.ListenAndServe(":8080", r)
}

// registerOrders receives an already-mounted subrouter and registers bare leaf paths
// on it. Read on its own, this function appears to serve "/orders/{id}".
func registerOrders(r *mux.Router) {
	r.HandleFunc("/orders/{id}", getOrder).Methods("GET")
	r.HandleFunc("/orders", listOrders).Methods("GET")
}

func getOrder(w http.ResponseWriter, req *http.Request)   {}
func listOrders(w http.ResponseWriter, req *http.Request) {}
