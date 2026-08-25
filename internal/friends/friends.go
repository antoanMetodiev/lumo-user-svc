// Package friends implements the friend-request/friendship system: search
// other users, send/cancel/decline/accept requests, list friends, unfriend.
// There's no Java original to mirror here (new feature) — it follows the
// same Repository+Service+Handlers shape as internal/watchlist instead.
//
// A single friend_requests table represents both a pending request and an
// accepted friendship via its status column, exactly like a watchlist_items
// row represents "this is on the list" — DELETE for cancel/decline/unfriend,
// UPDATE status for accept, no separate friendships table.
package friends

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lumo-user-svc/internal/users"
)

const (
	StatusPending  = "PENDING"
	StatusAccepted = "ACCEPTED"
)

const (
	RelationshipNone            = "NONE"
	RelationshipPendingSent     = "PENDING_SENT"
	RelationshipPendingReceived = "PENDING_RECEIVED"
	RelationshipFriends         = "FRIENDS"
)

type FriendRequest struct {
	ID          uuid.UUID  `json:"id"`
	RequesterID string     `json:"requesterId"`
	AddresseeID string     `json:"addresseeId"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"createdAt"`
	RespondedAt *time.Time `json:"respondedAt"`
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const requestColumns = "id, requester_id, addressee_id, status, created_at, responded_at"

func scanRequest(row pgx.CollectableRow) (FriendRequest, error) {
	var fr FriendRequest
	err := row.Scan(&fr.ID, &fr.RequesterID, &fr.AddresseeID, &fr.Status, &fr.CreatedAt, &fr.RespondedAt)
	return fr, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*FriendRequest, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+requestColumns+" FROM friend_requests WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fr, err := pgx.CollectExactlyOneRow(rows, scanRequest)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &fr, nil
}

// FindBetween returns the friend_requests row involving both clerk ids
// regardless of which one is the requester, or (nil, nil) if none exists.
func (r *Repository) FindBetween(ctx context.Context, a, b string) (*FriendRequest, error) {
	rows, err := r.pool.Query(ctx,
		"SELECT "+requestColumns+" FROM friend_requests WHERE (requester_id = $1 AND addressee_id = $2) OR (requester_id = $2 AND addressee_id = $1)",
		a, b)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fr, err := pgx.CollectExactlyOneRow(rows, scanRequest)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &fr, nil
}

func (r *Repository) Insert(ctx context.Context, fr *FriendRequest) error {
	fr.ID = uuid.New()
	_, err := r.pool.Exec(ctx,
		`INSERT INTO friend_requests (id, requester_id, addressee_id, status, created_at)
		 VALUES ($1,$2,$3,$4,$5)`,
		fr.ID, fr.RequesterID, fr.AddresseeID, fr.Status, fr.CreatedAt)
	return err
}

func (r *Repository) UpdateStatusAccepted(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		"UPDATE friend_requests SET status = $2, responded_at = $3 WHERE id = $1",
		id, StatusAccepted, time.Now().UTC())
	return err
}

func (r *Repository) DeleteByID(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM friend_requests WHERE id = $1", id)
	return err
}

