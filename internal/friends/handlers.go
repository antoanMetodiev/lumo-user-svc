package friends

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"lumo-user-svc/internal/gatewayauth"
)

// Handlers mirrors watchlist.Handlers' shape — every route requires a valid
// Clerk session forwarded by the gateway (see gatewayauth.Middleware; "/friends"
// must be listed in cmd/server/main.go's userScopedPaths for that to apply).
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /friends", h.listFriends)
	mux.HandleFunc("DELETE /friends/{clerkId}", h.unfriend)
	mux.HandleFunc("GET /friends/search", h.search)
	mux.HandleFunc("GET /friends/requests/incoming", h.listIncoming)
	mux.HandleFunc("GET /friends/requests/outgoing", h.listOutgoing)
	mux.HandleFunc("POST /friends/requests", h.sendRequest)
	mux.HandleFunc("DELETE /friends/requests/{id}", h.cancelOrDecline)
	mux.HandleFunc("POST /friends/requests/{id}/accept", h.accept)
	mux.HandleFunc("GET /friends/notifications", h.consumeNotifications)
	mux.HandleFunc("GET /friends/notifications/count", h.notificationCount)
}

// consumeNotifications returns the caller's notifications and deletes them
// from the database in the same call — "checking" the list is what clears
// it, there's no separate mark-as-read step.
func (h *Handlers) consumeNotifications(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	notifications, err := h.svc.ConsumeNotifications(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, notifications)
}

func (h *Handlers) notificationCount(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	count, err := h.svc.NotificationCount(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]int64{"count": count})
}

func (h *Handlers) listFriends(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	friends, err := h.svc.ListFriends(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, friends)
}

func (h *Handlers) unfriend(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)
	other := r.PathValue("clerkId")

	if err := h.svc.Unfriend(r.Context(), clerkID, other); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) search(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)
	query := strings.TrimSpace(r.URL.Query().Get("query"))

	results, err := h.svc.Search(r.Context(), clerkID, query)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, results)
}

func (h *Handlers) listIncoming(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	reqs, err := h.svc.ListIncoming(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, reqs)
}

func (h *Handlers) listOutgoing(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	reqs, err := h.svc.ListOutgoing(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, reqs)
}

type sendRequestBody struct {
	AddresseeID string `json:"addresseeId"`
}

func (h *Handlers) sendRequest(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	var req sendRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.AddresseeID) == "" {
		http.Error(w, "addresseeId: must not be blank", http.StatusBadRequest)
		return
	}

	fr, err := h.svc.SendRequest(r.Context(), clerkID, req.AddresseeID)
	if err != nil {
		switch err {
		case ErrSelfRequest:
			http.Error(w, "addresseeId: cannot send a friend request to yourself", http.StatusBadRequest)
		case ErrUserNotFound:
			http.Error(w, "addresseeId: user not found", http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, fr)
}

func (h *Handlers) cancelOrDecline(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if err := h.svc.CancelOrDecline(r.Context(), clerkID, id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) accept(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ok, err := h.svc.Accept(r.Context(), clerkID, id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"status": "ACCEPTED"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
