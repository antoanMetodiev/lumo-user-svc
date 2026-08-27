package playlists

import (
	"encoding/json"
	"errors"
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
// mux, exactly like cmd/server/main.go does for "/playlists" — this is the
// only way to populate gatewayauth.ClerkUserID for a handler test, since its
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

	handler := gatewayauth.Middleware(testInternalSecret, []string{"/playlists"}, nil)(mux)
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

// TestMalformedUUIDPathParamReturns400 covers every route that parses a
// {id} path segment as a uuid.UUID — a bad value must 400 before any
// repository call happens (repoMock/friendsMock have no expectations set,
// so an accidental fall-through would surface as an unmet/unexpected-call
// error too).
func TestMalformedUUIDPathParamReturns400(t *testing.T) {
	cases := []struct {
		name, method, path string
	}{
		{"update", http.MethodPatch, "/playlists/not-a-uuid"},
		{"delete", http.MethodDelete, "/playlists/not-a-uuid"},
		{"listItems", http.MethodGet, "/playlists/not-a-uuid/items"},
		{"addItem", http.MethodPost, "/playlists/not-a-uuid/items"},
		{"removeItem", http.MethodDelete, "/playlists/not-a-uuid/items/rec_1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, _ := newTestServer(t)

			req := newAuthedRequest(tc.method, tc.path, "user_1", "")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestCreateBlankNameReturns400(t *testing.T) {
	handler, _, _ := newTestServer(t)

	req := newAuthedRequest(http.MethodPost, "/playlists", "user_1", `{"name":"   ","isPublic":false}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "name: must not be blank" {
		t.Fatalf("body = %q, want the exact blank-name message", got)
	}
}

func TestUpdateBlankNameReturns400(t *testing.T) {
	handler, _, _ := newTestServer(t)

	id := uuid.New()
	req := newAuthedRequest(http.MethodPatch, "/playlists/"+id.String(), "user_1", `{"name":"","isPublic":false}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "name: must not be blank" {
		t.Fatalf("body = %q, want the exact blank-name message", got)
	}
}

func TestAddItemBlankRecordIDReturns400(t *testing.T) {
	handler, _, _ := newTestServer(t)

	id := uuid.New()
	req := newAuthedRequest(http.MethodPost, "/playlists/"+id.String()+"/items", "user_1", `{"recordId":" "}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "recordId: must not be blank" {
		t.Fatalf("body = %q, want the exact blank-recordId message", got)
	}
}

// --- writeServiceErr direct-call coverage -----------------------------------

func TestWriteServiceErrNotFoundMapsTo404(t *testing.T) {
	rec := httptest.NewRecorder()
	writeServiceErr(rec, ErrNotFound)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestWriteServiceErrItemLimitReachedMapsTo409WithExactMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	writeServiceErr(rec, ErrItemLimitReached)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "a playlist can have at most 30 titles" {
		t.Fatalf("body = %q, want the exact capacity message", got)
	}
}

func TestWriteServiceErrOtherMapsTo500(t *testing.T) {
	rec := httptest.NewRecorder()
	writeServiceErr(rec, errors.New("boom"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// TestListItemsNotFoundEndToEnd wires writeServiceErr's ErrNotFound branch
// through the real listItems handler, not just a direct call.
func TestListItemsNotFoundEndToEnd(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}))

	req := newAuthedRequest(http.MethodGet, "/playlists/"+id.String()+"/items", "user_1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestAddItemLimitReachedEndToEnd wires writeServiceErr's ErrItemLimitReached
// branch through the real addItem handler, not just a direct call.
func TestAddItemLimitReachedEndToEnd(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "user_1", false))
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM playlist_items WHERE playlist_id = $1 AND record_id = $2")).
		WithArgs(id, "rec_new").
		WillReturnRows(pgxmock.NewRows(playlistItemCols))
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM playlist_items WHERE playlist_id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(maxItemsPerPlaylist)))

	req := newAuthedRequest(http.MethodPost, "/playlists/"+id.String()+"/items", "user_1", `{"recordId":"rec_new"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "a playlist can have at most 30 titles" {
		t.Fatalf("body = %q, want the exact capacity message", got)
	}
}

// --- happy paths -------------------------------------------------------------

func TestListMineHappyPath(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	now := time.Now().UTC()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT " + playlistWithCountColumns + " FROM playlists p WHERE p.owner_id = $1 ORDER BY p.created_at DESC")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows(playlistCols).AddRow(uuid.New(), "user_1", "Favorites", false, 0, now, now))

	req := newAuthedRequest(http.MethodGet, "/playlists", "user_1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []PlaylistDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Favorites" {
		t.Fatalf("unexpected response body: %+v", got)
	}
}

func TestCreateHappyPath(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	repoMock.ExpectExec(regexp.QuoteMeta("INSERT INTO playlists (id, owner_id, name, is_public, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6)")).
		WithArgs(pgxmock.AnyArg(), "user_1", "Favorites", true, pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	req := newAuthedRequest(http.MethodPost, "/playlists", "user_1", `{"name":"Favorites","isPublic":true}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got models.Playlist
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.OwnerID != "user_1" || got.Name != "Favorites" || !got.IsPublic || got.ID == uuid.Nil {
		t.Fatalf("unexpected response body: %+v", got)
	}
}

func TestUpdateHappyPath(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	id := uuid.New()
	repoMock.ExpectExec(regexp.QuoteMeta("UPDATE playlists SET name = $1, is_public = $2, updated_at = $3 WHERE id = $4 AND owner_id = $5")).
		WithArgs("New Name", false, pgxmock.AnyArg(), id, "user_1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	req := newAuthedRequest(http.MethodPatch, "/playlists/"+id.String(), "user_1", `{"name":"New Name","isPublic":false}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestUpdateNotFoundReturns404(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	id := uuid.New()
	repoMock.ExpectExec(regexp.QuoteMeta("UPDATE playlists SET name = $1, is_public = $2, updated_at = $3 WHERE id = $4 AND owner_id = $5")).
		WithArgs("New Name", false, pgxmock.AnyArg(), id, "user_1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	req := newAuthedRequest(http.MethodPatch, "/playlists/"+id.String(), "user_1", `{"name":"New Name","isPublic":false}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeleteHappyPath(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	id := uuid.New()
	repoMock.ExpectExec(regexp.QuoteMeta("DELETE FROM playlists WHERE id = $1 AND owner_id = $2")).
		WithArgs(id, "user_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	req := newAuthedRequest(http.MethodDelete, "/playlists/"+id.String(), "user_1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestListItemsHappyPath(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "user_1", false))

	now := time.Now().UTC()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM playlist_items WHERE playlist_id = $1 ORDER BY added_at DESC")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows(playlistItemCols).AddRow(uuid.New(), id, "rec_1", nil, nil, nil, nil, nil, nil, now))

	req := newAuthedRequest(http.MethodGet, "/playlists/"+id.String()+"/items", "user_1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []models.PlaylistItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].RecordID != "rec_1" {
		t.Fatalf("unexpected response body: %+v", got)
	}
}

func TestAddItemHappyPath(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "user_1", false))
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM playlist_items WHERE playlist_id = $1 AND record_id = $2")).
		WithArgs(id, "rec_new").
		WillReturnRows(pgxmock.NewRows(playlistItemCols))
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM playlist_items WHERE playlist_id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(0)))
	repoMock.ExpectExec(regexp.QuoteMeta(
		`INSERT INTO playlist_items (id, playlist_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, added_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`)).
		WithArgs(pgxmock.AnyArg(), id, "rec_new", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	req := newAuthedRequest(http.MethodPost, "/playlists/"+id.String()+"/items", "user_1", `{"recordId":"rec_new"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got models.PlaylistItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.PlaylistID != id || got.RecordID != "rec_new" {
		t.Fatalf("unexpected response body: %+v", got)
	}
}

func TestRemoveItemHappyPath(t *testing.T) {
	handler, repoMock, _ := newTestServer(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "user_1", false))
	repoMock.ExpectExec(regexp.QuoteMeta("DELETE FROM playlist_items WHERE playlist_id = $1 AND record_id = $2")).
		WithArgs(id, "rec_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	req := newAuthedRequest(http.MethodDelete, "/playlists/"+id.String()+"/items/rec_1", "user_1", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestListForUserHappyPath(t *testing.T) {
	handler, repoMock, friendsMock := newTestServer(t)

	friendsMock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)")).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	now := time.Now().UTC()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT " + playlistWithCountColumns + " FROM playlists p WHERE p.owner_id = $1 AND p.is_public = TRUE ORDER BY p.created_at DESC")).
		WithArgs("user_b").
		WillReturnRows(pgxmock.NewRows(playlistCols).AddRow(uuid.New(), "user_b", "Public List", true, 0, now, now))

	req := newAuthedRequest(http.MethodGet, "/playlists/user_b/visible", "user_a", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []PlaylistDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Public List" {
		t.Fatalf("unexpected response body: %+v", got)
	}
}
