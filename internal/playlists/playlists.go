// Package playlists implements named, owned movie/series collections. No
// Java original to mirror (new feature, 2026-08) — follows the same
// Repository+Service+Handlers shape as internal/watchlist/internal/likes.
//
// IsPublic is deliberately narrower than Likes' public-to-everyone model:
// per the user's explicit call, a public playlist is visible only to the
// owner's friends, not to every signed-in user. A private playlist is
// visible to no one but the owner. canView enforces this in one place;
// every read/write path that isn't the owner's own /playlists list routes
// through it (directly or via ListForUser).
package playlists

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

// ErrNotFound covers both "no such playlist" and "exists but you can't see
// it" — the two are deliberately indistinguishable to a non-owner caller,
// same reasoning as friends.Accept returning a bare 404 for a request that
// isn't yours.
var ErrNotFound = errors.New("playlist not found")

// maxItemsPerPlaylist mirrors likes' own maxItems (2026-08, per the user) —
// same "reject outright once at capacity, don't say anything about it until
// they actually hit it" policy.
const maxItemsPerPlaylist = 30

// ErrItemLimitReached is returned by AddItem once a playlist already holds
// maxItemsPerPlaylist titles.
var ErrItemLimitReached = errors.New("playlist item limit reached")

type Repository struct {
	pool dbx.Querier
}

func NewRepository(pool dbx.Querier) *Repository {
	return &Repository{pool: pool}
}

// PlaylistDTO adds a computed ItemCount so the listing views (my playlists,
// a friend's public playlists) don't need a second round-trip just to show
// "12 titles" on a card.
type PlaylistDTO struct {
	ID        uuid.UUID `json:"id"`
	OwnerID   string    `json:"ownerId"`
	Name      string    `json:"name"`
	IsPublic  bool      `json:"isPublic"`
	ItemCount int       `json:"itemCount"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func scanPlaylistDTO(row pgx.CollectableRow) (PlaylistDTO, error) {
	var p PlaylistDTO
	err := row.Scan(&p.ID, &p.OwnerID, &p.Name, &p.IsPublic, &p.ItemCount, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

const playlistWithCountColumns = `p.id, p.owner_id, p.name, p.is_public,
	(SELECT COUNT(*) FROM playlist_items pi WHERE pi.playlist_id = p.id), p.created_at, p.updated_at`

func (r *Repository) FindByOwner(ctx context.Context, ownerID string) ([]PlaylistDTO, error) {
	rows, err := r.pool.Query(ctx,
		"SELECT "+playlistWithCountColumns+" FROM playlists p WHERE p.owner_id = $1 ORDER BY p.created_at DESC", ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, scanPlaylistDTO)
}

func (r *Repository) FindPublicByOwner(ctx context.Context, ownerID string) ([]PlaylistDTO, error) {
	rows, err := r.pool.Query(ctx,
		"SELECT "+playlistWithCountColumns+" FROM playlists p WHERE p.owner_id = $1 AND p.is_public = TRUE ORDER BY p.created_at DESC", ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, scanPlaylistDTO)
}

// playlistRow is the bare row needed for ownership/visibility checks —
// unexported since it never leaves this package (the DTO above is what
// handlers actually serialize).
type playlistRow struct {
	ID       uuid.UUID
	OwnerID  string
	IsPublic bool
}

func (r *Repository) findRowByID(ctx context.Context, id uuid.UUID) (*playlistRow, error) {
	rows, err := r.pool.Query(ctx, "SELECT id, owner_id, is_public FROM playlists WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	row, err := pgx.CollectExactlyOneRow(rows, func(row pgx.CollectableRow) (playlistRow, error) {
		var p playlistRow
		err := row.Scan(&p.ID, &p.OwnerID, &p.IsPublic)
		return p, err
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *Repository) Insert(ctx context.Context, p *models.Playlist) error {
	p.ID = uuid.New()
	_, err := r.pool.Exec(ctx,
		"INSERT INTO playlists (id, owner_id, name, is_public, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6)",
		p.ID, p.OwnerID, p.Name, p.IsPublic, p.CreatedAt, p.UpdatedAt)
	return err
}

func (r *Repository) Update(ctx context.Context, id uuid.UUID, ownerID, name string, isPublic bool, updatedAt time.Time) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		"UPDATE playlists SET name = $1, is_public = $2, updated_at = $3 WHERE id = $4 AND owner_id = $5",
		name, isPublic, updatedAt, id, ownerID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID, ownerID string) (bool, error) {
	tag, err := r.pool.Exec(ctx, "DELETE FROM playlists WHERE id = $1 AND owner_id = $2", id, ownerID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

const itemColumns = "id, playlist_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, added_at"

func scanItem(row pgx.CollectableRow) (models.PlaylistItem, error) {
	var it models.PlaylistItem
	err := row.Scan(&it.ID, &it.PlaylistID, &it.RecordID, &it.TmdbID, &it.Title, &it.PosterImgURL, &it.TmdbRating, &it.Type, &it.VideoID, &it.AddedAt)
	return it, err
}

func (r *Repository) FindItems(ctx context.Context, playlistID uuid.UUID) ([]models.PlaylistItem, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+itemColumns+" FROM playlist_items WHERE playlist_id = $1 ORDER BY added_at DESC", playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, scanItem)
}

func (r *Repository) FindItemByRecordID(ctx context.Context, playlistID uuid.UUID, recordID string) (*models.PlaylistItem, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+itemColumns+" FROM playlist_items WHERE playlist_id = $1 AND record_id = $2", playlistID, recordID)
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

func (r *Repository) InsertItem(ctx context.Context, it *models.PlaylistItem) error {
	it.ID = uuid.New()
	_, err := r.pool.Exec(ctx,
		`INSERT INTO playlist_items (id, playlist_id, record_id, tmdb_id, title, poster_img_url, tmdb_rating, type, video_id, added_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		it.ID, it.PlaylistID, it.RecordID, it.TmdbID, it.Title, it.PosterImgURL, it.TmdbRating, it.Type, it.VideoID, it.AddedAt)
	return err
}

