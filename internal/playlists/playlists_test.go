package playlists

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	"lumo-user-svc/internal/friends"
	"lumo-user-svc/internal/models"
)

func newMockRepo(t *testing.T) (*Repository, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating pgxmock pool: %v", err)
	}
	t.Cleanup(mock.Close)
	return NewRepository(mock), mock
}

// newTestService wires a Service against two independent mock pools — one
// behind the playlists Repository, one behind friendsRepo — so expectations
// set on each are unambiguous about which dependency they belong to.
func newTestService(t *testing.T) (*Service, pgxmock.PgxPoolIface, pgxmock.PgxPoolIface) {
	t.Helper()
	repo, repoMock := newMockRepo(t)

	friendsMock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating friends pgxmock pool: %v", err)
	}
	t.Cleanup(friendsMock.Close)

	svc := NewService(repo, friends.NewRepository(friendsMock))
	return svc, repoMock, friendsMock
}

func strPtr(s string) *string { return &s }

var playlistCols = []string{"id", "owner_id", "name", "is_public", "item_count", "created_at", "updated_at"}
var playlistItemCols = []string{"id", "playlist_id", "record_id", "tmdb_id", "title", "poster_img_url", "tmdb_rating", "type", "video_id", "added_at"}

func TestRepositoryFindByOwner(t *testing.T) {
	repo, mock := newMockRepo(t)

	now := time.Now().UTC()
	id := uuid.New()
	rows := pgxmock.NewRows(playlistCols).AddRow(id, "user_1", "Favorites", false, 3, now, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + playlistWithCountColumns + " FROM playlists p WHERE p.owner_id = $1 ORDER BY p.created_at DESC")).
		WithArgs("user_1").
		WillReturnRows(rows)

	got, err := repo.FindByOwner(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Favorites" || got[0].ItemCount != 3 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestRepositoryFindPublicByOwnerEmpty(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + playlistWithCountColumns + " FROM playlists p WHERE p.owner_id = $1 AND p.is_public = TRUE ORDER BY p.created_at DESC")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows(playlistCols))

	got, err := repo.FindPublicByOwner(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no playlists, got %+v", got)
	}
}

func TestRepositoryFindPublicByOwnerPopulated(t *testing.T) {
	repo, mock := newMockRepo(t)

	now := time.Now().UTC()
	id := uuid.New()
	rows := pgxmock.NewRows(playlistCols).AddRow(id, "user_1", "Public List", true, 1, now, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + playlistWithCountColumns + " FROM playlists p WHERE p.owner_id = $1 AND p.is_public = TRUE ORDER BY p.created_at DESC")).
		WithArgs("user_1").
		WillReturnRows(rows)

	got, err := repo.FindPublicByOwner(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || !got[0].IsPublic || got[0].ID != id {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestRepositoryFindRowByIDFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	id := uuid.New()
	rows := pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "user_1", true)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(rows)

	got, err := repo.findRowByID(context.Background(), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.OwnerID != "user_1" || !got.IsPublic {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestRepositoryFindRowByIDNotFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	id := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}))

	got, err := repo.findRowByID(context.Background(), id)
	if err != nil {
		t.Fatalf("expected no error for a missing row, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for a missing row, got %+v", got)
	}
}

func TestRepositoryFindRowByIDQueryError(t *testing.T) {
	repo, mock := newMockRepo(t)

	id := uuid.New()
	boom := errors.New("connection reset")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnError(boom)

	_, err := repo.findRowByID(context.Background(), id)
	if !errors.Is(err, boom) {
		t.Fatalf("expected the underlying query error to propagate, got %v", err)
	}
}

func TestRepositoryInsertAssignsFreshUUID(t *testing.T) {
	repo, mock := newMockRepo(t)

	now := time.Now().UTC()
	p := &models.Playlist{OwnerID: "user_1", Name: "Favorites", IsPublic: false, CreatedAt: now, UpdatedAt: now}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO playlists (id, owner_id, name, is_public, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6)")).
		WithArgs(pgxmock.AnyArg(), p.OwnerID, p.Name, p.IsPublic, p.CreatedAt, p.UpdatedAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.Insert(context.Background(), p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID == uuid.Nil {
		t.Fatal("expected Insert to assign a fresh, non-zero UUID")
	}
}

func TestRepositoryUpdateRowMatched(t *testing.T) {
	repo, mock := newMockRepo(t)

	id := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE playlists SET name = $1, is_public = $2, updated_at = $3 WHERE id = $4 AND owner_id = $5")).
		WithArgs("New Name", true, pgxmock.AnyArg(), id, "user_1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	ok, err := repo.Update(context.Background(), id, "user_1", "New Name", true, time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected updated=true when a row matched")
	}
}

func TestRepositoryUpdateNoRowMatched(t *testing.T) {
	repo, mock := newMockRepo(t)

	id := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE playlists SET name = $1, is_public = $2, updated_at = $3 WHERE id = $4 AND owner_id = $5")).
		WithArgs("New Name", true, pgxmock.AnyArg(), id, "user_1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	ok, err := repo.Update(context.Background(), id, "user_1", "New Name", true, time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected updated=false when no row matched")
	}
}

