package users

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"

	"lumo-user-svc/internal/gatewayauth"
	"lumo-user-svc/internal/models"
)

const handlersTestSecret = "internal-secret"

// newTestServer wraps Handlers with the real gatewayauth.Middleware, the
// same way cmd/server/main.go does, so tests exercise the actual
// X-Internal-Api-Key/X-Clerk-User-Id contract instead of poking an
// unexported context key from outside the gatewayauth package.
func newTestServer(svc *Service) http.Handler {
	mux := http.NewServeMux()
	NewHandlers(svc).Register(mux)
	return gatewayauth.Middleware(handlersTestSecret, []string{"/user/me"}, []string{"/internal"})(mux)
}

func TestGetCurrentUserFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating pgxmock pool: %v", err)
	}
	defer mock.Close()

	now := time.Now().UTC()
	rows := pgxmock.NewRows([]string{"clerk_id", "email", "display_name", "profile_image_url", "subscription_type", "created_at", "updated_at"}).
		AddRow("user_1", "a@example.com", nil, nil, models.SubscriptionRegular, now, now)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + userColumns + " FROM users WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(rows)

	svc := NewService(NewRepository(mock))
	srv := newTestServer(svc)

	req := httptest.NewRequest(http.MethodGet, "/user/me", nil)
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, handlersTestSecret)
	req.Header.Set(gatewayauth.ClerkUserIDHeader, "user_1")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `"clerkId":"user_1"`) {
		t.Errorf("body = %s, want it to contain the clerkId", rec.Body.String())
	}
}

func TestGetCurrentUserNotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating pgxmock pool: %v", err)
	}
	defer mock.Close()

	rows := pgxmock.NewRows([]string{"clerk_id", "email", "display_name", "profile_image_url", "subscription_type", "created_at", "updated_at"})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + userColumns + " FROM users WHERE clerk_id = $1")).
		WithArgs("ghost").
		WillReturnRows(rows)

	svc := NewService(NewRepository(mock))
	srv := newTestServer(svc)

	req := httptest.NewRequest(http.MethodGet, "/user/me", nil)
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, handlersTestSecret)
	req.Header.Set(gatewayauth.ClerkUserIDHeader, "ghost")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (Clerk webhook hasn't synced this account yet)", rec.Code)
	}
}

func TestGetCurrentUserWithoutGatewayHeadersIsUnauthorized(t *testing.T) {
	svc := NewService(NewRepository(nil))
	srv := newTestServer(svc)

	req := httptest.NewRequest(http.MethodGet, "/user/me", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — gatewayauth.Middleware should reject before the handler ever touches the (nil) pool", rec.Code)
	}
}

func TestUpdateSubscriptionMalformedJSONIs400(t *testing.T) {
	svc := NewService(NewRepository(nil))
	srv := newTestServer(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/users/user_1/subscription", bytes.NewReader([]byte("not json")))
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, handlersTestSecret)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestUpdateSubscriptionInvalidEnumIs400 checks the explicit
// SubscriptionType.Valid() rejection — this must happen before any DB call,
// mirroring Jackson's default enum-deserialization failure.
func TestUpdateSubscriptionInvalidEnumIs400(t *testing.T) {
	svc := NewService(NewRepository(nil))
	srv := newTestServer(svc)

	body := `{"subscriptionType":"NOT_A_REAL_PLAN"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/users/user_1/subscription", strings.NewReader(body))
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, handlersTestSecret)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "REGULAR_USER or PRO_USER") {
		t.Errorf("body = %q, want the explicit validation message", rec.Body.String())
	}
}

func TestUpdateSubscriptionSuccess(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating pgxmock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET subscription_type = $2, updated_at = $3 WHERE clerk_id = $1")).
		WithArgs("user_1", models.SubscriptionPro, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	svc := NewService(NewRepository(mock))
	srv := newTestServer(svc)

	body := `{"subscriptionType":"PRO_USER"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/users/user_1/subscription", strings.NewReader(body))
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, handlersTestSecret)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateSubscriptionNotFoundClerkID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating pgxmock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET subscription_type = $2, updated_at = $3 WHERE clerk_id = $1")).
		WithArgs("ghost", models.SubscriptionPro, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	svc := NewService(NewRepository(mock))
	srv := newTestServer(svc)

	body := `{"subscriptionType":"PRO_USER"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/users/ghost/subscription", strings.NewReader(body))
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, handlersTestSecret)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when the clerkId hasn't synced into our DB yet", rec.Code)
	}
}

// TestUpdateSubscriptionSkipsGatewayauthCheck confirms /internal/... paths
// don't require X-Clerk-User-Id (only the shared secret) — see
// gatewayauth's own package doc comment on the two kinds of protected paths.
func TestUpdateSubscriptionSkipsGatewayauthCheck(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating pgxmock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET subscription_type = $2, updated_at = $3 WHERE clerk_id = $1")).
		WithArgs("user_1", models.SubscriptionPro, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	svc := NewService(NewRepository(mock))
	srv := newTestServer(svc)

	body := `{"subscriptionType":"PRO_USER"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/users/user_1/subscription", strings.NewReader(body))
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, handlersTestSecret)
	// Deliberately no X-Clerk-User-Id header set.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 — internal paths must not require X-Clerk-User-Id", rec.Code)
	}
}

func TestUpdateSubscriptionWithoutInternalKeyIsUnauthorized(t *testing.T) {
	svc := NewService(NewRepository(nil))
	srv := newTestServer(svc)

	body := `{"subscriptionType":"PRO_USER"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/users/user_1/subscription", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
