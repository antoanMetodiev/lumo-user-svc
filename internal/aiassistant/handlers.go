package aiassistant

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Handlers mirrors likes.Handlers — every route requires a valid Clerk
// session forwarded by the gateway ("/ai-assistant" must be in
// cmd/server/main.go's userScopedPaths for gatewayauth.Middleware to run),
// even though the handler itself never reads the caller's identity.
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /ai-assistant/api-key", h.getApiKey)
}

type apiKeyResponse struct {
	ApiKey string `json:"apiKey"`
}

func (h *Handlers) getApiKey(w http.ResponseWriter, r *http.Request) {
	apiKey, err := h.svc.GetRandomApiKey(r.Context())
	if err != nil {
		if errors.Is(err, ErrNoActiveKeys) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apiKeyResponse{ApiKey: apiKey})
}