func TestRepositoryDeleteRowMatched(t *testing.T) {
	repo, mock := newMockRepo(t)

	id := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM playlists WHERE id = $1 AND owner_id = $2")).
		WithArgs(id, "user_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	ok, err := repo.Delete(context.Background(), id, "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected deleted=true when a row matched")
	}
}

func TestRepositoryDeleteNoRowMatched(t *testing.T) {
	repo, mock := newMockRepo(t)

	id := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM playlists WHERE id = $1 AND owner_id = $2")).
		WithArgs(id, "user_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	ok, err := repo.Delete(context.Background(), id, "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected deleted=false when no row matched")
	}
}

func TestRepositoryFindItems(t *testing.T) {
	repo, mock := newMockRepo(t)

	playlistID := uuid.New()
	now := time.Now().UTC()
	rows := pgxmock.NewRows(playlistItemCols).
		AddRow(uuid.New(), playlistID, "rec_1", nil, nil, nil, nil, nil, nil, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM playlist_items WHERE playlist_id = $1 ORDER BY added_at DESC")).
		WithArgs(playlistID).
		WillReturnRows(rows)

	got, err := repo.FindItems(context.Background(), playlistID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].RecordID != "rec_1" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestRepositoryFindItemByRecordIDFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	playlistID := uuid.New()
	itemID := uuid.New()
	now := time.Now().UTC()
	rows := pgxmock.NewRows(playlistItemCols).
		AddRow(itemID, playlistID, "rec_1", nil, nil, nil, nil, nil, nil, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM playlist_items WHERE playlist_id = $1 AND record_id = $2")).
		WithArgs(playlistID, "rec_1").
		WillReturnRows(rows)

	got, err := repo.FindItemByRecordID(context.Background(), playlistID, "rec_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != itemID {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestRepositoryFindItemByRecordIDNotFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	playlistID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM playlist_items WHERE playlist_id = $1 AND record_id = $2")).
		WithArgs(playlistID, "ghost").
		WillReturnRows(pgxmock.NewRows(playlistItemCols))

	got, err := repo.FindItemByRecordID(context.Background(), playlistID, "ghost")
	if err != nil {
		t.Fatalf("expected no error for a missing row, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for a missing row, got %+v", got)
	}
}

func TestRepositoryInsertItemAssignsFreshUUID(t *testing.T) {
	repo, mock := newMockRepo(t)

	playlistID := uuid.New()
	it := &models.PlaylistItem{PlaylistID: playlistID, RecordID: "rec_1", AddedAt: time.Now().UTC()}

	mock.ExpectExec(regexp.QuoteMeta(
		`INSERT INTO playlist_items (id, playlist_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, added_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`)).
		WithArgs(pgxmock.AnyArg(), it.PlaylistID, it.RecordID, it.TmdbID, it.Title, it.PosterImgURL, it.TmdbRating, it.Type, it.VideoID, it.AddedAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.InsertItem(context.Background(), it); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if it.ID == uuid.Nil {
		t.Fatal("expected InsertItem to assign a fresh, non-zero UUID")
	}
}

func TestRepositoryDeleteItem(t *testing.T) {
	repo, mock := newMockRepo(t)

	playlistID := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM playlist_items WHERE playlist_id = $1 AND record_id = $2")).
		WithArgs(playlistID, "rec_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := repo.DeleteItem(context.Background(), playlistID, "rec_1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepositoryCountItems(t *testing.T) {
	repo, mock := newMockRepo(t)

	playlistID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM playlist_items WHERE playlist_id = $1")).
		WithArgs(playlistID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(7)))

	count, err := repo.CountItems(context.Background(), playlistID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 7 {
		t.Fatalf("count = %d, want 7", count)
	}
}

// --- Service.canView -------------------------------------------------------

func TestCanViewOwnerAlwaysTrue(t *testing.T) {
	svc, _, friendsMock := newTestService(t)

	pl := &playlistRow{ID: uuid.New(), OwnerID: "user_1", IsPublic: false}
	ok, err := svc.canView(context.Background(), "user_1", pl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected the owner to always be able to view their own playlist")
	}
	if err := friendsMock.ExpectationsWereMet(); err != nil {
		t.Errorf("owner check should not have queried friendsRepo: %v", err)
	}
}

// TestCanViewNonOwnerPrivateShortCircuits leaves friendsMock with no
// expectations set — an unexpected AreFriends call there would surface as an
// error, proving canView short-circuits on IsPublic before ever asking.
func TestCanViewNonOwnerPrivateShortCircuits(t *testing.T) {
	svc, _, friendsMock := newTestService(t)

	pl := &playlistRow{ID: uuid.New(), OwnerID: "owner_1", IsPublic: false}
	ok, err := svc.canView(context.Background(), "stranger", pl)
	if err != nil {
		t.Fatalf("unexpected error (would fire if AreFriends had been unexpectedly called): %v", err)
	}
	if ok {
		t.Fatal("expected a private playlist to be invisible to a non-owner")
	}
	if err := friendsMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet friends expectations: %v", err)
	}
}

func TestCanViewNonOwnerPublicFriends(t *testing.T) {
	svc, _, friendsMock := newTestService(t)

	friendsMock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)")).
		WithArgs("owner_1", "stranger").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	pl := &playlistRow{ID: uuid.New(), OwnerID: "owner_1", IsPublic: true}
	ok, err := svc.canView(context.Background(), "stranger", pl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a public playlist to be visible to a friend")
	}
}

func TestCanViewNonOwnerPublicNotFriends(t *testing.T) {
	svc, _, friendsMock := newTestService(t)

	friendsMock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)")).
		WithArgs("owner_1", "stranger").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	pl := &playlistRow{ID: uuid.New(), OwnerID: "owner_1", IsPublic: true}
	ok, err := svc.canView(context.Background(), "stranger", pl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected a public playlist to stay invisible to a non-friend")
	}
}

// --- Service.ListItems -------------------------------------------------------

func TestListItemsPlaylistMissingReturnsErrNotFound(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}))

	_, err := svc.ListItems(context.Background(), "user_1", id)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestListItemsCanViewFalseReturnsSameErrNotFound checks the doc-commented
