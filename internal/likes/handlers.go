package likes

import (
	"encoding/json"
	"net/http"
	"strings"

	"lumo-user-svc/internal/gatewayauth"
)

// Handlers mirrors watchlist.Handlers — every route requires a valid Clerk
// session forwarded by the gateway ("/likes" must be in cmd/server/main.go's
// userScopedPaths for gatewayauth.Middleware to populate it).
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /likes", h.getLikes)
	mux.HandleFunc("POST /likes", h.addLike)
	mux.HandleFunc("DELETE /likes/{recordId}", h.removeLike)
	mux.HandleFunc("GET /likes/user/{clerkId}", h.getLikesForUser)
}

func (h *Handlers) getLikes(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	items, err := h.svc.GetLikes(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

// getLikesForUser is deliberately public among any signed-in caller — no
// friendship or other relationship check, per the user's explicit call:
// what someone likes is visible to anyone on the platform, not gated behind
// being friends first. Still requires a session (this whole path prefix is
// in userScopedPaths), just not a specific one.
func (h *Handlers) getLikesForUser(w http.ResponseWriter, r *http.Request) {
	targetClerkID := r.PathValue("clerkId")

	items, err := h.svc.GetLikes(r.Context(), targetClerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

// addLike mirrors watchlist's addToWatchlist — 200 (not 201), same shape.
func (h *Handlers) addLike(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	var req AddLikeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.RecordID) == "" {
		http.Error(w, "recordId: must not be blank", http.StatusBadRequest)
		return
	}

	item, err := h.svc.AddLike(r.Context(), clerkID, req)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, item)
}

func (h *Handlers) removeLike(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)
	recordID := r.PathValue("recordId")

	if err := h.svc.RemoveLike(r.Context(), clerkID, recordID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
