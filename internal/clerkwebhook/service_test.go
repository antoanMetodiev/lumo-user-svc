package clerkwebhook

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lumo-user-svc/internal/models"
)

// fakeUserStore is a hand-written stand-in for userStore — no mocking
// library needed since the interface is tiny and local to this package.
type fakeUserStore struct {
	findByClerkID   func(ctx context.Context, clerkID string) (*models.User, error)
	existsByClerkID func(ctx context.Context, clerkID string) (bool, error)

	upsertCalls int
	upserted    *models.User
	upsertErr   error

	deleteCalls int
	deletedID   string
	deleteErr   error

	existsCalls int
	findCalls   int
}

func (f *fakeUserStore) FindByClerkID(ctx context.Context, clerkID string) (*models.User, error) {
	f.findCalls++
	if f.findByClerkID != nil {
		return f.findByClerkID(ctx, clerkID)
	}
	return nil, nil
}

func (f *fakeUserStore) Upsert(ctx context.Context, u *models.User) error {
	f.upsertCalls++
	f.upserted = u
	return f.upsertErr
}

func (f *fakeUserStore) ExistsByClerkID(ctx context.Context, clerkID string) (bool, error) {
	f.existsCalls++
	if f.existsByClerkID != nil {
		return f.existsByClerkID(ctx, clerkID)
	}
	return false, nil
}

func (f *fakeUserStore) DeleteByClerkID(ctx context.Context, clerkID string) error {
	f.deleteCalls++
	f.deletedID = clerkID
	return f.deleteErr
}

// newTestPaymentsSync builds a PaymentsSync pointed at a throwaway,
// always-succeeding gateway — sufficient for tests that only care about
// Service's own behavior, not the payments call itself.
func newTestPaymentsSync(t *testing.T) (*PaymentsSync, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return NewPaymentsSync(server.URL, "secret"), &calls
}

func TestHandleIgnoresBlankEventType(t *testing.T) {
	store := &fakeUserStore{}
	sync, _ := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "", Data: ClerkUserData{ID: "user_1"}})
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if store.findCalls != 0 || store.upsertCalls != 0 || store.existsCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("expected no store calls for blank event type, got store = %+v", store)
	}
}

func TestHandleIgnoresBlankEventDataID(t *testing.T) {
	store := &fakeUserStore{}
	sync, _ := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.created", Data: ClerkUserData{ID: ""}})
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if store.findCalls != 0 || store.upsertCalls != 0 {
		t.Fatalf("expected no store calls for blank data.id, got store = %+v", store)
	}
}

func TestHandleIgnoresUnknownEventType(t *testing.T) {
	store := &fakeUserStore{}
	sync, _ := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.pwned", Data: ClerkUserData{ID: "user_1"}})
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if store.findCalls != 0 || store.upsertCalls != 0 || store.existsCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("expected no store calls for an untracked event type, got store = %+v", store)
	}
}

func TestHandleDispatchesUserCreatedToUpsert(t *testing.T) {
	store := &fakeUserStore{}
	sync, _ := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	data := ClerkUserData{ID: "user_1", FirstName: "Ada", LastName: "Lovelace", ImageURL: "https://img/a.png"}
	if err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.created", Data: data}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.upsertCalls != 1 {
		t.Fatalf("expected upsert to be called once, got %d", store.upsertCalls)
	}
}

func TestHandleDispatchesUserUpdatedToUpsert(t *testing.T) {
	store := &fakeUserStore{}
	sync, _ := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	data := ClerkUserData{ID: "user_1", FirstName: "Ada"}
	if err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.updated", Data: data}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.upsertCalls != 1 {
		t.Fatalf("expected upsert to be called once, got %d", store.upsertCalls)
	}
}

func TestHandleDispatchesUserDeletedToDelete(t *testing.T) {
	store := &fakeUserStore{
		existsByClerkID: func(ctx context.Context, clerkID string) (bool, error) { return true, nil },
	}
	sync, _ := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	if err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.deleted", Data: ClerkUserData{ID: "user_1"}}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.existsCalls != 1 || store.deleteCalls != 1 {
		t.Fatalf("expected exists+delete to be called once each, got store = %+v", store)
	}
}

