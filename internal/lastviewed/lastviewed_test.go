package lastviewed

import (
	"context"
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

var itemColumnNames = []string{"id", "clerk_id", "type", "tmdb_id", "series_id", "poster_url", "title", "tmdb_rating", "video_url", "release_date", "viewed_at"}

func TestRepositoryFindByClerkIDOrderByViewedAtDescPopulated(t *testing.T) {
	repo, mock := newMockRepo(t)

	id1, id2 := uuid.New(), uuid.New()
	newer := time.Now().UTC()
	older := newer.Add(-time.Hour)
	rows := pgxmock.NewRows(itemColumnNames).
		AddRow(id1, "user_1", "movie", (*string)(nil), (*string)(nil), (*string)(nil), "Title A", (*string)(nil), (*string)(nil), (*string)(nil), newer).
		AddRow(id2, "user_1", "movie", (*string)(nil), (*string)(nil), (*string)(nil), "Title B", (*string)(nil), (*string)(nil), (*string)(nil), older)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM last_viewed_items WHERE clerk_id = $1 ORDER BY viewed_at DESC")).
		WithArgs("user_1").
		WillReturnRows(rows)

	items, err := repo.FindByClerkIDOrderByViewedAtDesc(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 || items[0].Title != "Title A" || items[1].Title != "Title B" {
		t.Fatalf("unexpected items: %+v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryFindByClerkIDOrderByViewedAtDescEmpty(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM last_viewed_items WHERE clerk_id = $1 ORDER BY viewed_at DESC")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows(itemColumnNames))

	items, err := repo.FindByClerkIDOrderByViewedAtDesc(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items, got %+v", items)
	}
}

// TestRepositoryDeleteByClerkIDAndTitleNoMatch mirrors Spring Data's derived
// delete semantics — no matching row is a no-op, not an error.
func TestRepositoryDeleteByClerkIDAndTitleNoMatch(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM last_viewed_items WHERE clerk_id = $1 AND title = $2")).
		WithArgs("user_1", "ghost").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	if err := repo.DeleteByClerkIDAndTitle(context.Background(), "user_1", "ghost"); err != nil {
		t.Fatalf("expected no error when nothing matched, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryDeleteByID(t *testing.T) {
	repo, mock := newMockRepo(t)

	id := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM last_viewed_items WHERE id = $1")).
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

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM last_viewed_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(3)))

	count, err := repo.CountByClerkID(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

const lastViewedInsertSQL = `INSERT INTO last_viewed_items (id, clerk_id, type, tmdb_id, series_id, poster_url, title, tmdb_rating, video_url, release_date, viewed_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`

func TestRepositoryInsertAssignsUUID(t *testing.T) {
	repo, mock := newMockRepo(t)

	it := &models.LastViewedItem{
		ClerkID:  "user_1",
		Type:     "movie",
		Title:    "Some Movie",
		ViewedAt: time.Now().UTC(),
	}

	if it.ID != uuid.Nil {
		t.Fatalf("expected zero UUID before Insert, got %v", it.ID)
	}

	mock.ExpectExec(regexp.QuoteMeta(lastViewedInsertSQL)).
		WithArgs(pgxmock.AnyArg(), it.ClerkID, it.Type, it.TmdbID, it.SeriesID, it.PosterURL, it.Title, it.TmdbRating, it.VideoURL, it.ReleaseDate, it.ViewedAt).
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

// TestServiceRecordViewOrderDeleteEvictInsert drives the full RecordView
// sequence documented in lastviewed.go ("Exact Java order preserved"):
// DeleteByClerkIDAndTitle first (unconditionally, even though nothing
// matches here), then evictOldestIfAtCapacity, then Insert. pgxmock enforces
// expectation order by default, so queuing them in this exact sequence is
// itself the assertion.
func TestServiceRecordViewOrderDeleteEvictInsert(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	newestID, oldestID := uuid.New(), uuid.New()
	newer := time.Now().UTC()
	older := newer.Add(-24 * time.Hour)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM last_viewed_items WHERE clerk_id = $1 AND title = $2")).
		WithArgs("user_1", "New Movie").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM last_viewed_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(maxItems)))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM last_viewed_items WHERE clerk_id = $1 ORDER BY viewed_at DESC")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows(itemColumnNames).
			AddRow(newestID, "user_1", "movie", (*string)(nil), (*string)(nil), (*string)(nil), "Keep Me", (*string)(nil), (*string)(nil), (*string)(nil), newer).
			AddRow(oldestID, "user_1", "movie", (*string)(nil), (*string)(nil), (*string)(nil), "Evict Me", (*string)(nil), (*string)(nil), (*string)(nil), older))

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM last_viewed_items WHERE id = $1")).
		WithArgs(oldestID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	mock.ExpectExec(regexp.QuoteMeta(lastViewedInsertSQL)).
		WithArgs(pgxmock.AnyArg(), "user_1", "movie", (*string)(nil), (*string)(nil), (*string)(nil), "New Movie", (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	item, err := svc.RecordView(context.Background(), "user_1", RecordViewRequest{Type: "movie", Title: "New Movie"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item == nil || item.Title != "New Movie" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (wrong call order or args): %v", err)
	}
}

func TestServiceRecordViewUnderCapacitySkipsEviction(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM last_viewed_items WHERE clerk_id = $1 AND title = $2")).
		WithArgs("user_1", "New Movie").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM last_viewed_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(maxItems - 1)))

	// Below capacity — no FindByClerkIDOrderByViewedAtDesc/DeleteByID
	// expectation queued at all, so an eviction attempt would fail here.
	mock.ExpectExec(regexp.QuoteMeta(lastViewedInsertSQL)).
		WithArgs(pgxmock.AnyArg(), "user_1", "movie", (*string)(nil), (*string)(nil), (*string)(nil), "New Movie", (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	item, err := svc.RecordView(context.Background(), "user_1", RecordViewRequest{Type: "movie", Title: "New Movie"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item == nil {
		t.Fatal("expected an inserted item")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestServiceRecordViewAtCapacityButEmptyOrderedListSkipsEviction covers the
// race guard in evictOldestIfAtCapacity: CountByClerkID says the list is at
// capacity, but the ordered list comes back empty by the time it's fetched.
// No DeleteByID may be called, and the flow must still succeed.
func TestServiceRecordViewAtCapacityButEmptyOrderedListSkipsEviction(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM last_viewed_items WHERE clerk_id = $1 AND title = $2")).
		WithArgs("user_1", "New Movie").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM last_viewed_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(maxItems)))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM last_viewed_items WHERE clerk_id = $1 ORDER BY viewed_at DESC")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows(itemColumnNames))

	mock.ExpectExec(regexp.QuoteMeta(lastViewedInsertSQL)).
		WithArgs(pgxmock.AnyArg(), "user_1", "movie", (*string)(nil), (*string)(nil), (*string)(nil), "New Movie", (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	item, err := svc.RecordView(context.Background(), "user_1", RecordViewRequest{Type: "movie", Title: "New Movie"})
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
