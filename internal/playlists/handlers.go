package playlists

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"lumo-user-svc/internal/gatewayauth"
)

// Handlers mirrors likes.Handlers — every route requires a valid Clerk
// session ("/playlists" must be in cmd/server/main.go's userScopedPaths).
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /playlists", h.listMine)
	mux.HandleFunc("POST /playlists", h.create)
	mux.HandleFunc("PATCH /playlists/{id}", h.update)
	mux.HandleFunc("DELETE /playlists/{id}", h.delete)
	mux.HandleFunc("GET /playlists/{id}/items", h.listItems)
	mux.HandleFunc("POST /playlists/{id}/items", h.addItem)
	mux.HandleFunc("DELETE /playlists/{id}/items/{recordId}", h.removeItem)
	// Not "/playlists/user/{clerkId}" — that's the same 3-segment shape as
	// "/playlists/{id}/items" with the wildcard/literal swapped (segment 2
	// wildcard-vs-literal, segment 3 literal-vs-wildcard), which net/http's
	// ServeMux refuses to register as an unambiguous pair (panics at
	// startup: "/playlists/user/items" would match either). Keeping the
	// wildcard in the same position as items' {id} — just a different
	// trailing literal — makes the two provably disjoint.
	mux.HandleFunc("GET /playlists/{clerkId}/visible", h.listForUser)
}

func (h *Handlers) listMine(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)
	items, err := h.svc.ListMine(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

type upsertRequest struct {
	Name     string `json:"name"`
	IsPublic bool   `json:"isPublic"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	var req upsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name: must not be blank", http.StatusBadRequest)
		return
	}

	p, err := h.svc.Create(r.Context(), clerkID, name, req.IsPublic)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, p)
}

func (h *Handlers) update(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var req upsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name: must not be blank", http.StatusBadRequest)
		return
	}

	ok, err := h.svc.Update(r.Context(), clerkID, id, name, req.IsPublic)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) delete(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ok, err := h.svc.Delete(r.Context(), clerkID, id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) listItems(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	items, err := h.svc.ListItems(r.Context(), clerkID, id)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	writeJSON(w, items)
}

func (h *Handlers) addItem(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var req AddItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.RecordID) == "" {
		http.Error(w, "recordId: must not be blank", http.StatusBadRequest)
		return
	}

	item, err := h.svc.AddItem(r.Context(), clerkID, id, req)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	writeJSON(w, item)
}

func (h *Handlers) removeItem(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	recordID := r.PathValue("recordId")

	if err := h.svc.RemoveItem(r.Context(), clerkID, id, recordID); err != nil {
		writeServiceErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listForUser is public among any signed-in caller (same as
// likes.getLikesForUser) — the friendship/public gating happens inside
// Service.ListForUser, not here, so a stranger just sees an empty list
// rather than getting a 403/404 that would confirm the target has playlists.
func (h *Handlers) listForUser(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)
	target := r.PathValue("clerkId")

	items, err := h.svc.ListForUser(r.Context(), clerkID, target)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

func writeServiceErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if errors.Is(err, ErrItemLimitReached) {
		// 409, not 400 — see likes.Handlers.addLike's identical reasoning.
		http.Error(w, "a playlist can have at most 30 titles", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
