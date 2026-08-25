// Package likes mirrors internal/watchlist's exact shape — "like a movie or
// series" is the same "does this user have this record on some list" pattern
// watchlist already established, just a different list with no capacity cap
// (a like is a lighter, uncapped signal, not a bounded "queue to watch").
// No listing UI yet by design (per the user, 2026-08) — GetLikes exists for
// when that lands, but nothing renders it today.
package likes

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lumo-user-svc/internal/models"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const itemColumns = "id, clerk_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, liked_at"

func scanItem(row pgx.CollectableRow) (models.LikedItem, error) {
	var it models.LikedItem
	err := row.Scan(&it.ID, &it.ClerkID, &it.RecordID, &it.TmdbID, &it.Title, &it.PosterImgURL, &it.TmdbRating, &it.Type, &it.VideoID, &it.LikedAt)
	return it, err
}

func (r *Repository) FindByClerkIDOrderByLikedAtDesc(ctx context.Context, clerkID string) ([]models.LikedItem, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+itemColumns+" FROM liked_items WHERE clerk_id = $1 ORDER BY liked_at DESC", clerkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, scanItem)
}

func (r *Repository) FindByClerkIDAndRecordID(ctx context.Context, clerkID, recordID string) (*models.LikedItem, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+itemColumns+" FROM liked_items WHERE clerk_id = $1 AND record_id = $2", clerkID, recordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	it, err := pgx.CollectExactlyOneRow(rows, scanItem)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &it, nil
}

func (r *Repository) Insert(ctx context.Context, it *models.LikedItem) error {
	it.ID = uuid.New()
	_, err := r.pool.Exec(ctx,
		`INSERT INTO liked_items (id, clerk_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, liked_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		it.ID, it.ClerkID, it.RecordID, it.TmdbID, it.Title, it.PosterImgURL, it.TmdbRating, it.Type, it.VideoID, it.LikedAt)
	return err
}

// DeleteByClerkIDAndRecordID is a no-op, not an error, when nothing matches
// — same as watchlist's equivalent delete.
func (r *Repository) DeleteByClerkIDAndRecordID(ctx context.Context, clerkID, recordID string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM liked_items WHERE clerk_id = $1 AND record_id = $2", clerkID, recordID)
	return err
}

// Service mirrors watchlist.Service.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetLikes(ctx context.Context, clerkID string) ([]models.LikedItem, error) {
	return s.repo.FindByClerkIDOrderByLikedAtDesc(ctx, clerkID)
}

// AddLikeRequest mirrors watchlist.AddToWatchlistRequest's shape.
type AddLikeRequest struct {
	RecordID     string  `json:"recordId"`
	TmdbID       *string `json:"tmdbId"`
	Title        *string `json:"title"`
	PosterImgURL *string `json:"posterImgURL"`
	TmdbRating   *string `json:"tmdbRating"`
	Type         *string `json:"type"`
	VideoID      *string `json:"videoId"`
}

// AddLike returns the existing like unchanged if this recordId is already
// liked — idempotent, same as AddToWatchlist, no capacity cap to enforce.
func (s *Service) AddLike(ctx context.Context, clerkID string, req AddLikeRequest) (*models.LikedItem, error) {
	existing, err := s.repo.FindByClerkIDAndRecordID(ctx, clerkID, req.RecordID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	item := &models.LikedItem{
		ClerkID:      clerkID,
		RecordID:     req.RecordID,
		TmdbID:       req.TmdbID,
		Title:        req.Title,
		PosterImgURL: req.PosterImgURL,
		TmdbRating:   req.TmdbRating,
		Type:         req.Type,
		VideoID:      req.VideoID,
		LikedAt:      time.Now().UTC(),
	}
	if err := s.repo.Insert(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) RemoveLike(ctx context.Context, clerkID, recordID string) error {
	return s.repo.DeleteByClerkIDAndRecordID(ctx, clerkID, recordID)
}
