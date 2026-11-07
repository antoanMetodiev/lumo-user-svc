package watching

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"

	"lumo-user-svc/internal/gatewayauth"
)

// Handlers mirrors friends.Handlers' shape — every route requires a valid
// Clerk session forwarded by the gateway ("/watching" must be listed in
// cmd/server/main.go's userScopedPaths for that to apply).
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("PUT /watching", h.set)
	mux.HandleFunc("DELETE /watching", h.clear)
	mux.HandleFunc("GET /watching/friends", h.listFriendsWatching)
}

type setBody struct {
	TmdbID  string `json:"tmdbId"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	Season  *int   `json:"season,omitempty"`
	Episode *int   `json:"episode,omitempty"`

	Position *float64 `json:"position,omitempty"`
	Duration *float64 `json:"duration,omitempty"`
	Paused   *bool    `json:"paused,omitempty"`
}

// maxSeconds bounds a position or runtime: 100 hours.
const maxSeconds = 360000

// seconds keeps a reported time only when it's a sane one.
func seconds(value *float64) *float64 {
	if value == nil || math.IsNaN(*value) || *value < 0 || *value > maxSeconds {
		return nil
	}
	rounded := math.Round(*value)
	return &rounded
}

func (h *Handlers) set(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	var body setBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.TmdbID) == "" || strings.TrimSpace(body.Title) == "" {
		http.Error(w, "tmdbId and title must not be blank", http.StatusBadRequest)
		return
	}
	if body.Type != "movie" && body.Type != "series" {
		http.Error(w, `type must be "movie" or "series"`, http.StatusBadRequest)
		return
	}

	h.svc.Set(r.Context(), clerkID, Status{
		TmdbID:  body.TmdbID,
		Type:    body.Type,
		Title:   body.Title,
		Season:  body.Season,
		Episode: body.Episode,

		Position: seconds(body.Position),
		Duration: seconds(body.Duration),
		Paused:   body.Paused,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) clear(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)
	h.svc.Clear(r.Context(), clerkID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) listFriendsWatching(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	out, err := h.svc.ListFriendsWatching(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
