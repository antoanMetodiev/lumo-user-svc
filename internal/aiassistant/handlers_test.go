package aiassistant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/pashagolub/pgxmock/v4"

	"lumo-user-svc/internal/gatewayauth"
)

const testInternalSecret = "test-internal-secret"

func newTestServer(t *testing.T) (http.Handler, pgxmock.PgxPoolIface) {
	t.Helper()
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	mux := http.NewServeMux()
	NewHandlers(svc).Register(mux)

	handler := gatewayauth.Middleware(testInternalSecret, []string{"/ai-assistant"}, nil)(mux)
	return handler, mock
}

func newAuthedRequest(method, target, clerkID string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, testInternalSecret)
	req.Header.Set(gatewayauth.ClerkUserIDHeader, clerkID)
	return req
}

func TestGetApiKeyHappyPath(t *testing.T) {
	handler, mock := newTestServer(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT api_key FROM ai_assistant_api_keys WHERE is_active = true ORDER BY random() LIMIT 1")).
		WillReturnRows(pgxmock.NewRows([]string{"api_key"}).AddRow("test-key-1"))

	req := newAuthedRequest(http.MethodGet, "/ai-assistant/api-key", "user_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got apiKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.ApiKey != "test-key-1" {
		t.Fatalf("apiKey = %q, want %q", got.ApiKey, "test-key-1")
	}
}

func TestGetApiKeyNoActiveKeysReturns503(t *testing.T) {
	handler, mock := newTestServer(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT api_key FROM ai_assistant_api_keys WHERE is_active = true ORDER BY random() LIMIT 1")).
		WillReturnRows(pgxmock.NewRows([]string{"api_key"}))

	req := newAuthedRequest(http.MethodGet, "/ai-assistant/api-key", "user_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestGetApiKeyRequiresGatewayAuth(t *testing.T) {
	handler, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/ai-assistant/api-key", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
