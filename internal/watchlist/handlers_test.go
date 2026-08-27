package watchlist

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	"lumo-user-svc/internal/gatewayauth"
	"lumo-user-svc/internal/models"
)

const testInternalSecret = "test-internal-secret"

// newTestHandler wraps the real handlers in the real gatewayauth.Middleware,
// exactly like cmd/server/main.go does, rather than faking the unexported
// clerk-user-id context key from another package.
func newTestHandler(svc *Service) http.Handler {
	mux := http.NewServeMux()
	NewHandlers(svc).Register(mux)
	return gatewayauth.Middleware(testInternalSecret, []string{"/watchlist"}, nil)(mux)
}

func newAuthedRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, testInternalSecret)
	req.Header.Set(gatewayauth.ClerkUserIDHeader, "user_1")
	return req
}

func TestAddToWatchlistBlankRecordIDReturns400(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	req := newAuthedRequest(t, http.MethodPost, "/watchlist", `{"recordId":"   "}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "recordId: must not be blank" {
		t.Fatalf("body = %q, want %q", body, "recordId: must not be blank")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected no repository calls for a blank recordId, got: %v", err)
	}
}

func TestAddToWatchlistMalformedJSONBodyReturns400(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	req := newAuthedRequest(t, http.MethodPost, "/watchlist", `{not valid json`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected no repository calls for a malformed body, got: %v", err)
	}
}

// TestAddToWatchlistReturns200NotCreated pins down the deliberate
// Java-parity quirk noted in handlers.go: WatchlistController never
// overrode @ResponseStatus, so a successful add responds 200, not 201.
func TestAddToWatchlistReturns200NotCreated(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnRows(pgxmock.NewRows(itemColumnNames))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM watchlist_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectExec(regexp.QuoteMeta(watchlistInsertSQL)).
		WithArgs(pgxmock.AnyArg(), "user_1", "rec_1", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	req := newAuthedRequest(t, http.MethodPost, "/watchlist", `{"recordId":"rec_1"}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (Java parity: no @ResponseStatus override)", rec.Code, http.StatusOK)
	}

	var got models.WatchlistItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	if got.RecordID != "rec_1" {
		t.Fatalf("unexpected response body: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRemoveFromWatchlistReturns204(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	req := newAuthedRequest(t, http.MethodDelete, "/watchlist/rec_1", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetWatchlistHappyPathReturns200JSONArray(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	now := time.Now().UTC()
	rows := pgxmock.NewRows(itemColumnNames).
		AddRow(uuid.New(), "user_1", "rec_1", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), now).
		AddRow(uuid.New(), "user_1", "rec_2", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM watchlist_items WHERE clerk_id = $1 ORDER BY added_at DESC")).
		WithArgs("user_1").
		WillReturnRows(rows)

	req := newAuthedRequest(t, http.MethodGet, "/watchlist", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var got []models.WatchlistItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(got), got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
