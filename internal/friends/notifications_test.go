package friends

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
)

const (
	countNotificationsSQL   = "SELECT COUNT(*) FROM notifications WHERE clerk_id = $1"
	consumeNotificationsSQL = "WITH deleted AS ( DELETE FROM notifications WHERE clerk_id = $1 RETURNING id, type, actor_id, created_at ) SELECT d.id, d.type, d.actor_id, u.display_name, u.profile_image_url, d.created_at FROM deleted d LEFT JOIN users u ON u.clerk_id = d.actor_id ORDER BY d.created_at DESC"
)

func TestRepositoryInsertNotification(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta(insertNotificationSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", NotificationFriendRequest, "user_b", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.InsertNotification(context.Background(), "user_a", NotificationFriendRequest, "user_b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryCountNotifications(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta(countNotificationsSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(3)))

	count, err := repo.CountNotifications(context.Background(), "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

func TestRepositoryCountNotificationsQueryError(t *testing.T) {
	repo, mock := newMockRepo(t)
	boom := errors.New("connection reset")

	mock.ExpectQuery(regexp.QuoteMeta(countNotificationsSQL)).
		WithArgs("user_a").
		WillReturnError(boom)

	_, err := repo.CountNotifications(context.Background(), "user_a")
	if !errors.Is(err, boom) {
		t.Fatalf("expected the underlying query error to propagate, got %v", err)
	}
}

func TestRepositoryConsumeNotificationsPopulated(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	now := time.Now().UTC()
	name := "Ada"

	rows := pgxmock.NewRows([]string{"id", "type", "actor_id", "display_name", "profile_image_url", "created_at"}).
		AddRow(id, NotificationFriendRequest, "user_b", &name, (*string)(nil), now)

	mock.ExpectQuery(regexp.QuoteMeta(consumeNotificationsSQL)).
		WithArgs("user_a").
		WillReturnRows(rows)

	got, err := repo.ConsumeNotifications(context.Background(), "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != id || got[0].Type != NotificationFriendRequest || got[0].ActorID != "user_b" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRepositoryConsumeNotificationsEmpty checks the same non-nil-empty-slice
// convention as the friend/request list methods: no notifications must come
// back as []NotificationDTO{}, not nil.
func TestRepositoryConsumeNotificationsEmpty(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta(consumeNotificationsSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "actor_id", "display_name", "profile_image_url", "created_at"}))

	got, err := repo.ConsumeNotifications(context.Background(), "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected a non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty slice, got %+v", got)
	}
}

func TestServiceNotificationCount(t *testing.T) {
	svc, mock, _ := newMockService(t)

	mock.ExpectQuery(regexp.QuoteMeta(countNotificationsSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(5)))

	count, err := svc.NotificationCount(context.Background(), "user_a")
	if err != nil || count != 5 {
		t.Fatalf("count=%d err=%v, want 5, nil", count, err)
	}
}

func TestServiceConsumeNotifications(t *testing.T) {
	svc, mock, _ := newMockService(t)

	mock.ExpectQuery(regexp.QuoteMeta(consumeNotificationsSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "actor_id", "display_name", "profile_image_url", "created_at"}))

	got, err := svc.ConsumeNotifications(context.Background(), "user_a")
	if err != nil || got == nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