func (r *Repository) DeleteItem(ctx context.Context, playlistID uuid.UUID, recordID string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM playlist_items WHERE playlist_id = $1 AND record_id = $2", playlistID, recordID)
	return err
}

func (r *Repository) CountItems(ctx context.Context, playlistID uuid.UUID) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM playlist_items WHERE playlist_id = $1", playlistID).Scan(&count)
	return count, err
}

// Service mirrors watchlist.Service/likes.Service, plus the friendship-gated
// visibility check described in the package doc comment.
type Service struct {
	repo        *Repository
	friendsRepo *friends.Repository
}

func NewService(repo *Repository, friendsRepo *friends.Repository) *Service {
	return &Service{repo: repo, friendsRepo: friendsRepo}
}

func (s *Service) ListMine(ctx context.Context, clerkID string) ([]PlaylistDTO, error) {
	return s.repo.FindByOwner(ctx, clerkID)
}

func (s *Service) Create(ctx context.Context, clerkID, name string, isPublic bool) (*models.Playlist, error) {
	now := time.Now().UTC()
	p := &models.Playlist{OwnerID: clerkID, Name: name, IsPublic: isPublic, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.Insert(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) Update(ctx context.Context, clerkID string, id uuid.UUID, name string, isPublic bool) (bool, error) {
	return s.repo.Update(ctx, id, clerkID, name, isPublic, time.Now().UTC())
}

func (s *Service) Delete(ctx context.Context, clerkID string, id uuid.UUID) (bool, error) {
	return s.repo.Delete(ctx, id, clerkID)
}

// canView reports whether callerID may see a playlist's items — the owner
// always can; anyone else only if it's public AND they're friends with the
// owner.
func (s *Service) canView(ctx context.Context, callerID string, pl *playlistRow) (bool, error) {
	if pl.OwnerID == callerID {
		return true, nil
	}
	if !pl.IsPublic {
		return false, nil
	}
	return s.friendsRepo.AreFriends(ctx, callerID, pl.OwnerID)
}

func (s *Service) ListItems(ctx context.Context, callerID string, id uuid.UUID) ([]models.PlaylistItem, error) {
	pl, err := s.repo.findRowByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if pl == nil {
		return nil, ErrNotFound
	}
	ok, err := s.canView(ctx, callerID, pl)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	return s.repo.FindItems(ctx, id)
}

// AddItemRequest mirrors likes.AddLikeRequest's shape.
type AddItemRequest struct {
	RecordID     string  `json:"recordId"`
	TmdbID       *string `json:"tmdbId"`
	Title        *string `json:"title"`
	PosterImgURL *string `json:"posterImgURL"`
	TmdbRating   *string `json:"tmdbRating"`
	Type         *string `json:"type"`
	VideoID      *string `json:"videoId"`
}

// AddItem is idempotent (mirrors AddLike) and owner-only — adding to a
// playlist is never something a friend can do on your behalf.
func (s *Service) AddItem(ctx context.Context, clerkID string, id uuid.UUID, req AddItemRequest) (*models.PlaylistItem, error) {
	pl, err := s.repo.findRowByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if pl == nil || pl.OwnerID != clerkID {
		return nil, ErrNotFound
	}

	existing, err := s.repo.FindItemByRecordID(ctx, id, req.RecordID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	count, err := s.repo.CountItems(ctx, id)
	if err != nil {
		return nil, err
	}
	if count >= maxItemsPerPlaylist {
		return nil, ErrItemLimitReached
	}

	item := &models.PlaylistItem{
		PlaylistID:   id,
		RecordID:     req.RecordID,
		TmdbID:       req.TmdbID,
		Title:        req.Title,
		PosterImgURL: req.PosterImgURL,
		TmdbRating:   req.TmdbRating,
		Type:         req.Type,
		VideoID:      req.VideoID,
		AddedAt:      time.Now().UTC(),
	}
	if err := s.repo.InsertItem(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) RemoveItem(ctx context.Context, clerkID string, id uuid.UUID, recordID string) error {
	pl, err := s.repo.findRowByID(ctx, id)
	if err != nil {
		return err
	}
	if pl == nil || pl.OwnerID != clerkID {
		return ErrNotFound
	}
	return s.repo.DeleteItem(ctx, id, recordID)
}

// ListForUser is ListMine's counterpart for viewing someone else's
// playlists — their full list if callerID == targetID (viewing your own
// profile through the same code path), otherwise only their public
// playlists and only once callerID is confirmed as their friend. Not being
// friends yields an empty slice, not an error — same "just show nothing"
// shape friends.Search uses for non-matches, no need to leak *why*.
func (s *Service) ListForUser(ctx context.Context, callerID, targetID string) ([]PlaylistDTO, error) {
	if callerID == targetID {
		return s.repo.FindByOwner(ctx, targetID)
	}
	areFriends, err := s.friendsRepo.AreFriends(ctx, callerID, targetID)
	if err != nil {
		return nil, err
	}
	if !areFriends {
		return []PlaylistDTO{}, nil
	}
	return s.repo.FindPublicByOwner(ctx, targetID)
}
