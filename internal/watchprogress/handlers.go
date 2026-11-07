package watchprogress

import (
	"encoding/json"
	"math"
	"net/http"

	"lumo-user-svc/internal/gatewayauth"
)

// maxDeleteKeys bounds one DELETE: a series' episodes, with room to spare.
const maxDeleteKeys = 500

// maxText bounds a stored title / picture URL.
const maxText = 1000

// Handlers — every route requires a valid Clerk session forwarded by the
// gateway ("/watch-progress" is in cmd/server/main.go's userScopedPaths).
type Handlers struct {
	repo *Repository
}

func NewHandlers(repo *Repository) *Handlers {
	return &Handlers{repo: repo}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /watch-progress", h.list)
	mux.HandleFunc("PUT /watch-progress", h.save)
	mux.HandleFunc("DELETE /watch-progress", h.delete)
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.repo.List(r.Context(), gatewayauth.ClerkUserID(r))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []Entry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func (h *Handlers) save(w http.ResponseWriter, r *http.Request) {
	var e Entry
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&e); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !ValidKey(e.Key) {
		http.Error(w, "key: must be movie:<id> or tv:<id>:<season>:<episode>", http.StatusBadRequest)
		return
	}
	if !validSeconds(e.Seconds) || (e.Duration != nil && !validSeconds(*e.Duration)) {
		http.Error(w, "seconds / duration: out of range", http.StatusBadRequest)
		return
	}
	e.Title = trimmed(e.Title)
	e.Image = trimmed(e.Image)

	if err := h.repo.Save(r.Context(), gatewayauth.ClerkUserID(r), e); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// delete takes the keys as ?key=…&key=…
func (h *Handlers) delete(w http.ResponseWriter, r *http.Request) {
	keys := r.URL.Query()["key"]
	if len(keys) == 0 || len(keys) > maxDeleteKeys {
		http.Error(w, "key: 1 to 500 of them", http.StatusBadRequest)
		return
	}
	for _, key := range keys {
		if !ValidKey(key) {
			http.Error(w, "key: must be movie:<id> or tv:<id>:<season>:<episode>", http.StatusBadRequest)
			return
		}
	}
	if err := h.repo.Delete(r.Context(), gatewayauth.ClerkUserID(r), keys); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validSeconds: a real position, up to a (generous) 100 hours.
func validSeconds(s float64) bool {
	return !math.IsNaN(s) && !math.IsInf(s, 0) && s >= 0 && s <= 360000
}

func trimmed(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	if len(*s) > maxText {
		return nil
	}
	return s
}
