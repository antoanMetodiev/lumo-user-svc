package watchlist

import (
	"encoding/json"
	"net/http"
	"strings"

	"lumo-user-svc/internal/gatewayauth"
)

// Handlers mirrors WatchlistController — every route requires a valid Clerk
// session forwarded by the gateway (see gatewayauth.Middleware).
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /watchlist", h.getWatchlist)
	mux.HandleFunc("POST /watchlist", h.addToWatchlist)
	mux.HandleFunc("DELETE /watchlist/{recordId}", h.removeFromWatchlist)
}

func (h *Handlers) getWatchlist(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	items, err := h.svc.GetWatchlist(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

// addToWatchlist mirrors WatchlistController.addToWatchlist — no
// @ResponseStatus override in Java, so this returns 200 (not 201) like the
// original.
func (h *Handlers) addToWatchlist(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	var req AddToWatchlistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.RecordID) == "" {
		http.Error(w, "recordId: must not be blank", http.StatusBadRequest)
		return
	}

	item, err := h.svc.AddToWatchlist(r.Context(), clerkID, req)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, item)
}

func (h *Handlers) removeFromWatchlist(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)
	recordID := r.PathValue("recordId")

	if err := h.svc.RemoveFromWatchlist(r.Context(), clerkID, recordID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
