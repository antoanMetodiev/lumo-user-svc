package aiassistant

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
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

func TestRepositoryRandomActiveKeyReturnsAKey(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT api_key FROM ai_assistant_api_keys WHERE is_active = true ORDER BY random() LIMIT 1")).
		WillReturnRows(pgxmock.NewRows([]string{"api_key"}).AddRow("test-key-1"))

	got, err := repo.RandomActiveKey(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "test-key-1" {
		t.Fatalf("got %q, want %q", got, "test-key-1")
	}
}

func TestServiceGetRandomApiKeyReturnsErrNoActiveKeysWhenTableEmpty(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT api_key FROM ai_assistant_api_keys WHERE is_active = true ORDER BY random() LIMIT 1")).
		WillReturnRows(pgxmock.NewRows([]string{"api_key"}))

	_, err := svc.GetRandomApiKey(context.Background())
	if !errors.Is(err, ErrNoActiveKeys) {
		t.Fatalf("err = %v, want ErrNoActiveKeys", err)
	}
}

func TestServiceGetRandomApiKeyPropagatesOtherErrors(t *testing.T) {
	repo, mock := newMockRepo(t)
	svc := NewService(repo)

	dbErr := errors.New("connection reset")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT api_key FROM ai_assistant_api_keys WHERE is_active = true ORDER BY random() LIMIT 1")).
		WillReturnError(dbErr)

	_, err := svc.GetRandomApiKey(context.Background())
	if err == nil || errors.Is(err, ErrNoActiveKeys) {
		t.Fatalf("err = %v, want a plain propagated error, not ErrNoActiveKeys", err)
	}
}
