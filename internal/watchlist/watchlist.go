// Package watchlist mirrors WatchlistRepository + WatchlistService +
// WatchlistController.
package watchlist

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lumo-user-svc/internal/models"
)

// maxItems mirrors MAX_WATCHLIST_ITEMS in the frontend's localStorage
// watchlist, so signed-in and anonymous users see the same cap.
const maxItems = 20

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const itemColumns = "id, clerk_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, added_at"

func scanItem(row pgx.CollectableRow) (models.WatchlistItem, error) {
	var it models.WatchlistItem
	err := row.Scan(&it.ID, &it.ClerkID, &it.RecordID, &it.TmdbID, &it.Title, &it.PosterImgURL, &it.TmdbRating, &it.Type, &it.VideoID, &it.AddedAt)
	return it, err
}

// FindByClerkIDOrderByAddedAtDesc mirrors the derived query of the same name.
func (r *Repository) FindByClerkIDOrderByAddedAtDesc(ctx context.Context, clerkID string) ([]models.WatchlistItem, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+itemColumns+" FROM watchlist_items WHERE clerk_id = $1 ORDER BY added_at DESC", clerkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, scanItem)
}

// FindByClerkIDAndRecordID mirrors the derived query of the same name.
func (r *Repository) FindByClerkIDAndRecordID(ctx context.Context, clerkID, recordID string) (*models.WatchlistItem, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+itemColumns+" FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2", clerkID, recordID)
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

func (r *Repository) Insert(ctx context.Context, it *models.WatchlistItem) error {
	it.ID = uuid.New()
	_, err := r.pool.Exec(ctx,
		`INSERT INTO watchlist_items (id, clerk_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, added_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		it.ID, it.ClerkID, it.RecordID, it.TmdbID, it.Title, it.PosterImgURL, it.TmdbRating, it.Type, it.VideoID, it.AddedAt)
	return err
}

// DeleteByClerkIDAndRecordID mirrors the derived delete query — a no-op,
// not an error, when nothing matches (same as Spring Data's own derived
// delete).
func (r *Repository) DeleteByClerkIDAndRecordID(ctx context.Context, clerkID, recordID string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM watchlist_items WHERE clerk_id = $1 AND record_id = $2", clerkID, recordID)
	return err
}

func (r *Repository) DeleteByID(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM watchlist_items WHERE id = $1", id)
	return err
}

func (r *Repository) CountByClerkID(ctx context.Context, clerkID string) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM watchlist_items WHERE clerk_id = $1", clerkID).Scan(&count)
	return count, err
}

// Service mirrors WatchlistService.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetWatchlist(ctx context.Context, clerkID string) ([]models.WatchlistItem, error) {
	return s.repo.FindByClerkIDOrderByAddedAtDesc(ctx, clerkID)
}

// AddToWatchlistRequest mirrors WatchlistItemRequest.
type AddToWatchlistRequest struct {
	RecordID     string  `json:"recordId"`
	TmdbID       *string `json:"tmdbId"`
	Title        *string `json:"title"`
	PosterImgURL *string `json:"posterImgURL"`
	TmdbRating   *string `json:"tmdbRating"`
	Type         *string `json:"type"`
	VideoID      *string `json:"videoId"`
}

// AddToWatchlist mirrors WatchlistService.addToWatchlist — returns the
// existing item unchanged if this recordId is already saved, otherwise
// evicts the oldest item first if already at capacity, then inserts.
func (s *Service) AddToWatchlist(ctx context.Context, clerkID string, req AddToWatchlistRequest) (*models.WatchlistItem, error) {
	existing, err := s.repo.FindByClerkIDAndRecordID(ctx, clerkID, req.RecordID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	if err := s.evictOldestIfAtCapacity(ctx, clerkID); err != nil {
		return nil, err
	}

	item := &models.WatchlistItem{
		ClerkID:      clerkID,
		RecordID:     req.RecordID,
		TmdbID:       req.TmdbID,
		Title:        req.Title,
		PosterImgURL: req.PosterImgURL,
		TmdbRating:   req.TmdbRating,
		Type:         req.Type,
		VideoID:      req.VideoID,
		AddedAt:      time.Now().UTC(),
	}
	if err := s.repo.Insert(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) RemoveFromWatchlist(ctx context.Context, clerkID, recordID string) error {
	return s.repo.DeleteByClerkIDAndRecordID(ctx, clerkID, recordID)
}

func (s *Service) evictOldestIfAtCapacity(ctx context.Context, clerkID string) error {
	count, err := s.repo.CountByClerkID(ctx, clerkID)
	if err != nil {
		return err
	}
	if count < maxItems {
		return nil
	}

	ordered, err := s.repo.FindByClerkIDOrderByAddedAtDesc(ctx, clerkID)
	if err != nil {
		return err
	}
	if len(ordered) == 0 {
		return nil
	}
	oldest := ordered[len(ordered)-1]
	return s.repo.DeleteByID(ctx, oldest.ID)
}
