// Package heartbeat mirrors Heartbeat: two trivial public endpoints with no
// downstream fan-out (unlike lumo-api-gateway's own heartbeat, this service
// doesn't call anything else to answer one).
package heartbeat

import "net/http"

func Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", ok)
	mux.HandleFunc("GET /heartbeat", ok)
}

func ok(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
