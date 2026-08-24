// Package clerkwebhook mirrors ClerkWebhookController + ClerkWebhookService
// + PaymentsSyncService. Keeps the local `users` table in sync with Clerk,
// which is the source of truth for identity/auth now. New signups (Google
// or email/password — Clerk handles both the same way) land here as
// "user.created", so every account always gets a local row, always starting
// on SubscriptionRegular.
package clerkwebhook

import (
	"context"
	"log"
	"net/http"
	"time"

	"lumo-user-svc/internal/models"
)

type userStore interface {
	FindByClerkID(ctx context.Context, clerkID string) (*models.User, error)
	Upsert(ctx context.Context, u *models.User) error
	ExistsByClerkID(ctx context.Context, clerkID string) (bool, error)
	DeleteByClerkID(ctx context.Context, clerkID string) error
}

// Service mirrors ClerkWebhookService.
type Service struct {
	users userStore
	sync  *PaymentsSync
}

func NewService(users userStore, sync *PaymentsSync) *Service {
	return &Service{users: users, sync: sync}
}

// Handle mirrors ClerkWebhookService.handle.
func (s *Service) Handle(ctx context.Context, event ClerkWebhookEvent) error {
	if event.Type == "" || event.Data.ID == "" {
		return nil
	}

	switch event.Type {
	case "user.created", "user.updated":
		return s.upsertUser(ctx, event.Data)
	case "user.deleted":
		return s.deleteUser(ctx, event.Data.ID)
	default:
		return nil // not something we track
	}
}

func (s *Service) upsertUser(ctx context.Context, data ClerkUserData) error {
	existing, err := s.users.FindByClerkID(ctx, data.ID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	user := existing
	if user == nil {
		user = &models.User{
			ClerkID:          data.ID,
			SubscriptionType: models.SubscriptionRegular,
			CreatedAt:        now,
		}
	}

	// primaryEmail() can legitimately be "" for a malformed/edge-case Clerk
	// payload with no email addresses at all — the Java entity's email
	// column is NOT NULL, so that case would fail the INSERT/UPDATE there;
	// here it's simply stored as an empty string rather than rejected,
	// since Clerk always supplies an email for a real account in practice.
	displayName := data.FullName()
	user.Email = data.PrimaryEmail()
	user.DisplayName = &displayName
	user.ProfileImageURL = &data.ImageURL
	user.UpdatedAt = now

	return s.users.Upsert(ctx, user)
}

func (s *Service) deleteUser(ctx context.Context, clerkID string) error {
	// deleteById() throws if the row doesn't exist in the Java version —
	// which happens for real whenever a user is deleted before their
	// "user.created" ever landed (e.g. it arrived while this service was
	// down or misconfigured) — so this has to be a no-op rather than an
	// error in that case, same here.
	exists, err := s.users.ExistsByClerkID(ctx, clerkID)
	if err != nil {
		return err
	}
	if exists {
		if err := s.users.DeleteByClerkID(ctx, clerkID); err != nil {
			return err
		}
	}

	// Unconditional, unlike the local delete above — lumo-payments-svc
	// might have a row even if this service's own one was somehow missing
	// (or vice versa), and its delete is already a no-op for a clerkId it
	// doesn't know about, so there's no need to gate this on the check above.
	s.sync.DeleteSubscription(ctx, clerkID)
	return nil
}

// PaymentsSync mirrors PaymentsSyncService: tells lumo-payments-svc — via
// lumo-api-gateway, same as every other cross-service call in this system —
// to drop its own copy of a Clerk account's billing record once Service
// deletes it here. Otherwise lumo-payments-svc would keep an orphaned
// `subscriptions` row for someone who no longer has an account at all.
type PaymentsSync struct {
	http           *http.Client
	gatewayURL     string
	internalSecret string
}

func NewPaymentsSync(gatewayURL, internalSecret string) *PaymentsSync {
	return &PaymentsSync{
		http:           &http.Client{Timeout: 30 * time.Second},
		gatewayURL:     gatewayURL,
		internalSecret: internalSecret,
	}
}

// DeleteSubscription is best-effort: the Clerk account is already gone from
// this service's own `users` table regardless of whether this call
// succeeds — a failure here just leaves a stale row in lumo-payments-svc (a
// data-hygiene issue, not a security or billing one), so it's logged, not
// propagated as an error.
func (p *PaymentsSync) DeleteSubscription(ctx context.Context, clerkID string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.gatewayURL+"/internal/subscriptions/"+clerkID, nil)
	if err != nil {
		log.Printf("Failed to build request to delete lumo-payments-svc's subscription record for deleted clerkId=%s: %v", clerkID, err)
		return
	}
	req.Header.Set("X-Internal-Api-Key", p.internalSecret)

	resp, err := p.http.Do(req)
	if err != nil {
		log.Printf("Failed to delete lumo-payments-svc's subscription record for deleted clerkId=%s — a stale row may remain: %v", clerkID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		log.Printf("Failed to delete lumo-payments-svc's subscription record for deleted clerkId=%s — a stale row may remain: status %d", clerkID, resp.StatusCode)
	}
}
