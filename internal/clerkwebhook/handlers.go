package clerkwebhook

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// Handlers mirrors ClerkWebhookController. Public endpoint — authenticated
// by its own Svix signature, not gatewayauth.Middleware.
type Handlers struct {
	webhookSecret string
	svc           *Service
}

func NewHandlers(webhookSecret string, svc *Service) *Handlers {
	return &Handlers{webhookSecret: webhookSecret, svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /webhooks/clerk", h.handleClerkWebhook)
}

// handleClerkWebhook mirrors ClerkWebhookController.handleClerkWebhook.
// Signature verification uses the raw request body — never a re-serialized
// one, since that would change the bytes being signed.
func (h *Handlers) handleClerkWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	err = verifySignature(h.webhookSecret, r.Header.Get("svix-id"), r.Header.Get("svix-timestamp"), r.Header.Get("svix-signature"), payload)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var event ClerkWebhookEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err := h.svc.Handle(r.Context(), event); err != nil {
		log.Printf("clerkwebhook: handling %q event failed: %v", event.Type, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