func (r *Repository) DeleteAcceptedBetween(ctx context.Context, a, b string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM friend_requests
		 WHERE status = $3 AND ((requester_id = $1 AND addressee_id = $2) OR (requester_id = $2 AND addressee_id = $1))`,
		a, b, StatusAccepted)
	return err
}

type FriendDTO struct {
	ClerkID         string     `json:"clerkId"`
	DisplayName     *string    `json:"displayName"`
	ProfileImageURL *string    `json:"profileImageURL"`
	FriendsSince    *time.Time `json:"friendsSince"`
}

func scanFriend(row pgx.CollectableRow) (FriendDTO, error) {
	var f FriendDTO
	err := row.Scan(&f.ClerkID, &f.DisplayName, &f.ProfileImageURL, &f.FriendsSince)
	return f, err
}

// ListFriends resolves "the other person" on each accepted row via a CASE
// against the caller's own id, joined once against users for display info.
func (r *Repository) ListFriends(ctx context.Context, clerkID string) ([]FriendDTO, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.clerk_id, u.display_name, u.profile_image_url, fr.responded_at
		FROM friend_requests fr
		JOIN users u ON u.clerk_id = CASE WHEN fr.requester_id = $1 THEN fr.addressee_id ELSE fr.requester_id END
		WHERE fr.status = $2 AND (fr.requester_id = $1 OR fr.addressee_id = $1)
		ORDER BY fr.responded_at DESC`,
		clerkID, StatusAccepted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, scanFriend)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []FriendDTO{}
	}
	return out, nil
}

type RequestDTO struct {
	ID              uuid.UUID `json:"id"`
	ClerkID         string    `json:"clerkId"`
	DisplayName     *string   `json:"displayName"`
	ProfileImageURL *string   `json:"profileImageURL"`
	CreatedAt       time.Time `json:"createdAt"`
}

func scanRequestDTO(row pgx.CollectableRow) (RequestDTO, error) {
	var d RequestDTO
	err := row.Scan(&d.ID, &d.ClerkID, &d.DisplayName, &d.ProfileImageURL, &d.CreatedAt)
	return d, err
}

func (r *Repository) ListIncoming(ctx context.Context, addresseeID string) ([]RequestDTO, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT fr.id, u.clerk_id, u.display_name, u.profile_image_url, fr.created_at
		FROM friend_requests fr
		JOIN users u ON u.clerk_id = fr.requester_id
		WHERE fr.addressee_id = $1 AND fr.status = $2
		ORDER BY fr.created_at DESC`,
		addresseeID, StatusPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, scanRequestDTO)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []RequestDTO{}
	}
	return out, nil
}

func (r *Repository) ListOutgoing(ctx context.Context, requesterID string) ([]RequestDTO, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT fr.id, u.clerk_id, u.display_name, u.profile_image_url, fr.created_at
		FROM friend_requests fr
		JOIN users u ON u.clerk_id = fr.addressee_id
		WHERE fr.requester_id = $1 AND fr.status = $2
		ORDER BY fr.created_at DESC`,
		requesterID, StatusPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, scanRequestDTO)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []RequestDTO{}
	}
	return out, nil
}

type SearchResultDTO struct {
	ClerkID         string  `json:"clerkId"`
	DisplayName     *string `json:"displayName"`
	ProfileImageURL *string `json:"profileImageURL"`
	Relationship    string  `json:"relationship"`
}

