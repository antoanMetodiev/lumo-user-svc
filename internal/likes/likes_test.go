package likes

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
// behind the likes Repository, one behind friendsRepo — so expectations set
// on each are unambiguous about which dependency they belong to.
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

var itemCols = []string{"id", "clerk_id", "record_id", "tmdb_id", "title", "poster_img_url", "tmdb_rating", "type", "video_id", "liked_at"}

func TestRepositoryFindByClerkIDOrderByLikedAtDescReturnsRows(t *testing.T) {
	repo, mock := newMockRepo(t)

	now := time.Now().UTC()
	id1, id2 := uuid.New(), uuid.New()
	rows := pgxmock.NewRows(itemCols).
		AddRow(id1, "user_1", "rec_1", strPtr("tmdb_1"), strPtr("Movie One"), strPtr("poster1"), strPtr("7.5"), strPtr("movie"), strPtr("vid1"), now).
		AddRow(id2, "user_1", "rec_2", strPtr("tmdb_2"), strPtr("Movie Two"), strPtr("poster2"), strPtr("8.1"), strPtr("movie"), strPtr("vid2"), now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM liked_items WHERE clerk_id = $1 ORDER BY liked_at DESC")).
		WithArgs("user_1").
		WillReturnRows(rows)

	got, err := repo.FindByClerkIDOrderByLikedAtDesc(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].RecordID != "rec_1" || got[1].RecordID != "rec_2" {
		t.Fatalf("unexpected items: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryFindByClerkIDOrderByLikedAtDescEmpty(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM liked_items WHERE clerk_id = $1 ORDER BY liked_at DESC")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows(itemCols))

	got, err := repo.FindByClerkIDOrderByLikedAtDesc(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no items, got %+v", got)
	}
}

func TestRepositoryFindByClerkIDAndRecordIDFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	now := time.Now().UTC()
	id := uuid.New()
	rows := pgxmock.NewRows(itemCols).
		AddRow(id, "user_1", "rec_1", strPtr("tmdb_1"), strPtr("Movie One"), strPtr("poster1"), strPtr("7.5"), strPtr("movie"), strPtr("vid1"), now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnRows(rows)

	got, err := repo.FindByClerkIDAndRecordID(context.Background(), "user_1", "rec_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != id {
		t.Fatalf("unexpected result: %+v", got)
	}
}

// TestRepositoryFindByClerkIDAndRecordIDNotFound checks that no matching row
// comes back as (nil, nil), not pgx.ErrNoRows leaking to the caller — same
// convention as users.Repository.FindByClerkID.
func TestRepositoryFindByClerkIDAndRecordIDNotFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "ghost").
		WillReturnRows(pgxmock.NewRows(itemCols))

	got, err := repo.FindByClerkIDAndRecordID(context.Background(), "user_1", "ghost")
	if err != nil {
		t.Fatalf("expected no error for a missing row, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for a missing row, got %+v", got)
	}
}

func TestRepositoryFindByClerkIDAndRecordIDQueryError(t *testing.T) {
	repo, mock := newMockRepo(t)

	boom := errors.New("connection reset")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnError(boom)

	_, err := repo.FindByClerkIDAndRecordID(context.Background(), "user_1", "rec_1")
	if !errors.Is(err, boom) {
		t.Fatalf("expected the underlying query error to propagate, got %v", err)
	}
}

func TestRepositoryInsertAssignsFreshUUID(t *testing.T) {
	repo, mock := newMockRepo(t)

	it := &models.LikedItem{
		ClerkID:  "user_1",
		RecordID: "rec_1",
		TmdbID:   strPtr("tmdb_1"),
		Title:    strPtr("Movie One"),
		LikedAt:  time.Now().UTC(),
	}

	mock.ExpectExec(regexp.QuoteMeta(
		`INSERT INTO liked_items (id, clerk_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, liked_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`)).
		WithArgs(pgxmock.AnyArg(), it.ClerkID, it.RecordID, it.TmdbID, it.Title, it.PosterImgURL, it.TmdbRating, it.Type, it.VideoID, it.LikedAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if it.ID != uuid.Nil {
		t.Fatalf("precondition failed: ID already set before Insert")
	}
	if err := repo.Insert(context.Background(), it); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if it.ID == uuid.Nil {
		t.Fatal("expected Insert to assign a fresh, non-zero UUID")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryDeleteByClerkIDAndRecordID(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := repo.DeleteByClerkIDAndRecordID(context.Background(), "user_1", "rec_1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepositoryCountByClerkID(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM liked_items WHERE clerk_id = $1")).
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

// TestServiceGetLikesForUserOwnProfileSkipsFriendshipCheck asserts the
// callerID == targetID short-circuit never touches friendsRepo at all: no
// expectation is set on friendsMock, so an unexpected AreFriends call there
// would surface as an error rather than silently succeeding.
func TestServiceGetLikesForUserOwnProfileSkipsFriendshipCheck(t *testing.T) {
	svc, repoMock, friendsMock := newTestService(t)

	now := time.Now().UTC()
	rows := pgxmock.NewRows(itemCols).
		AddRow(uuid.New(), "user_1", "rec_1", strPtr("tmdb_1"), strPtr("Movie One"), nil, nil, nil, nil, now)
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM liked_items WHERE clerk_id = $1 ORDER BY liked_at DESC")).
		WithArgs("user_1").
		WillReturnRows(rows)

	got, err := svc.GetLikesForUser(context.Background(), "user_1", "user_1")
	if err != nil {
		t.Fatalf("unexpected error (would fire if AreFriends had been unexpectedly called): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the full list, got %+v", got)
	}
	if err := repoMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet repo expectations: %v", err)
	}
	if err := friendsMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet friends expectations: %v", err)
	}
}

// TestServiceGetLikesForUserNotFriendsReturnsEmpty leaves repoMock with no
// expectations at all — if GetLikesForUser fell through to the underlying
// likes query despite not being friends, that unexpected call would surface
// as an error here.
func TestServiceGetLikesForUserNotFriendsReturnsEmpty(t *testing.T) {
	svc, repoMock, friendsMock := newTestService(t)

	friendsMock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)")).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	got, err := svc.GetLikesForUser(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected a non-nil empty slice, got %#v", got)
	}
	if err := repoMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet repo expectations (likes query should not have run): %v", err)
	}
	if err := friendsMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet friends expectations: %v", err)
	}
}

