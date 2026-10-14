// Package lastviewed mirrors LastViewedRepository + LastViewedService +
// LastViewedController.
package lastviewed

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lumo-user-svc/internal/dbx"
	"lumo-user-svc/internal/models"
)

// maxItems mirrors MAX_ITEMS in the frontend's localStorage version.
const maxItems = 20

type Repository struct {
	pool dbx.Querier
}

func NewRepository(pool dbx.Querier) *Repository {
	return &Repository{pool: pool}
}

const itemColumns = "id, clerk_id, type, tmdb_id, series_id, poster_url, title, tmdb_rating, video_url, release_date, viewed_at"

func scanItem(row pgx.CollectableRow) (models.LastViewedItem, error) {
	var it models.LastViewedItem
	err := row.Scan(&it.ID, &it.ClerkID, &it.Type, &it.TmdbID, &it.SeriesID, &it.PosterURL, &it.Title, &it.TmdbRating, &it.VideoURL, &it.ReleaseDate, &it.ViewedAt)
	return it, err
}

func (r *Repository) FindByClerkIDOrderByViewedAtDesc(ctx context.Context, clerkID string) ([]models.LastViewedItem, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+itemColumns+" FROM last_viewed_items WHERE clerk_id = $1 ORDER BY viewed_at DESC", clerkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, scanItem)
}

func (r *Repository) DeleteByClerkIDAndTitle(ctx context.Context, clerkID, title string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM last_viewed_items WHERE clerk_id = $1 AND title = $2", clerkID, title)
	return err
}

// DeleteSameRecord removes the user's earlier entries for the same movie /
// series: matched by its TMDB id (or series id) — titles now come back
// translated (Inception / Генезис), so the title alone would keep one entry
// per language — and still by title for entries saved without an id.
func (r *Repository) DeleteSameRecord(ctx context.Context, clerkID, recordType, title string, tmdbID, seriesID *string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM last_viewed_items
		 WHERE clerk_id = $1
		   AND (title = $2
		        OR (type = $3 AND (($4::text IS NOT NULL AND tmdb_id = $4) OR ($5::text IS NOT NULL AND series_id = $5))))`,
		clerkID, title, recordType, nonEmpty(tmdbID), nonEmpty(seriesID))
	return err
}

func nonEmpty(value *string) *string {
	if value == nil || *value == "" {
		return nil
	}
	return value
}

// recordKey identifies one movie / series across languages.
func recordKey(it models.LastViewedItem) string {
	switch {
	case it.TmdbID != nil && *it.TmdbID != "":
		return it.Type + ":tmdb:" + *it.TmdbID
	case it.SeriesID != nil && *it.SeriesID != "":
		return it.Type + ":series:" + *it.SeriesID
	default:
		return it.Type + ":title:" + it.Title
	}
}

func (r *Repository) DeleteByID(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM last_viewed_items WHERE id = $1", id)
	return err
}

func (r *Repository) CountByClerkID(ctx context.Context, clerkID string) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM last_viewed_items WHERE clerk_id = $1", clerkID).Scan(&count)
	return count, err
}

func (r *Repository) Insert(ctx context.Context, it *models.LastViewedItem) error {
	it.ID = uuid.New()
	_, err := r.pool.Exec(ctx,
		`INSERT INTO last_viewed_items (id, clerk_id, type, tmdb_id, series_id, poster_url, title, tmdb_rating, video_url, release_date, viewed_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		it.ID, it.ClerkID, it.Type, it.TmdbID, it.SeriesID, it.PosterURL, it.Title, it.TmdbRating, it.VideoURL, it.ReleaseDate, it.ViewedAt)
	return err
}

// Service mirrors LastViewedService.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// GetLastViewed is newest first, one entry per movie / series — entries
// saved before the id-based dedupe (one per language) collapse to the newest.
func (s *Service) GetLastViewed(ctx context.Context, clerkID string) ([]models.LastViewedItem, error) {
	items, err := s.repo.FindByClerkIDOrderByViewedAtDesc(ctx, clerkID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	unique := items[:0]
	for _, it := range items {
		if key := recordKey(it); !seen[key] {
			seen[key] = true
			unique = append(unique, it)
		}
	}
	return unique, nil
}

// RecordViewRequest mirrors LastViewedItemRequest.
type RecordViewRequest struct {
	Type        string  `json:"type"`
	TmdbID      *string `json:"tmdbId"`
	SeriesID    *string `json:"seriesId"`
	PosterURL   *string `json:"posterURL"`
	Title       string  `json:"title"`
	TmdbRating  *string `json:"tmdbRating"`
	VideoURL    *string `json:"videoURL"`
	ReleaseDate *string `json:"releaseDate"`
}

// RecordView mirrors LastViewedService.recordView: re-viewing something
// already on the list bumps it to the top — deletes the old entry first
// (by TMDB / series id, else by title — see DeleteSameRecord), then evicts
// the oldest if already at capacity, then inserts fresh. Exact Java order
// preserved.
func (s *Service) RecordView(ctx context.Context, clerkID string, req RecordViewRequest) (*models.LastViewedItem, error) {
	if err := s.repo.DeleteSameRecord(ctx, clerkID, req.Type, req.Title, req.TmdbID, req.SeriesID); err != nil {
		return nil, err
	}
	if err := s.evictOldestIfAtCapacity(ctx, clerkID); err != nil {
		return nil, err
	}

	item := &models.LastViewedItem{
		ClerkID:     clerkID,
		Type:        req.Type,
		TmdbID:      req.TmdbID,
		SeriesID:    req.SeriesID,
		PosterURL:   req.PosterURL,
		Title:       req.Title,
		TmdbRating:  req.TmdbRating,
		VideoURL:    req.VideoURL,
		ReleaseDate: req.ReleaseDate,
		ViewedAt:    time.Now().UTC(),
	}
	if err := s.repo.Insert(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) evictOldestIfAtCapacity(ctx context.Context, clerkID string) error {
	count, err := s.repo.CountByClerkID(ctx, clerkID)
	if err != nil {
		return err
	}
	if count < maxItems {
		return nil
	}

	ordered, err := s.repo.FindByClerkIDOrderByViewedAtDesc(ctx, clerkID)
	if err != nil {
		return err
	}
	if len(ordered) == 0 {
		return nil
	}
	oldest := ordered[len(ordered)-1]
	return s.repo.DeleteByID(ctx, oldest.ID)
}