// scanSearchResults is shared by SearchUsers and RecentUsers — both queries
// select the exact same five columns (clerk_id, display_name,
// profile_image_url, friend_requests.status, friend_requests.requester_id),
// just with a different WHERE/ORDER BY, so the relationship-classifying scan
// loop only needs to exist once.
func scanSearchResults(rows pgx.Rows, callerID string) ([]SearchResultDTO, error) {
	out := []SearchResultDTO{}
	for rows.Next() {
		var res SearchResultDTO
		var status, otherRequester *string
		if err := rows.Scan(&res.ClerkID, &res.DisplayName, &res.ProfileImageURL, &status, &otherRequester); err != nil {
			return nil, err
		}
		switch {
		case status == nil:
			res.Relationship = RelationshipNone
		case *status == StatusAccepted:
			res.Relationship = RelationshipFriends
		case otherRequester != nil && *otherRequester == callerID:
			res.Relationship = RelationshipPendingSent
		default:
			res.Relationship = RelationshipPendingReceived
		}
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SearchUsers mirrors lumo-movies-svc's SuggestActors query shape (LOWER+LIKE
// substring match, prefix matches ranked first) applied to display_name,
// with a LEFT JOIN against friend_requests to compute each result's
// relationship to the caller in the same query instead of N+1 lookups.
func (r *Repository) SearchUsers(ctx context.Context, callerID, query string, limit int) ([]SearchResultDTO, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.clerk_id, u.display_name, u.profile_image_url, fr.status, fr.requester_id
		FROM users u
		LEFT JOIN friend_requests fr
		  ON (fr.requester_id = u.clerk_id AND fr.addressee_id = $1)
		  OR (fr.addressee_id = u.clerk_id AND fr.requester_id = $1)
		WHERE u.clerk_id <> $1
		  AND LOWER(u.display_name) LIKE LOWER('%' || $2 || '%')
		ORDER BY
		  CASE WHEN LOWER(u.display_name) LIKE LOWER($2 || '%') THEN 0 ELSE 1 END,
		  u.display_name
		LIMIT $3`,
		callerID, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSearchResults(rows, callerID)
}

// RecentUsers mirrors SearchUsers but orders by signup recency instead of a
// name match — this is what the Search tab shows before the caller has
// typed anything, as a lightweight "who's new here" default.
func (r *Repository) RecentUsers(ctx context.Context, callerID string, limit int) ([]SearchResultDTO, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.clerk_id, u.display_name, u.profile_image_url, fr.status, fr.requester_id
		FROM users u
		LEFT JOIN friend_requests fr
		  ON (fr.requester_id = u.clerk_id AND fr.addressee_id = $1)
		  OR (fr.addressee_id = u.clerk_id AND fr.requester_id = $1)
		WHERE u.clerk_id <> $1
		ORDER BY u.created_at DESC
		LIMIT $2`,
		callerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSearchResults(rows, callerID)
}

// Service mirrors watchlist.Service's shape: business logic lives here, the
// repository stays a thin SQL layer.
type Service struct {
	repo      *Repository
	usersRepo *users.Repository
	notifier  *NotifyClient
}

func NewService(repo *Repository, usersRepo *users.Repository, notifier *NotifyClient) *Service {
	return &Service{repo: repo, usersRepo: usersRepo, notifier: notifier}
}

// notifyAsync persists a notification row (surfaced later via
// ConsumeNotifications, e.g. the frontend's notification bell) and pushes a
// live WebSocket event, together, fire-and-forget. Must not reuse ctx
// directly inside the goroutine: net/http cancels the request context as
// soon as the HTTP handler returns, which happens right after this call —
// same lesson as the context-cancellation bug fixed earlier this session in
// lumo-movies-generator-by-actors and lumo-series-svc's fire-and-forget
// handlers.
func (s *Service) notifyAsync(ctx context.Context, targetClerkID, notifType, eventType, actorID string, fr *FriendRequest) {
	detached := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("friends: notifyAsync panic: %v", rec)
			}
		}()
		if err := s.repo.InsertNotification(detached, targetClerkID, notifType, actorID); err != nil {
			log.Printf("friends: failed to insert %s notification for clerkId=%s: %v", notifType, targetClerkID, err)
		}
		s.notifier.Notify(detached, targetClerkID, eventType, fr)
	}()
}

var (
	ErrSelfRequest  = errors.New("cannot send a friend request to yourself")
	ErrUserNotFound = errors.New("user not found")
)