// TestUpsertUserNewUserSetsDefaults verifies a brand-new row (FindByClerkID
// returns nil, nil) gets SubscriptionRegular + a fresh CreatedAt, in addition
// to the fields every upsert sets.
func TestUpsertUserNewUserSetsDefaults(t *testing.T) {
	store := &fakeUserStore{
		findByClerkID: func(ctx context.Context, clerkID string) (*models.User, error) { return nil, nil },
	}
	sync, _ := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	before := time.Now().UTC()
	data := ClerkUserData{
		ID:                    "user_new",
		FirstName:             "Ada",
		LastName:              "Lovelace",
		ImageURL:              "https://img/new.png",
		PrimaryEmailAddressID: "e1",
		EmailAddresses:        []ClerkEmailAddress{{ID: "e1", EmailAddress: "ada@example.com"}},
	}

	if err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.created", Data: data}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	after := time.Now().UTC()

	got := store.upserted
	if got == nil {
		t.Fatal("expected Upsert to be called with a user")
	}
	if got.ClerkID != "user_new" {
		t.Errorf("ClerkID = %q, want %q", got.ClerkID, "user_new")
	}
	if got.SubscriptionType != models.SubscriptionRegular {
		t.Errorf("SubscriptionType = %q, want %q", got.SubscriptionType, models.SubscriptionRegular)
	}
	if got.CreatedAt.Before(before) || got.CreatedAt.After(after) {
		t.Errorf("CreatedAt = %v, want between %v and %v", got.CreatedAt, before, after)
	}
	if got.UpdatedAt.Before(before) || got.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt = %v, want between %v and %v", got.UpdatedAt, before, after)
	}
	if got.Email != "ada@example.com" {
		t.Errorf("Email = %q, want %q", got.Email, "ada@example.com")
	}
	if got.DisplayName == nil || *got.DisplayName != "Ada Lovelace" {
		t.Errorf("DisplayName = %v, want %q", got.DisplayName, "Ada Lovelace")
	}
	if got.ProfileImageURL == nil || *got.ProfileImageURL != "https://img/new.png" {
		t.Errorf("ProfileImageURL = %v, want %q", got.ProfileImageURL, "https://img/new.png")
	}
}

// TestUpsertUserExistingUserPreservesIdentityFields verifies an existing row
// keeps its ClerkID/SubscriptionType/CreatedAt while Email/DisplayName/
// ProfileImageURL/UpdatedAt get overwritten with the incoming Clerk data.
func TestUpsertUserExistingUserPreservesIdentityFields(t *testing.T) {
	originalCreatedAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	existing := &models.User{
		ClerkID:          "user_existing",
		Email:            "old@example.com",
		DisplayName:      strPtr("Old Name"),
		ProfileImageURL:  strPtr("https://img/old.png"),
		SubscriptionType: models.SubscriptionPro,
		CreatedAt:        originalCreatedAt,
		UpdatedAt:        originalCreatedAt,
	}
	store := &fakeUserStore{
		findByClerkID: func(ctx context.Context, clerkID string) (*models.User, error) { return existing, nil },
	}
	sync, _ := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	before := time.Now().UTC()
	data := ClerkUserData{
		ID:                    "user_existing",
		FirstName:             "New",
		LastName:              "Name",
		ImageURL:              "https://img/new.png",
		PrimaryEmailAddressID: "e1",
		EmailAddresses:        []ClerkEmailAddress{{ID: "e1", EmailAddress: "new@example.com"}},
	}

	if err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.updated", Data: data}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	after := time.Now().UTC()

	got := store.upserted
	if got == nil {
		t.Fatal("expected Upsert to be called with a user")
	}

	// Preserved fields.
	if got.ClerkID != "user_existing" {
		t.Errorf("ClerkID = %q, want preserved %q", got.ClerkID, "user_existing")
	}
	if got.SubscriptionType != models.SubscriptionPro {
		t.Errorf("SubscriptionType = %q, want preserved %q", got.SubscriptionType, models.SubscriptionPro)
	}
	if !got.CreatedAt.Equal(originalCreatedAt) {
		t.Errorf("CreatedAt = %v, want preserved %v", got.CreatedAt, originalCreatedAt)
	}

	// Overwritten fields.
	if got.Email != "new@example.com" {
		t.Errorf("Email = %q, want %q", got.Email, "new@example.com")
	}
	if got.DisplayName == nil || *got.DisplayName != "New Name" {
		t.Errorf("DisplayName = %v, want %q", got.DisplayName, "New Name")
	}
	if got.ProfileImageURL == nil || *got.ProfileImageURL != "https://img/new.png" {
		t.Errorf("ProfileImageURL = %v, want %q", got.ProfileImageURL, "https://img/new.png")
	}
	if got.UpdatedAt.Before(before) || got.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt = %v, want between %v and %v", got.UpdatedAt, before, after)
	}
}