// design choice that "doesn't exist" and "exists but you can't see it" are
// deliberately indistinguishable to the caller.
func TestListItemsCanViewFalseReturnsSameErrNotFound(t *testing.T) {
	svc, repoMock, friendsMock := newTestService(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "owner_1", false))

	_, err := svc.ListItems(context.Background(), "stranger", id)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := friendsMock.ExpectationsWereMet(); err != nil {
		t.Errorf("private playlist should short-circuit before AreFriends: %v", err)
	}
}

func TestListItemsCanViewTrueReturnsItems(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "user_1", false))

	now := time.Now().UTC()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM playlist_items WHERE playlist_id = $1 ORDER BY added_at DESC")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows(playlistItemCols).AddRow(uuid.New(), id, "rec_1", nil, nil, nil, nil, nil, nil, now))

	got, err := svc.ListItems(context.Background(), "user_1", id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].RecordID != "rec_1" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

// --- Service.AddItem -------------------------------------------------------

func TestAddItemPlaylistMissingReturnsErrNotFound(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}))

	_, err := svc.AddItem(context.Background(), "user_1", id, AddItemRequest{RecordID: "rec_1"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestAddItemNotOwnerReturnsErrNotFound checks AddItem's owner-only guard —
// unlike ListItems, a friend viewing a public playlist may not add to it.
func TestAddItemNotOwnerReturnsErrNotFound(t *testing.T) {
	svc, repoMock, friendsMock := newTestService(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "owner_1", true))

	_, err := svc.AddItem(context.Background(), "friend_1", id, AddItemRequest{RecordID: "rec_1"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := friendsMock.ExpectationsWereMet(); err != nil {
		t.Errorf("AddItem is owner-only and should never consult friendsRepo: %v", err)
	}
}

func TestAddItemExistingReturnsUnchanged(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "user_1", false))

	existingID := uuid.New()
	now := time.Now().UTC()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM playlist_items WHERE playlist_id = $1 AND record_id = $2")).
		WithArgs(id, "rec_1").
		WillReturnRows(pgxmock.NewRows(playlistItemCols).AddRow(existingID, id, "rec_1", nil, nil, nil, nil, nil, nil, now))

	got, err := svc.AddItem(context.Background(), "user_1", id, AddItemRequest{RecordID: "rec_1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != existingID {
		t.Fatalf("expected the existing item unchanged, got %+v", got)
	}
	if err := repoMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (InsertItem/CountItems should not have run): %v", err)
	}
}

func TestAddItemAtCapacityReturnsErrItemLimitReached(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

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

	_, err := svc.AddItem(context.Background(), "user_1", id, AddItemRequest{RecordID: "rec_new"})
	if !errors.Is(err, ErrItemLimitReached) {
		t.Fatalf("expected ErrItemLimitReached, got %v", err)
	}
	if err := repoMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (InsertItem should not have run): %v", err)
	}
}

