// Package friends implements the friend-request/friendship system: search
// other users, send/cancel/decline/accept requests, list friends, unfriend.
// There's no Java original to mirror here (new feature) — it follows the
// same Repository+Service+Handlers shape as internal/watchlist instead.
//
// Two tables, not one: friend_requests holds only pending requests (a row's
// mere existence means "pending" — no status column, deleted the instant
// it's resolved one way or another) and friendships holds accepted
// friendships as their own first-class relation, independent of whatever
// request led to them. Accepting moves a row from one table to the other;
// declining/canceling just deletes it; unfriending deletes straight out of
// friendships. This was a deliberate correction from an earlier
// single-table design (a friend_requests row with a status column standing
// in for the friendship itself) — that shape made it awkward to keep a
// declined request around (e.g. for a future block-list feature) or reason
// about "friendship" and "request" as the distinct entities they are.
package friends

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lumo-user-svc/internal/dbx"
	"lumo-user-svc/internal/users"
)

const (
	RelationshipNone            = "NONE"
	RelationshipPendingSent     = "PENDING_SENT"
	RelationshipPendingReceived = "PENDING_RECEIVED"
	RelationshipFriends         = "FRIENDS"
)

// FriendRequest is always pending — see the package doc comment.
type FriendRequest struct {
	ID          uuid.UUID `json:"id"`
	RequesterID string    `json:"requesterId"`
	AddresseeID string    `json:"addresseeId"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Friendship is symmetric — UserAID/UserBID are just storage slots, always
// populated in a canonical (lexicographically sorted) order via orderPair
// so a given pair of people can only ever match one row, checkable from
// either direction without an OR.
type Friendship struct {
	ID        uuid.UUID
	UserAID   string
	UserBID   string
	CreatedAt time.Time
}

// orderPair returns (a, b) sorted so the same two people always produce the
// same (userAID, userBID) pair regardless of who's "a" and who's "b" in the
// caller's own head — this is what lets friendships enforce "at most one
// row per pair" with a plain UNIQUE constraint instead of a pair of
// OR-conditions everywhere.
func orderPair(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

type Repository struct {
	pool dbx.Querier
}

func NewRepository(pool dbx.Querier) *Repository {
	return &Repository{pool: pool}
}

const requestColumns = "id, requester_id, addressee_id, created_at"

func scanRequest(row pgx.CollectableRow) (FriendRequest, error) {
	var fr FriendRequest
	err := row.Scan(&fr.ID, &fr.RequesterID, &fr.AddresseeID, &fr.CreatedAt)
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

// FindBetween returns the pending friend_requests row involving both clerk
// ids regardless of which one is the requester, or (nil, nil) if none exists.
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

// Insert also stamps requester_email/requester_display_name/addressee_email/
// addressee_display_name — pulled straight from users in the same
// INSERT...SELECT rather than a separate round trip. Purely for a human
// glancing at the raw table to see who a row is about without a join; the
// app itself never reads these back (every API response already joins
// against users live, which stays accurate even if someone renames their
// account later — these columns are a snapshot, not a second source of truth).
func (r *Repository) Insert(ctx context.Context, fr *FriendRequest) error {
	fr.ID = uuid.New()
	_, err := r.pool.Exec(ctx, `
		INSERT INTO friend_requests (id, requester_id, requester_email, requester_display_name,
		                              addressee_id, addressee_email, addressee_display_name, created_at)
		SELECT $1, $2, ru.email, ru.display_name, $3, au.email, au.display_name, $4
		FROM users ru, users au
		WHERE ru.clerk_id = $2 AND au.clerk_id = $3`,
		fr.ID, fr.RequesterID, fr.AddresseeID, fr.CreatedAt)
	return err
}

func (r *Repository) DeleteByID(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM friend_requests WHERE id = $1", id)
	return err
}

// InsertFriendship is idempotent (ON CONFLICT DO NOTHING on the
// (user_a_id, user_b_id) unique constraint) — the two call sites (Accept,
// and SendRequest's auto-accept-on-reverse-pending branch) both already
// checked this pair wasn't already friends, but this stays safe under a
// race rather than surfacing a constraint-violation error. Also stamps
// user_a_email/user_a_display_name/user_b_email/user_b_display_name — see
// Insert's doc comment for why (same reasoning, snapshot for humans reading
// the raw table, not read back by the app).
func (r *Repository) InsertFriendship(ctx context.Context, a, b string) error {
	userA, userB := orderPair(a, b)
	_, err := r.pool.Exec(ctx, `
		INSERT INTO friendships (id, user_a_id, user_a_email, user_a_display_name,
		                          user_b_id, user_b_email, user_b_display_name, created_at)
		SELECT $1, $2, ua.email, ua.display_name, $3, ub.email, ub.display_name, $4
		FROM users ua, users ub
		WHERE ua.clerk_id = $2 AND ub.clerk_id = $3
		ON CONFLICT (user_a_id, user_b_id) DO NOTHING`,
		uuid.New(), userA, userB, time.Now().UTC())
	return err
}

func (r *Repository) AreFriends(ctx context.Context, a, b string) (bool, error) {
	userA, userB := orderPair(a, b)
	var exists bool
	err := r.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)", userA, userB).Scan(&exists)
	return exists, err
}

func (r *Repository) DeleteFriendship(ctx context.Context, a, b string) error {
	userA, userB := orderPair(a, b)
	_, err := r.pool.Exec(ctx, "DELETE FROM friendships WHERE user_a_id = $1 AND user_b_id = $2", userA, userB)
	return err
}

type FriendDTO struct {
	ClerkID         string    `json:"clerkId"`
	DisplayName     *string   `json:"displayName"`
	ProfileImageURL *string   `json:"profileImageURL"`
	FriendsSince    time.Time `json:"friendsSince"`
}

func scanFriend(row pgx.CollectableRow) (FriendDTO, error) {
	var f FriendDTO
	err := row.Scan(&f.ClerkID, &f.DisplayName, &f.ProfileImageURL, &f.FriendsSince)
	return f, err
}

// ListFriendships resolves "the other person" on each row via a CASE
// against the caller's own id, joined once against users for display info.
func (r *Repository) ListFriendships(ctx context.Context, clerkID string) ([]FriendDTO, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.clerk_id, u.display_name, u.profile_image_url, f.created_at
		FROM friendships f
		JOIN users u ON u.clerk_id = CASE WHEN f.user_a_id = $1 THEN f.user_b_id ELSE f.user_a_id END
		WHERE f.user_a_id = $1 OR f.user_b_id = $1
		ORDER BY f.created_at DESC`,
		clerkID)
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
		WHERE fr.addressee_id = $1
		ORDER BY fr.created_at DESC`,
		addresseeID)
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
		WHERE fr.requester_id = $1
		ORDER BY fr.created_at DESC`,
		requesterID)
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
// select the exact same columns, just with a different WHERE/ORDER BY, so
// the relationship-classifying scan loop only needs to exist once.
// is_friend comes from a LEFT JOIN against friendships (using the same
// LEAST/GREATEST canonical ordering as orderPair, done in SQL here since
// it's per-row against every candidate rather than a single pair);
// otherRequester comes from a LEFT JOIN against friend_requests and is only
// meaningful when is_friend is false.
func scanSearchResults(rows pgx.Rows, callerID string) ([]SearchResultDTO, error) {
	out := []SearchResultDTO{}
	for rows.Next() {
		var res SearchResultDTO
		var isFriend bool
		var otherRequester *string
		if err := rows.Scan(&res.ClerkID, &res.DisplayName, &res.ProfileImageURL, &isFriend, &otherRequester); err != nil {
			return nil, err
		}
		switch {
		case isFriend:
			res.Relationship = RelationshipFriends
		case otherRequester == nil:
			res.Relationship = RelationshipNone
		case *otherRequester == callerID:
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

const searchSelectColumns = `
	u.clerk_id, u.display_name, u.profile_image_url,
	f.id IS NOT NULL AS is_friend,
	fr.requester_id`

const searchJoins = `
	FROM users u
	LEFT JOIN friendships f
	  ON f.user_a_id = LEAST(u.clerk_id, $1) AND f.user_b_id = GREATEST(u.clerk_id, $1)
	LEFT JOIN friend_requests fr
	  ON (fr.requester_id = u.clerk_id AND fr.addressee_id = $1)
	  OR (fr.addressee_id = u.clerk_id AND fr.requester_id = $1)`

// SearchUsers mirrors lumo-movies-svc's SuggestActors query shape (LOWER+LIKE
// substring match, prefix matches ranked first) applied to display_name.
func (r *Repository) SearchUsers(ctx context.Context, callerID, query string, limit int) ([]SearchResultDTO, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+searchSelectColumns+searchJoins+`
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
		SELECT `+searchSelectColumns+searchJoins+`
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

// SendRequest is idempotent: resending to someone already pending just
// returns the existing pending row unchanged, resending to someone already
// a friend is a no-op (nil, nil — nothing new to report), and sending to
// someone who already sent *us* a pending request accepts it on the spot
// (moves it into friendships) instead of creating a second, reverse-direction
// request row.
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

	alreadyFriends, err := s.repo.AreFriends(ctx, requesterID, addresseeID)
	if err != nil {
		return nil, err
	}
	if alreadyFriends {
		return nil, nil
	}

	existing, err := s.repo.FindBetween(ctx, requesterID, addresseeID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.RequesterID == addresseeID {
			// They already asked us — this "send" is really an accept.
			if err := s.acceptRequest(ctx, existing); err != nil {
				return nil, err
			}
			return nil, nil
		}
		// We already sent one — idempotent, return the existing pending request.
		return existing, nil
	}

	fr := &FriendRequest{RequesterID: requesterID, AddresseeID: addresseeID, CreatedAt: time.Now().UTC()}
	if err := s.repo.Insert(ctx, fr); err != nil {
		return nil, err
	}

	s.notifyAsync(ctx, addresseeID, NotificationFriendRequest, EventFriendRequest, requesterID, fr)

	return fr, nil
}

// acceptRequest is the shared "turn a pending request into a friendship"
// step used by both Accept and SendRequest's auto-accept-on-reverse-pending
// branch: insert into friendships, delete the now-fulfilled request row,
// notify the original requester.
func (s *Service) acceptRequest(ctx context.Context, fr *FriendRequest) error {
	if err := s.repo.InsertFriendship(ctx, fr.RequesterID, fr.AddresseeID); err != nil {
		return err
	}
	if err := s.repo.DeleteByID(ctx, fr.ID); err != nil {
		return err
	}
	s.notifyAsync(ctx, fr.RequesterID, NotificationFriendRequestAccepted, EventFriendRequestAccepted, fr.AddresseeID, fr)
	return nil
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
	if callerID == fr.AddresseeID {
		s.notifyAsync(ctx, fr.RequesterID, NotificationFriendRequestDeclined, EventFriendRequestDeclined, callerID, fr)
	}
	return nil
}

// Accept turns a pending request into a friendship — only its addressee may
// accept. Returns false when the request doesn't exist or the caller isn't
// its addressee; the handler maps false to 404.
func (s *Service) Accept(ctx context.Context, callerID string, id uuid.UUID) (bool, error) {
	fr, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return false, err
	}
	if fr == nil || fr.AddresseeID != callerID {
		return false, nil
	}
	if err := s.acceptRequest(ctx, fr); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) Unfriend(ctx context.Context, callerID, otherClerkID string) error {
	return s.repo.DeleteFriendship(ctx, callerID, otherClerkID)
}

func (s *Service) ListFriends(ctx context.Context, clerkID string) ([]FriendDTO, error) {
	return s.repo.ListFriendships(ctx, clerkID)
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
