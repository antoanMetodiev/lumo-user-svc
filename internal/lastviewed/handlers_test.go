package lastviewed

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
	return gatewayauth.Middleware(testInternalSecret, []string{"/last-viewed"}, nil)(mux)
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

func TestRecordViewBlankTypeReturns400(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	req := newAuthedRequest(t, http.MethodPost, "/last-viewed", `{"type":"  ","title":"Some Movie"}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "type: must not be blank" {
		t.Fatalf("body = %q, want %q", body, "type: must not be blank")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected no repository calls for a blank type, got: %v", err)
	}
}

func TestRecordViewBlankTitleReturns400(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	req := newAuthedRequest(t, http.MethodPost, "/last-viewed", `{"type":"movie","title":"   "}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "title: must not be blank" {
		t.Fatalf("body = %q, want %q", body, "title: must not be blank")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected no repository calls for a blank title, got: %v", err)
	}
}

// TestRecordViewBothBlankReturnsTypeErrorFirst confirms the handler checks
// type before title: a request with both fields blank must surface the
// type error specifically, not the title one.
func TestRecordViewBothBlankReturnsTypeErrorFirst(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	req := newAuthedRequest(t, http.MethodPost, "/last-viewed", `{"type":"","title":""}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "type: must not be blank" {
		t.Fatalf("body = %q, want %q (type must be checked before title)", body, "type: must not be blank")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected no repository calls, got: %v", err)
	}
}

func TestRecordViewMalformedJSONBodyReturns400(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	req := newAuthedRequest(t, http.MethodPost, "/last-viewed", `{not valid json`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected no repository calls for a malformed body, got: %v", err)
	}
}

// TestRecordViewReturns200NotCreated pins down the deliberate Java-parity
// quirk noted in handlers.go: LastViewedController never overrode
// @ResponseStatus, so a successful record responds 200, not 201.
func TestRecordViewReturns200NotCreated(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM last_viewed_items WHERE clerk_id = $1 AND title = $2")).
		WithArgs("user_1", "Some Movie").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM last_viewed_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(0)))
	mock.ExpectExec(regexp.QuoteMeta(lastViewedInsertSQL)).
		WithArgs(pgxmock.AnyArg(), "user_1", "movie", (*string)(nil), (*string)(nil), (*string)(nil), "Some Movie", (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	req := newAuthedRequest(t, http.MethodPost, "/last-viewed", `{"type":"movie","title":"Some Movie"}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (Java parity: no @ResponseStatus override)", rec.Code, http.StatusOK)
	}

	var got models.LastViewedItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	if got.Title != "Some Movie" {
		t.Fatalf("unexpected response body: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetLastViewedHappyPathReturns200JSONArray(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)
	h := newTestHandler(svc)

	now := time.Now().UTC()
	rows := pgxmock.NewRows(itemColumnNames).
		AddRow(uuid.New(), "user_1", "movie", (*string)(nil), (*string)(nil), (*string)(nil), "Title A", (*string)(nil), (*string)(nil), (*string)(nil), now).
		AddRow(uuid.New(), "user_1", "movie", (*string)(nil), (*string)(nil), (*string)(nil), "Title B", (*string)(nil), (*string)(nil), (*string)(nil), now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM last_viewed_items WHERE clerk_id = $1 ORDER BY viewed_at DESC")).
		WithArgs("user_1").
		WillReturnRows(rows)

	req := newAuthedRequest(t, http.MethodGet, "/last-viewed", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var got []models.LastViewedItem
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