func TestUpsertUserPropagatesFindError(t *testing.T) {
	wantErr := errors.New("db exploded")
	store := &fakeUserStore{
		findByClerkID: func(ctx context.Context, clerkID string) (*models.User, error) { return nil, wantErr },
	}
	sync, _ := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.created", Data: ClerkUserData{ID: "user_1"}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle() error = %v, want %v", err, wantErr)
	}
	if store.upsertCalls != 0 {
		t.Fatalf("expected Upsert not to be called when FindByClerkID errors, got %d calls", store.upsertCalls)
	}
}

// TestDeleteUserMissingRowIsNoOp mirrors the Java-vs-Go note in service.go:
// unlike deleteById() throwing on a missing row, DeleteByClerkID must simply
// not be called at all when the row is already gone.
func TestDeleteUserMissingRowIsNoOp(t *testing.T) {
	store := &fakeUserStore{
		existsByClerkID: func(ctx context.Context, clerkID string) (bool, error) { return false, nil },
	}
	sync, syncCalls := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	if err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.deleted", Data: ClerkUserData{ID: "user_gone"}}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.deleteCalls != 0 {
		t.Fatalf("expected DeleteByClerkID not to be called for a missing row, got %d calls", store.deleteCalls)
	}
	if *syncCalls != 1 {
		t.Fatalf("expected PaymentsSync.DeleteSubscription to be called unconditionally, got %d calls", *syncCalls)
	}
}

func TestDeleteUserExistingRowDeletesAndSyncs(t *testing.T) {
	store := &fakeUserStore{
		existsByClerkID: func(ctx context.Context, clerkID string) (bool, error) { return true, nil },
	}
	sync, syncCalls := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	if err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.deleted", Data: ClerkUserData{ID: "user_1"}}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.deleteCalls != 1 || store.deletedID != "user_1" {
		t.Fatalf("expected DeleteByClerkID to be called with %q, got calls=%d id=%q", "user_1", store.deleteCalls, store.deletedID)
	}
	if *syncCalls != 1 {
		t.Fatalf("expected PaymentsSync.DeleteSubscription to be called once, got %d calls", *syncCalls)
	}
}

func TestDeleteUserExistsErrorSkipsSyncEntirely(t *testing.T) {
	wantErr := errors.New("db exploded")
	store := &fakeUserStore{
		existsByClerkID: func(ctx context.Context, clerkID string) (bool, error) { return false, wantErr },
	}
	sync, syncCalls := newTestPaymentsSync(t)
	svc := NewService(store, sync)

	err := svc.Handle(context.Background(), ClerkWebhookEvent{Type: "user.deleted", Data: ClerkUserData{ID: "user_1"}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle() error = %v, want %v", err, wantErr)
	}
	if store.deleteCalls != 0 {
		t.Fatalf("expected DeleteByClerkID not to be called when ExistsByClerkID errors, got %d calls", store.deleteCalls)
	}
	if *syncCalls != 0 {
		t.Fatalf("expected PaymentsSync.DeleteSubscription not to be called when ExistsByClerkID errors, got %d calls", *syncCalls)
	}
}

func strPtr(s string) *string { return &s }

// --- PaymentsSync.DeleteSubscription ---

func TestDeleteSubscriptionSendsExpectedRequest(t *testing.T) {
	var gotMethod, gotPath, gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotKey = r.Header.Get("X-Internal-Api-Key")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sync := NewPaymentsSync(server.URL, "top-secret")
	sync.DeleteSubscription(context.Background(), "user_123")

	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/internal/subscriptions/user_123" {
		t.Errorf("path = %q, want %q", gotPath, "/internal/subscriptions/user_123")
	}
	if gotKey != "top-secret" {
		t.Errorf("X-Internal-Api-Key = %q, want %q", gotKey, "top-secret")
	}
}

// TestDeleteSubscriptionToleratesErrorResponse checks the best-effort
// contract: a non-2xx response is logged, never surfaced as an error or
// panic (the method has no return value to assert on beyond "didn't blow
// up").
func TestDeleteSubscriptionToleratesErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	sync := NewPaymentsSync(server.URL, "secret")
	sync.DeleteSubscription(context.Background(), "user_123") // must not panic
}

// TestDeleteSubscriptionToleratesUnreachableServer checks the other
// best-effort branch: the gateway being completely unreachable (connection
// refused) must also not panic or block forever.
func TestDeleteSubscriptionToleratesUnreachableServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close() // closed immediately: nothing is listening on this URL anymore

	sync := NewPaymentsSync(server.URL, "secret")
	sync.DeleteSubscription(context.Background(), "user_123") // must not panic
}