func TestAddItemHappyPathInserts(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "user_1", false))
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM playlist_items WHERE playlist_id = $1 AND record_id = $2")).
		WithArgs(id, "rec_new").
		WillReturnRows(pgxmock.NewRows(playlistItemCols))
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM playlist_items WHERE playlist_id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(2)))
	repoMock.ExpectExec(regexp.QuoteMeta(
		`INSERT INTO playlist_items (id, playlist_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, added_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`)).
		WithArgs(pgxmock.AnyArg(), id, "rec_new", (*string)(nil), strPtr("Title"), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	got, err := svc.AddItem(context.Background(), "user_1", id, AddItemRequest{RecordID: "rec_new", Title: strPtr("Title")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PlaylistID != id || got.RecordID != "rec_new" || got.ID == uuid.Nil {
		t.Fatalf("unexpected item: %+v", got)
	}
	if err := repoMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// --- Service.RemoveItem -----------------------------------------------------

func TestRemoveItemNotOwnerReturnsErrNotFound(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "owner_1", true))

	err := svc.RemoveItem(context.Background(), "friend_1", id, "rec_1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRemoveItemOwnerHappyPath(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	id := uuid.New()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT id, owner_id, is_public FROM playlists WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "owner_id", "is_public"}).AddRow(id, "user_1", false))
	repoMock.ExpectExec(regexp.QuoteMeta("DELETE FROM playlist_items WHERE playlist_id = $1 AND record_id = $2")).
		WithArgs(id, "rec_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := svc.RemoveItem(context.Background(), "user_1", id, "rec_1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Service.ListForUser -----------------------------------------------------

// TestListForUserOwnProfileSkipsFriendshipCheck leaves friendsMock with no
// expectations set — an unexpected AreFriends call there would surface as an
// error.
func TestListForUserOwnProfileSkipsFriendshipCheck(t *testing.T) {
	svc, repoMock, friendsMock := newTestService(t)

	now := time.Now().UTC()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT " + playlistWithCountColumns + " FROM playlists p WHERE p.owner_id = $1 ORDER BY p.created_at DESC")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows(playlistCols).AddRow(uuid.New(), "user_1", "Private List", false, 0, now, now))

	got, err := svc.ListForUser(context.Background(), "user_1", "user_1")
	if err != nil {
		t.Fatalf("unexpected error (would fire if AreFriends had been unexpectedly called): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the full list including private playlists, got %+v", got)
	}
	if err := friendsMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet friends expectations: %v", err)
	}
}

func TestListForUserNotFriendsReturnsEmpty(t *testing.T) {
	svc, repoMock, friendsMock := newTestService(t)

	friendsMock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)")).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	got, err := svc.ListForUser(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected a non-nil empty slice, got %#v", got)
	}
	if err := repoMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet repo expectations (playlist query should not have run): %v", err)
	}
}

func TestListForUserFriendsReturnsPublicOnly(t *testing.T) {
	svc, repoMock, friendsMock := newTestService(t)

	friendsMock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)")).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	now := time.Now().UTC()
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT " + playlistWithCountColumns + " FROM playlists p WHERE p.owner_id = $1 AND p.is_public = TRUE ORDER BY p.created_at DESC")).
		WithArgs("user_b").
		WillReturnRows(pgxmock.NewRows(playlistCols).AddRow(uuid.New(), "user_b", "Public List", true, 0, now, now))

	got, err := svc.ListForUser(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || !got[0].IsPublic {
		t.Fatalf("unexpected result: %+v", got)
	}
}
