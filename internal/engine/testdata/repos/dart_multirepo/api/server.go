package main

import (
	"net/http"

	"github.com/gorilla/mux"
)

// The server half. Methods are declared explicitly, as a real backend does: the
// cross-repo matcher joins on (normalized path, method), so a route registered
// without a verb indexes under an empty method and can never match a client's GET.
//
// /api/orders is called by the mobile client for both GET and POST;
// /api/internal/reindex is called by nothing loaded, which is what makes it an
// unused-route candidate rather than a resolved edge.
func main() {
	r := mux.NewRouter()
	r.HandleFunc("/api/orders", listOrders).Methods("GET")
	r.HandleFunc("/api/orders", createOrder).Methods("POST")
	r.HandleFunc("/api/orders/detail", orderDetail).Methods("GET")
	r.HandleFunc("/api/internal/reindex", reindex).Methods("POST")
	http.ListenAndServe(":8080", r)
}

func listOrders(w http.ResponseWriter, r *http.Request)  {}
func createOrder(w http.ResponseWriter, r *http.Request) {}
func orderDetail(w http.ResponseWriter, r *http.Request) {}
func reindex(w http.ResponseWriter, r *http.Request)     {}
