// Package likes mirrors internal/watchlist's exact shape — "like a movie or
// series" is the same "does this user have this record on some list" pattern
// watchlist already established, just a different list.
//
// Visibility policy (revised, 2026-08): likes were originally public to any
// signed-in caller, no friendship required. The user reversed that call —
// liked titles are now friendship-gated exactly like playlists.Service's
// ListForUser (own likes always visible; someone else's only if you're
// their friend; a non-friend gets an empty slice rather than a 403/404 that
// would confirm the target has likes at all).
//
// Capacity (added 2026-08, per the user): capped at maxItems, unlike
// watchlist's evict-the-oldest-and-keep-going behavior — AddLike rejects
// outright once at capacity (ErrLimitReached) rather than silently dropping
// an old like to make room. The cap itself isn't surfaced anywhere upfront
// (no "12/30" counter) — the caller only learns about it if they actually
// hit it.
package likes

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lumo-user-svc/internal/dbx"
	"lumo-user-svc/internal/friends"
	"lumo-user-svc/internal/models"
)

const maxItems = 30

// ErrLimitReached is returned by AddLike once the caller already has
// maxItems liked titles.
var ErrLimitReached = errors.New("like limit reached")

type Repository struct {
	pool dbx.Querier
}

func NewRepository(pool dbx.Querier) *Repository {
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

func (r *Repository) CountByClerkID(ctx context.Context, clerkID string) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM liked_items WHERE clerk_id = $1", clerkID).Scan(&count)
	return count, err
}

// Service mirrors watchlist.Service.
type Service struct {
	repo        *Repository
	friendsRepo *friends.Repository
}

func NewService(repo *Repository, friendsRepo *friends.Repository) *Service {
	return &Service{repo: repo, friendsRepo: friendsRepo}
}

func (s *Service) GetLikes(ctx context.Context, clerkID string) ([]models.LikedItem, error) {
	return s.repo.FindByClerkIDOrderByLikedAtDesc(ctx, clerkID)
}

// GetLikesForUser is GetLikes' friendship-gated counterpart — see the
// package doc comment. callerID == targetID (viewing your own profile
// through this same path) always sees everything.
func (s *Service) GetLikesForUser(ctx context.Context, callerID, targetID string) ([]models.LikedItem, error) {
	if callerID == targetID {
		return s.GetLikes(ctx, targetID)
	}
	areFriends, err := s.friendsRepo.AreFriends(ctx, callerID, targetID)
	if err != nil {
		return nil, err
	}
	if !areFriends {
		return []models.LikedItem{}, nil
	}
	return s.GetLikes(ctx, targetID)
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
// liked — idempotent, same as AddToWatchlist. Unlike watchlist, hitting
// capacity rejects the add (ErrLimitReached) instead of evicting the oldest
// like to make room.
func (s *Service) AddLike(ctx context.Context, clerkID string, req AddLikeRequest) (*models.LikedItem, error) {
	existing, err := s.repo.FindByClerkIDAndRecordID(ctx, clerkID, req.RecordID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	count, err := s.repo.CountByClerkID(ctx, clerkID)
	if err != nil {
		return nil, err
	}
	if count >= maxItems {
		return nil, ErrLimitReached
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
