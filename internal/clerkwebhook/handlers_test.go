package clerkwebhook

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"lumo-user-svc/internal/models"
)

const testWebhookSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"

// newSignedRequest builds a POST /webhooks/clerk request carrying valid Svix
// headers for payload, reusing the `sign` helper already defined in
// svix_test.go (same package, same test binary — no need to duplicate it).
func newSignedRequest(t *testing.T, secret, payload string) *http.Request {
	t.Helper()
	id := "msg_test123"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig := sign(t, secret, id, timestamp, payload)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", strings.NewReader(payload))
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", timestamp)
	req.Header.Set("svix-signature", sig)
	return req
}

func TestHandleClerkWebhookValidSignatureReturns200(t *testing.T) {
	store := &fakeUserStore{
		existsByClerkID: func(ctx context.Context, clerkID string) (bool, error) { return true, nil },
	}
	sync, _ := newTestPaymentsSync(t)
	h := NewHandlers(testWebhookSecret, NewService(store, sync))

	payload := `{"type":"user.deleted","data":{"id":"user_1"}}`
	req := newSignedRequest(t, testWebhookSecret, payload)
	rec := httptest.NewRecorder()

	h.handleClerkWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if store.existsCalls != 1 {
		t.Fatalf("expected the event to reach the store, got existsCalls = %d", store.existsCalls)
	}
}

func TestHandleClerkWebhookBadSignatureReturns401AndSkipsStore(t *testing.T) {
	store := &fakeUserStore{}
	sync, _ := newTestPaymentsSync(t)
	h := NewHandlers(testWebhookSecret, NewService(store, sync))

	payload := `{"type":"user.deleted","data":{"id":"user_1"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", strings.NewReader(payload))
	req.Header.Set("svix-id", "msg_test123")
	req.Header.Set("svix-timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("svix-signature", "v1,bm90dGhlcmlnaHRzaWc=") // well-formed but wrong

	rec := httptest.NewRecorder()
	h.handleClerkWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if store.findCalls != 0 || store.existsCalls != 0 || store.upsertCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("expected no store calls for a bad signature, got store = %+v", store)
	}
}

func TestHandleClerkWebhookMissingSignatureHeadersReturns401(t *testing.T) {
	store := &fakeUserStore{}
	sync, _ := newTestPaymentsSync(t)
	h := NewHandlers(testWebhookSecret, NewService(store, sync))

	payload := `{"type":"user.deleted","data":{"id":"user_1"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", strings.NewReader(payload))
	// No svix-* headers at all.

	rec := httptest.NewRecorder()
	h.handleClerkWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if store.findCalls != 0 || store.existsCalls != 0 {
		t.Fatalf("expected no store calls for missing signature headers, got store = %+v", store)
	}
}

// TestHandleClerkWebhookMalformedJSONBody checks what the handler actually
// returns for a body that passes signature verification (it's signed
// correctly, byte-for-byte) but isn't valid JSON — handlers.go returns 500
// for a json.Unmarshal failure, not 400.
func TestHandleClerkWebhookMalformedJSONBody(t *testing.T) {
	store := &fakeUserStore{}
	sync, _ := newTestPaymentsSync(t)
	h := NewHandlers(testWebhookSecret, NewService(store, sync))

	payload := `{not valid json`
	req := newSignedRequest(t, testWebhookSecret, payload)
	rec := httptest.NewRecorder()

	h.handleClerkWebhook(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if store.findCalls != 0 || store.existsCalls != 0 {
		t.Fatalf("expected no store calls for a malformed body, got store = %+v", store)
	}
}

func TestHandleClerkWebhookServiceErrorReturns500(t *testing.T) {
	store := &fakeUserStore{
		findByClerkID: func(ctx context.Context, clerkID string) (*models.User, error) {
			return nil, errors.New("db exploded")
		},
	}
	sync, _ := newTestPaymentsSync(t)
	h := NewHandlers(testWebhookSecret, NewService(store, sync))

	payload := `{"type":"user.created","data":{"id":"user_1"}}`
	req := newSignedRequest(t, testWebhookSecret, payload)
	rec := httptest.NewRecorder()

	h.handleClerkWebhook(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
