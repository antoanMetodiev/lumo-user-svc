package users

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

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

func TestRepositoryFindByClerkIDFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	name := "Ada Lovelace"
	img := "https://example.com/a.png"
	now := time.Now().UTC()
	rows := pgxmock.NewRows([]string{"clerk_id", "email", "display_name", "profile_image_url", "subscription_type", "created_at", "updated_at"}).
		AddRow("user_1", "ada@example.com", &name, &img, models.SubscriptionRegular, now, now)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + userColumns + " FROM users WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnRows(rows)

	got, err := repo.FindByClerkID(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected a user, got nil")
	}
	if got.ClerkID != "user_1" || got.Email != "ada@example.com" || *got.DisplayName != name {
		t.Fatalf("unexpected user: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRepositoryFindByClerkIDNotFound mirrors Optional.empty(): no matching
// row must come back as (nil, nil), never pgx.ErrNoRows leaking to the caller.
func TestRepositoryFindByClerkIDNotFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	rows := pgxmock.NewRows([]string{"clerk_id", "email", "display_name", "profile_image_url", "subscription_type", "created_at", "updated_at"})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + userColumns + " FROM users WHERE clerk_id = $1")).
		WithArgs("ghost").
		WillReturnRows(rows)

	got, err := repo.FindByClerkID(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("expected no error for a missing row, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil user for a missing row, got %+v", got)
	}
}

func TestRepositoryFindByClerkIDQueryError(t *testing.T) {
	repo, mock := newMockRepo(t)

	boom := errors.New("connection reset")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + userColumns + " FROM users WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnError(boom)

	_, err := repo.FindByClerkID(context.Background(), "user_1")
	if !errors.Is(err, boom) {
		t.Fatalf("expected the underlying query error to propagate, got %v", err)
	}
}

func TestRepositoryExistsByClerkID(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM users WHERE clerk_id = $1)")).
		WithArgs("user_1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	exists, err := repo.ExistsByClerkID(context.Background(), "user_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("expected exists to be true")
	}
}

func TestRepositoryUpsert(t *testing.T) {
	repo, mock := newMockRepo(t)

	u := &models.User{ClerkID: "user_1", Email: "a@example.com", SubscriptionType: models.SubscriptionRegular}

	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO users (clerk_id, email, display_name, profile_image_url, subscription_type, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (clerk_id) DO UPDATE SET
			email = EXCLUDED.email,
			display_name = EXCLUDED.display_name,
			profile_image_url = EXCLUDED.profile_image_url,
			updated_at = EXCLUDED.updated_at`)).
		WithArgs(u.ClerkID, u.Email, u.DisplayName, u.ProfileImageURL, u.SubscriptionType, u.CreatedAt, u.UpdatedAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.Upsert(context.Background(), u); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryDeleteByClerkID(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM users WHERE clerk_id = $1")).
		WithArgs("user_1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := repo.DeleteByClerkID(context.Background(), "user_1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRepositoryUpdateSubscriptionTypeFound and ...NotFound cover the
// rows-affected branch that stands in for Java's separate find-then-save —
// zero rows affected must report false, not an error, since a clerkId that
// hasn't synced yet is an expected race, not a failure.
func TestRepositoryUpdateSubscriptionTypeFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET subscription_type = $2, updated_at = $3 WHERE clerk_id = $1")).
		WithArgs("user_1", models.SubscriptionPro, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	updated, err := repo.UpdateSubscriptionType(context.Background(), "user_1", models.SubscriptionPro)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !updated {
		t.Fatal("expected updated=true when a row matched")
	}
}

func TestRepositoryUpdateSubscriptionTypeNotFound(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET subscription_type = $2, updated_at = $3 WHERE clerk_id = $1")).
		WithArgs("ghost", models.SubscriptionPro, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	updated, err := repo.UpdateSubscriptionType(context.Background(), "ghost", models.SubscriptionPro)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated {
		t.Fatal("expected updated=false when no row matched")
	}
}

// TestServiceUpdateSubscriptionType is a thin pass-through check — Service
// should neither swallow the repository's result nor its error.
func TestServiceUpdateSubscriptionType(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET subscription_type = $2, updated_at = $3 WHERE clerk_id = $1")).
		WithArgs("user_1", models.SubscriptionPro, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	updated, err := svc.UpdateSubscriptionType(context.Background(), "user_1", models.SubscriptionPro)
	if err != nil || !updated {
		t.Fatalf("updated=%v err=%v, want true, nil", updated, err)
	}
}

func TestServiceFindByClerkID(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	rows := pgxmock.NewRows([]string{"clerk_id", "email", "display_name", "profile_image_url", "subscription_type", "created_at", "updated_at"})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT " + userColumns + " FROM users WHERE clerk_id = $1")).
		WithArgs("ghost").
		WillReturnRows(rows)

	got, err := svc.FindByClerkID(context.Background(), "ghost")
	if err != nil || got != nil {
		t.Fatalf("got=%v err=%v, want nil, nil", got, err)
	}
}
