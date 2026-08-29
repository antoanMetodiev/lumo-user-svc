// Package aiassistant serves the AuroraFlix AI chat feature's own Gemini API
// key pool — a table of keys (all currently free-tier, no billing behind
// them) the frontend draws from at random on every chat request, so the
// feature's effective daily quota is the sum of every key in the table
// instead of being capped by a single one. Any signed-in caller may draw a
// key (see cmd/server/main.go's userScopedPaths) — the specific caller
// identity doesn't matter to this package at all, unlike likes/watchlist.
package aiassistant

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"lumo-user-svc/internal/dbx"
)

// ErrNoActiveKeys means the table has no is_active=true row to hand out —
// the caller (Handlers) turns this into a 503 rather than a 500, since it's
// an operational/config gap (no keys provisioned yet), not a bug.
var ErrNoActiveKeys = errors.New("no active AI assistant API keys configured")

type Repository struct {
	pool dbx.Querier
}

func NewRepository(pool dbx.Querier) *Repository {
	return &Repository{pool: pool}
}

// RandomActiveKey picks uniformly at random among every is_active row —
// ORDER BY random() over a table expected to stay tiny (a handful of keys)
// costs nothing meaningful, and needs no application-side bookkeeping the
// way a round-robin counter would.
func (r *Repository) RandomActiveKey(ctx context.Context) (string, error) {
	var apiKey string
	err := r.pool.QueryRow(ctx,
		"SELECT api_key FROM ai_assistant_api_keys WHERE is_active = true ORDER BY random() LIMIT 1",
	).Scan(&apiKey)
	return apiKey, err
}

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetRandomApiKey(ctx context.Context) (string, error) {
	apiKey, err := s.repo.RandomActiveKey(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNoActiveKeys
		}
		return "", err
	}
	return apiKey, nil
}