func TestServiceGetLikesForUserFriendsReturnsFullList(t *testing.T) {
	svc, repoMock, friendsMock := newTestService(t)

	friendsMock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)")).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	now := time.Now().UTC()
	rows := pgxmock.NewRows(itemCols).
		AddRow(uuid.New(), "user_b", "rec_1", nil, nil, nil, nil, nil, nil, now)
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT " + itemColumns + " FROM liked_items WHERE clerk_id = $1 ORDER BY liked_at DESC")).
		WithArgs("user_b").
		WillReturnRows(rows)

	got, err := svc.GetLikesForUser(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the friend's full list, got %+v", got)
	}
}

// TestServiceAddLikeExistingReturnsUnchanged leaves the count/insert
// expectations unset — any call to either would surface as an error.
func TestServiceAddLikeExistingReturnsUnchanged(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	now := time.Now().UTC()
	existingID := uuid.New()
	rows := pgxmock.NewRows(itemCols).
		AddRow(existingID, "user_1", "rec_1", nil, nil, nil, nil, nil, nil, now)
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnRows(rows)

	got, err := svc.AddLike(context.Background(), "user_1", AddLikeRequest{RecordID: "rec_1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != existingID {
		t.Fatalf("expected the existing item unchanged, got %+v", got)
	}
	if err := repoMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (Insert/Count should not have run): %v", err)
	}
}

func TestServiceAddLikeAtCapacityReturnsErrLimitReached(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_new").
		WillReturnRows(pgxmock.NewRows(itemCols))
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM liked_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(maxItems)))

	_, err := svc.AddLike(context.Background(), "user_1", AddLikeRequest{RecordID: "rec_new"})
	if !errors.Is(err, ErrLimitReached) {
		t.Fatalf("expected ErrLimitReached, got %v", err)
	}
	if err := repoMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (Insert should not have run): %v", err)
	}
}

func TestServiceAddLikeUnderCapacityInserts(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT "+itemColumns+" FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_new").
		WillReturnRows(pgxmock.NewRows(itemCols))
	repoMock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM liked_items WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(5)))
	repoMock.ExpectExec(regexp.QuoteMeta(
		`INSERT INTO liked_items (id, clerk_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, liked_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`)).
		WithArgs(pgxmock.AnyArg(), "user_1", "rec_new", (*string)(nil), strPtr("Some Title"), (*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	before := time.Now().UTC()
	got, err := svc.AddLike(context.Background(), "user_1", AddLikeRequest{RecordID: "rec_new", Title: strPtr("Some Title")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ClerkID != "user_1" || got.RecordID != "rec_new" || got.Title == nil || *got.Title != "Some Title" {
		t.Fatalf("unexpected item: %+v", got)
	}
	if got.ID == uuid.Nil {
		t.Fatal("expected a fresh UUID")
	}
	if got.LikedAt.Before(before) {
		t.Fatalf("expected LikedAt to be stamped at insert time, got %v (before %v)", got.LikedAt, before)
	}
	if err := repoMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestServiceRemoveLikePassesThrough(t *testing.T) {
	svc, repoMock, _ := newTestService(t)

	repoMock.ExpectExec(regexp.QuoteMeta("DELETE FROM liked_items WHERE clerk_id = $1 AND record_id = $2")).
		WithArgs("user_1", "rec_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := svc.RemoveLike(context.Background(), "user_1", "rec_1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := repoMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
