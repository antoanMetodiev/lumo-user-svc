package lastviewed

import (
	"encoding/json"
	"net/http"
	"strings"

	"lumo-user-svc/internal/gatewayauth"
)

// Handlers mirrors LastViewedController — every route requires a valid
// Clerk session forwarded by the gateway (see gatewayauth.Middleware).
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /last-viewed", h.getLastViewed)
	mux.HandleFunc("POST /last-viewed", h.recordView)
}

func (h *Handlers) getLastViewed(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	items, err := h.svc.GetLastViewed(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

// recordView mirrors LastViewedController.recordView — no @ResponseStatus
// override in Java, so this returns 200 (not 201) like the original.
func (h *Handlers) recordView(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	var req RecordViewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		http.Error(w, "type: must not be blank", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		http.Error(w, "title: must not be blank", http.StatusBadRequest)
		return
	}

	item, err := h.svc.RecordView(r.Context(), clerkID, req)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, item)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
