package likes

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

	"lumo-user-svc/internal/friends"
	"lumo-user-svc/internal/gatewayauth"
	"lumo-user-svc/internal/models"
)

const testInternalSecret = "test-internal-secret"

// newTestServer wires a real gatewayauth.Middleware in front of the real
// mux, exactly like cmd/server/main.go does for "/likes" — this is the only
// way to populate gatewayauth.ClerkUserID for a handler test, since its
// context key is unexported to this package.
func newTestServer(t *testing.T) (http.Handler, pgxmock.PgxPoolIface, pgxmock.PgxPoolIface) {
	t.Helper()
	repo, repoMock := newMockRepo(t)

	friendsMock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating friends pgxmock pool: %v", err)
	}
	t.Cleanup(friendsMock.Close)

	svc := NewService(repo, friends.NewRepository(friendsMock))

	mux := http.NewServeMux()
	NewHandlers(svc).Register(mux)

	handler := gatewayauth.Middleware(testInternalSecret, []string{"/likes"}, nil)(mux)
	return handler, repoMock, friendsMock
}

func newAuthedRequest(method, target, clerkID, body string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, testInternalSecret)
	req.Header.Set(gatewayauth.ClerkUserIDHeader, clerkID)
	return req
}

func TestAddLikeBlankRecordIDReturns400(t *testing.T) {
	handler, _, _ := newTestServer(t)

	req := newAuthedRequest(http.MethodPost, "/likes", "user_1", `{"recordId":"  "}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestAddLikeLimitReachedReturns409WithExactMessage(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_new").
		WillReturnRows(pgxmock.NewRows(itemCols))
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM liked_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(maxItems)))

	req := newAuthedRequest(http.MethodPost, "/likes", "user_1", `{"recordId":"rec_new"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "you can have at most 30 liked titles" {
		t.Fatalf("body = %q, want the exact capacity message", got)
	}
}

func TestAddLikeHappyPath(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_new").
		WillReturnRows(pgxmock.NewRows(itemCols))
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM liked_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(1)))
	repoMock.ExpectExec(regexp.QuoteMeta(
		`INSERT INTO liked_items (id, clerk_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, liked_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`)).
		WithArgs(pgxmock.AnyArg(), "user_1", "rec_new", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	req := newAuthedRequest(http.MethodPost, "/likes", "user_1", `{"recordId":"rec_new"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got models.LikedItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.ClerkID != "user_1" || got.RecordID != "rec_new" || got.ID == uuid.Nil {
		t.Fatalf("unexpected response body: %+v", got)
	}
}

func TestGetLikesHappyPath(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	now := time.Now().UTC()
	rows := pgxmock.NewRows(itemCols).
		AddRow(uuid.New(), "user_1", "rec_1", nil, nil, nil, nil, nil, nil, now)
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM liked_items WHERE clerk_id = $1 ORDER BY liked_at DESC")).
		WithArgs("user_1").
		WillReturnRows(rows)

	req := newAuthedRequest(http.MethodGet, "/likes", "user_1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []models.LikedItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].RecordID != "rec_1" {
		t.Fatalf("unexpected response body: %+v", got)
	}
}

func TestRemoveLikeHappyPath(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	repoMock.ExpectExec(regexp.QuoteMeta("DELETE FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	req := newAuthedRequest(http.MethodDelete, "/likes/rec_1", "user_1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestGetLikesForUserHappyPath(t *testing.T) {
	handler, repoMock, friendsMock := newTestServer(t)

	friendsMock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)")).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	now := time.Now().UTC()
	rows := pgxmock.NewRows(itemCols).
		AddRow(uuid.New(), "user_b", "rec_1", nil, nil, nil, nil, nil, nil, now)
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM liked_items WHERE clerk_id = $1 ORDER BY liked_at DESC")).
		WithArgs("user_b").
		WillReturnRows(rows)

	req := newAuthedRequest(http.MethodGet, "/likes/user/user_b", "user_a", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []models.LikedItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].RecordID != "rec_1" {
		t.Fatalf("unexpected response body: %+v", got)
	}
}

func TestGetLikesForUserNotFriendsReturnsEmptyList(t *testing.T) {
	handler, _, friendsMock := newTestServer(t)

	friendsMock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)")).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	req := newAuthedRequest(http.MethodGet, "/likes/user/user_b", "user_a", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want an empty JSON array", got)
	}
}