// SendRequest is idempotent: resending to someone already pending/friends
// just returns the existing row unchanged, and sending to someone who
// already sent *us* a pending request auto-accepts instead of creating a
// second, reverse-direction row.
func (s *Service) SendRequest(ctx context.Context, requesterID, addresseeID string) (*FriendRequest, error) {
	if requesterID == addresseeID {
		return nil, ErrSelfRequest
	}

	exists, err := s.usersRepo.ExistsByClerkID(ctx, addresseeID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrUserNotFound
	}

	existing, err := s.repo.FindBetween(ctx, requesterID, addresseeID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Status == StatusPending && existing.RequesterID == addresseeID {
			if err := s.repo.UpdateStatusAccepted(ctx, existing.ID); err != nil {
				return nil, err
			}
			existing.Status = StatusAccepted
			// existing.RequesterID (== addresseeID here) is the person who
			// sent the original request — they're the one who just found
			// out it's accepted, by requesterID's shortcut of "sending" a
			// request back to them.
			s.notifyAsync(ctx, existing.RequesterID, NotificationFriendRequestAccepted, EventFriendRequestAccepted, requesterID, existing)
		}
		return existing, nil
	}

	fr := &FriendRequest{RequesterID: requesterID, AddresseeID: addresseeID, Status: StatusPending, CreatedAt: time.Now().UTC()}
	if err := s.repo.Insert(ctx, fr); err != nil {
		return nil, err
	}

	s.notifyAsync(ctx, addresseeID, NotificationFriendRequest, EventFriendRequest, requesterID, fr)

	return fr, nil
}

// CancelOrDecline deletes a pending request — the same operation whether
// it's the requester canceling their own outgoing request or the addressee
// declining an incoming one. A no-op (not an error) when the request is
// missing or the caller isn't a participant, matching watchlist's delete
// convention (and not leaking whether a given id exists to non-participants).
func (s *Service) CancelOrDecline(ctx context.Context, callerID string, id uuid.UUID) error {
	fr, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if fr == nil || (fr.RequesterID != callerID && fr.AddresseeID != callerID) {
		return nil
	}
	if err := s.repo.DeleteByID(ctx, id); err != nil {
		return err
	}

	// Only notify when the addressee is the one declining someone else's
	// request — the requester canceling their own outgoing request doesn't
	// need to be told, they already know, they just did it.
	if fr.Status == StatusPending && callerID == fr.AddresseeID {
		s.notifyAsync(ctx, fr.RequesterID, NotificationFriendRequestDeclined, EventFriendRequestDeclined, callerID, fr)
	}
	return nil
}

// Accept flips a pending request to accepted — only its addressee may
// accept. Returns false when the request doesn't exist, isn't pending, or
// the caller isn't its addressee; the handler maps false to 404.
func (s *Service) Accept(ctx context.Context, callerID string, id uuid.UUID) (bool, error) {
	fr, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return false, err
	}
	if fr == nil || fr.Status != StatusPending || fr.AddresseeID != callerID {
		return false, nil
	}
	if err := s.repo.UpdateStatusAccepted(ctx, id); err != nil {
		return false, err
	}
	fr.Status = StatusAccepted
	s.notifyAsync(ctx, fr.RequesterID, NotificationFriendRequestAccepted, EventFriendRequestAccepted, callerID, fr)
	return true, nil
}

func (s *Service) Unfriend(ctx context.Context, callerID, otherClerkID string) error {
	return s.repo.DeleteAcceptedBetween(ctx, callerID, otherClerkID)
}

func (s *Service) ListFriends(ctx context.Context, clerkID string) ([]FriendDTO, error) {
	return s.repo.ListFriends(ctx, clerkID)
}

func (s *Service) ListIncoming(ctx context.Context, clerkID string) ([]RequestDTO, error) {
	return s.repo.ListIncoming(ctx, clerkID)
}

func (s *Service) ListOutgoing(ctx context.Context, clerkID string) ([]RequestDTO, error) {
	return s.repo.ListOutgoing(ctx, clerkID)
}

// Search falls back to RecentUsers (most recently joined, excluding the
// caller) for a blank query — the frontend's Search tab calls this
// unconditionally, blank query included, so people always see *something*
// there rather than an empty state before they've typed anything.
func (s *Service) Search(ctx context.Context, callerID, query string) ([]SearchResultDTO, error) {
	if query == "" {
		return s.repo.RecentUsers(ctx, callerID, 20)
	}
	return s.repo.SearchUsers(ctx, callerID, query, 20)
}
