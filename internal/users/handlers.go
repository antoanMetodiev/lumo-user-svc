package users

import (
	"encoding/json"
	"net/http"

	"lumo-user-svc/internal/gatewayauth"
	"lumo-user-svc/internal/models"
)

// Handlers mirrors UserController + InternalUserController.
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /user/me", h.getCurrentUser)
	mux.HandleFunc("POST /internal/users/{clerkId}/subscription", h.updateSubscription)
}

// getCurrentUser mirrors UserController.getCurrentUser: 404 here means the
// Clerk webhook hasn't synced this account into our DB yet — near-instant
// after sign-up, but a real race the frontend should tolerate rather than
// treat as a hard failure. Also serves lumo-api-gateway's own
// InternalUserController.getUser, which reuses this same route with
// X-Clerk-User-Id set to whatever clerkId it's looking up on
// lumo-payments-svc's behalf.
func (h *Handlers) getCurrentUser(w http.ResponseWriter, r *http.Request) {
	clerkID := gatewayauth.ClerkUserID(r)

	user, err := h.svc.FindByClerkID(r.Context(), clerkID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if user == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	writeJSON(w, user)
}

// updateSubscriptionRequest mirrors UpdateSubscriptionRequest — the field is
// a plain string here (not the SubscriptionType enum type) so an invalid
// value can be rejected explicitly with the same net effect as Jackson's
// default enum deserialization failing (400), rather than a Go zero-value
// silently mapping to something unintended.
type updateSubscriptionRequest struct {
	SubscriptionType string `json:"subscriptionType"`
}

// updateSubscription mirrors InternalUserController.updateSubscription.
func (h *Handlers) updateSubscription(w http.ResponseWriter, r *http.Request) {
	clerkID := r.PathValue("clerkId")

	var req updateSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	subscriptionType := models.SubscriptionType(req.SubscriptionType)
	if !subscriptionType.Valid() {
		http.Error(w, "subscriptionType: must be REGULAR_USER or PRO_USER", http.StatusBadRequest)
		return
	}

	updated, err := h.svc.UpdateSubscriptionType(r.Context(), clerkID, subscriptionType)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !updated {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
