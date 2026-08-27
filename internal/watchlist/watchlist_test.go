package watchlist

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

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

var itemColumnNames = []string{"id", "clerk_id", "record_id", "tmdb_id", "title", "poster_img_url", "tmdb_rating", "type", "video_id", "added_at"}

func TestRepositoryFindByClerkIDOrderByAddedAtDescPopulated(t *testing.T) {
	repo, mock := newMockRepo(t)

	id1, id2 := uuid.New(), uuid.New()
	newer := time.Now().UTC()
	older := newer.Add(-time.Hour)
	rows := pgxmock.NewRows(itemColumnNames).
		AddRow(id1, "user_1", "rec_1", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), newer).
		AddRow(id2, "user_1", "rec_2", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), older)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM watchlist_items WHERE clerk_id = $1 ORDER BY added_at DESC")).
		WithArgs("user_1").
		WillReturnRows(rows)

	items, err := repo.FindByClerkIDOrderByAddedAtDesc(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 || items[0].RecordID != "rec_1" || items[1].RecordID != "rec_2" {
		t.Fatalf("unexpected items: %+v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryFindByClerkIDOrderByAddedAtDescEmpty(t *testing.T) {
	repo, mock := newMockRepo(t)

	rows := pgxmock.NewRows(itemColumnNames)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM watchlist_items WHERE clerk_id = $1 ORDER BY added_at DESC")).
		WithArgs("user_1").
		WillReturnRows(rows)

	items, err := repo.FindByClerkIDOrderByAddedAtDesc(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items, got %+v", items)
	}
}

func TestRepositoryFindByClerkIDAndRecordIDFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	id := uuid.New()
	now := time.Now().UTC()
	rows := pgxmock.NewRows(itemColumnNames).
		AddRow(id, "user_1", "rec_1", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnRows(rows)

	got, err := repo.FindByClerkIDAndRecordID(context.Background(), "user_1", "rec_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != id {
		t.Fatalf("unexpected result: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRepositoryFindByClerkIDAndRecordIDNotFound mirrors Optional.empty():
// no matching row must come back as (nil, nil), never pgx.ErrNoRows leaking
// to the caller.
func TestRepositoryFindByClerkIDAndRecordIDNotFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	rows := pgxmock.NewRows(itemColumnNames)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "ghost").
		WillReturnRows(rows)

	got, err := repo.FindByClerkIDAndRecordID(context.Background(), "user_1", "ghost")
	if err != nil {
		t.Fatalf("expected no error for a missing row, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil item for a missing row, got %+v", got)
	}
}

func TestRepositoryFindByClerkIDAndRecordIDQueryError(t *testing.T) {
	repo, mock := newMockRepo(t)

	boom := errors.New("connection reset")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnError(boom)

	_, err := repo.FindByClerkIDAndRecordID(context.Background(), "user_1", "rec_1")
	if !errors.Is(err, boom) {
		t.Fatalf("expected the underlying query error to propagate, got %v", err)
	}
}

const watchlistInsertSQL = `INSERT INTO watchlist_items (id, clerk_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, added_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`

func TestRepositoryInsertAssignsUUID(t *testing.T) {
	repo, mock := newMockRepo(t)

	it := &models.WatchlistItem{
		ClerkID:  "user_1",
		RecordID: "rec_1",
		AddedAt:  time.Now().UTC(),
	}

	if it.ID != uuid.Nil {
		t.Fatalf("expected zero UUID before Insert, got %v", it.ID)
	}

	mock.ExpectExec(regexp.QuoteMeta(watchlistInsertSQL)).
		WithArgs(pgxmock.AnyArg(), it.ClerkID, it.RecordID, it.TmdbID, it.Title, it.PosterImgURL, it.TmdbRating, it.Type, it.VideoID, it.AddedAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.Insert(context.Background(), it); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if it.ID == uuid.Nil {
		t.Fatal("expected Insert to assign a non-zero UUID")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRepositoryDeleteByClerkIDAndRecordIDNoMatch mirrors Spring Data's
// derived delete semantics — no matching row is a no-op, not an error.
func TestRepositoryDeleteByClerkIDAndRecordIDNoMatch(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "ghost").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	if err := repo.DeleteByClerkIDAndRecordID(context.Background(), "user_1", "ghost"); err != nil {
		t.Fatalf("expected no error when nothing matched, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryDeleteByID(t *testing.T) {
	repo, mock := newMockRepo(t)

	id := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM watchlist_items WHERE id = $1")).
		WithArgs(id).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := repo.DeleteByID(context.Background(), id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryCountByClerkID(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM watchlist_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(7)))

	count, err := repo.CountByClerkID(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 7 {
		t.Fatalf("count = %d, want 7", count)
	}
}

// TestServiceAddToWatchlistExistingItemReturnedUnchanged verifies that a
// recordId already on the list short-circuits before any eviction/insert
// logic runs — the mock has exactly one expectation queued, so an unmet or
// unexpected call would fail ExpectationsWereMet.
func TestServiceAddToWatchlistExistingItemReturnedUnchanged(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	existingID := uuid.New()
	rows := pgxmock.NewRows(itemColumnNames).
		AddRow(existingID, "user_1", "rec_1", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), time.Now().UTC())

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnRows(rows)

	got, err := svc.AddToWatchlist(context.Background(), "user_1", AddToWatchlistRequest{RecordID: "rec_1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != existingID {
		t.Fatalf("expected the existing item returned unchanged, got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (or unexpected eviction/insert calls): %v", err)
	}
}

func TestServiceAddToWatchlistUnderCapacityInsertsDirectly(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_new").
		WillReturnRows(pgxmock.NewRows(itemColumnNames))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM watchlist_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(maxItems - 1)))

	// Below capacity, so no FindByClerkIDOrderByAddedAtDesc/DeleteByID
	// expectation is queued at all — an eviction attempt would fail here.
	mock.ExpectExec(regexp.QuoteMeta(watchlistInsertSQL)).
		WithArgs(pgxmock.AnyArg(), "user_1", "rec_new", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	item, err := svc.AddToWatchlist(context.Background(), "user_1", AddToWatchlistRequest{RecordID: "rec_new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item == nil || item.RecordID != "rec_new" || item.ID == uuid.Nil {
		t.Fatalf("unexpected item: %+v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestServiceAddToWatchlistAtCapacityEvictsOldestThenInserts drives the full
// evictOldestIfAtCapacity sequence and checks it happens in the documented
// order: count, then find-ordered, then delete the oldest (last element of
// the desc-ordered list), then insert the new item. pgxmock enforces
// expectation order by default, so queuing them in this exact sequence is
// itself the assertion.
func TestServiceAddToWatchlistAtCapacityEvictsOldestThenInserts(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	newestID, oldestID := uuid.New(), uuid.New()
	newer := time.Now().UTC()
	older := newer.Add(-24 * time.Hour)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_new").
		WillReturnRows(pgxmock.NewRows(itemColumnNames))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM watchlist_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(maxItems)))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM watchlist_items WHERE clerk_id = $1 ORDER BY added_at DESC")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows(itemColumnNames).
			AddRow(newestID, "user_1", "rec_keep", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), newer).
			AddRow(oldestID, "user_1", "rec_old", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), older))

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM watchlist_items WHERE id = $1")).
		WithArgs(oldestID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	mock.ExpectExec(regexp.QuoteMeta(watchlistInsertSQL)).
		WithArgs(pgxmock.AnyArg(), "user_1", "rec_new", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	item, err := svc.AddToWatchlist(context.Background(), "user_1", AddToWatchlistRequest{RecordID: "rec_new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item == nil || item.RecordID != "rec_new" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (wrong call order or args): %v", err)
	}
}

// TestServiceAddToWatchlistAtCapacityButEmptyOrderedListSkipsEviction covers
// the race guard in evictOldestIfAtCapacity: CountByClerkID says the list is
// at capacity, but by the time the ordered list is fetched it's empty (e.g.
// concurrent removal). No DeleteByID may be called, and the flow must still
// succeed by falling through to Insert.
func TestServiceAddToWatchlistAtCapacityButEmptyOrderedListSkipsEviction(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_new").
		WillReturnRows(pgxmock.NewRows(itemColumnNames))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM watchlist_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(maxItems)))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM watchlist_items WHERE clerk_id = $1 ORDER BY added_at DESC")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows(itemColumnNames))

	// No DeleteByID expectation queued — if the code called it anyway, the
	// unmatched call would fail below.
	mock.ExpectExec(regexp.QuoteMeta(watchlistInsertSQL)).
		WithArgs(pgxmock.AnyArg(), "user_1", "rec_new", (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	item, err := svc.AddToWatchlist(context.Background(), "user_1", AddToWatchlistRequest{RecordID: "rec_new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item == nil {
		t.Fatal("expected the new item to still be inserted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (or an unexpected DeleteByID call): %v", err)
	}
}

// TestServiceRemoveFromWatchlist is a thin pass-through check — Service
// should neither swallow the repository's error nor add any behavior.
func TestServiceRemoveFromWatchlist(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := svc.RemoveFromWatchlist(context.Background(), "user_1", "rec_1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
