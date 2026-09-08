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
//
// ORDER BY created_at + LIMIT 1 rather than CollectExactlyOneRow: nothing at
// the database level stops a pair from ending up with two rows (A→B and B→A
// both inserted by two SendRequest calls that raced past each other's
// FindBetween check), and CollectExactlyOneRow turned that into a permanent
// ErrTooManyRows — every subsequent send/accept between those two people
// failing with a 500 forever, with no way out from the UI. Taking the oldest
// row instead means the pair just resolves through the request that was made
// first, and the duplicate is cleaned up by the same accept/decline that
// resolves it.
func (r *Repository) FindBetween(ctx context.Context, a, b string) (*FriendRequest, error) {
	rows, err := r.pool.Query(ctx,
		"SELECT "+requestColumns+" FROM friend_requests WHERE (requester_id = $1 AND addressee_id = $2) OR (requester_id = $2 AND addressee_id = $1) ORDER BY created_at LIMIT 1",
		a, b)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fr, err := pgx.CollectOneRow(rows, scanRequest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &fr, nil
}

// DeleteBetween removes every pending request between two people in either
// direction — used once a pair is resolved (accepted), so a duplicate
// reverse-direction row left over from the race described on FindBetween
// can't survive as a phantom "pending" request between two people who are
// now already friends.
func (r *Repository) DeleteBetween(ctx context.Context, a, b string) error {
	_, err := r.pool.Exec(ctx,
		"DELETE FROM friend_requests WHERE (requester_id = $1 AND addressee_id = $2) OR (requester_id = $2 AND addressee_id = $1)",
		a, b)
	return err
}

// Insert also stamps requester_email/requester_display_name/addressee_email/
// addressee_display_name — pulled straight from users in the same
// INSERT...SELECT rather than a separate round trip. Purely for a human
// glancing at the raw table to see who a row is about without a join; the
// app itself never reads these back (every API response already joins
// against users live, which stays accurate even if someone renames their
// account later — these columns are a snapshot, not a second source of truth).
//
// Because it's an INSERT...SELECT over users, a missing users row for either
// participant makes it insert nothing at all while still returning a nil
// error — which used to read as success all the way back up: the caller got
// a FriendRequest carrying an id no row has, the addressee got a "you have a
// friend request" push and notification, and their incoming list stayed
// empty forever. (The realistic way to land there is a brand-new account
// whose Clerk user.created webhook hasn't been processed yet.) Hence the
// RowsAffected check: no row inserted is ErrUserNotFound, not success.
func (r *Repository) Insert(ctx context.Context, fr *FriendRequest) error {
	fr.ID = uuid.New()
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO friend_requests (id, requester_id, requester_email, requester_display_name,
		                              addressee_id, addressee_email, addressee_display_name, created_at)
		SELECT $1, $2, ru.email, ru.display_name, $3, au.email, au.display_name, $4
		FROM users ru, users au
		WHERE ru.clerk_id = $2 AND au.clerk_id = $3`,
		fr.ID, fr.RequesterID, fr.AddresseeID, fr.CreatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
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
//
// It shares Insert's INSERT...SELECT-over-users hazard, and here it was
// worse: acceptRequest inserts the friendship and then deletes the request
// row, so a silent zero-row insert destroyed the request while creating no
// friendship — the request simply vanished from both people's lists and
// neither became the other's friend, with a 200 reported to the accepter.
// Zero rows here is ambiguous though (ON CONFLICT DO NOTHING also affects
// none when they're already friends, which is a legitimate success), so the
// rare zero-row path confirms which case it is before deciding.
func (r *Repository) InsertFriendship(ctx context.Context, a, b string) error {
	userA, userB := orderPair(a, b)
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO friendships (id, user_a_id, user_a_email, user_a_display_name,
		                          user_b_id, user_b_email, user_b_display_name, created_at)
		SELECT $1, $2, ua.email, ua.display_name, $3, ub.email, ub.display_name, $4
		FROM users ua, users ub
		WHERE ua.clerk_id = $2 AND ub.clerk_id = $3
		ON CONFLICT (user_a_id, user_b_id) DO NOTHING`,
		uuid.New(), userA, userB, time.Now().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	alreadyFriends, err := r.AreFriends(ctx, userA, userB)
	if err != nil {
		return err
	}
	if !alreadyFriends {
		return ErrUserNotFound
	}
	return nil
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

// friendships' rows are written with (user_a_id, user_b_id) ordered by
// orderPair, which sorts by plain Go byte-order — NOT the same ordering
// Postgres's LEAST/GREATEST would produce under this DB's collation (which
// can disagree with byte-order on mixed-case clerk_ids, e.g. an uppercase
// letter sorting after a lowercase one under a locale-aware collation even
// though it's smaller in ASCII). Joining via LEAST/GREATEST silently missed
// real rows whenever the two orderings disagreed, showing "Add Friend" for
// people who were already friends. Matching directly against both possible
// (user_a_id, user_b_id) arrangements — same as ListFriendships already
// does — sidesteps collation entirely instead of needing it to agree with
// orderPair.
//
// The friend_requests side is a LATERAL ... LIMIT 1 rather than a plain
// LEFT JOIN because nothing stops a pair from holding two pending rows (see
// FindBetween) — a plain join duplicated that person in the search results,
// once per row, each rendered as its own list entry.
const searchJoins = `
	FROM users u
	LEFT JOIN friendships f
	  ON (f.user_a_id = u.clerk_id AND f.user_b_id = $1)
	  OR (f.user_a_id = $1 AND f.user_b_id = u.clerk_id)
	LEFT JOIN LATERAL (
	  SELECT r.requester_id
	  FROM friend_requests r
	  WHERE (r.requester_id = u.clerk_id AND r.addressee_id = $1)
	     OR (r.addressee_id = u.clerk_id AND r.requester_id = $1)
	  ORDER BY r.created_at
	  LIMIT 1
	) fr ON TRUE`

// SearchUsers mirrors lumo-movies-svc's SuggestActors query shape (LOWER+LIKE
// substring match, prefix matches ranked first), matching on display_name or
// email.
//
// Both are COALESCEd to ”: display_name is nullable, and comparing NULL with
// LIKE yields NULL rather than false, so anyone who hadn't set a display name
// was unfindable by search at all — they only ever appeared in the blank-query
// RecentUsers listing, which drops off the moment someone actually types.
// Email is matched (never returned — see SearchResultDTO) because it's the
// one identifier people reliably know about each other when trying to add a
// specific person rather than browse.
func (r *Repository) SearchUsers(ctx context.Context, callerID, query string, limit int) ([]SearchResultDTO, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+searchSelectColumns+searchJoins+`
		WHERE u.clerk_id <> $1
		  AND (LOWER(COALESCE(u.display_name, '')) LIKE LOWER('%' || $2 || '%')
		    OR LOWER(COALESCE(u.email, '')) LIKE LOWER('%' || $2 || '%'))
		ORDER BY
		  CASE WHEN LOWER(COALESCE(u.display_name, '')) LIKE LOWER($2 || '%') THEN 0 ELSE 1 END,
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
			// A push whose notification row failed to persist is worse than
			// no push: the bell's count comes from that row, so the client
			// would bump its badge on the event and then find nothing behind
			// it, leaving a badge that never clears.
			return
		}
		s.notifier.Notify(detached, targetClerkID, eventType, fr)
	}()
}

// pushAsync is notifyAsync minus the persisted row — for state changes the
// other side needs to re-render for (a request being withdrawn, a friendship
// being ended) but that don't belong in the notification bell as something
// that happened *to* them worth reading later.
func (s *Service) pushAsync(ctx context.Context, targetClerkID, eventType string, fr *FriendRequest) {
	detached := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("friends: pushAsync panic: %v", rec)
			}
		}()
		s.notifier.Notify(detached, targetClerkID, eventType, fr)
	}()
}

// clearRequestNotification drops the "X sent you a friend request" row once
// that request no longer exists — whether it was accepted, declined, or
// withdrawn by the sender. It's a prompt to act on a pending request, not a
// historical record, so leaving it behind meant a bell badge pointing at a
// request that was already gone (and, for a withdrawn one, at a request the
// recipient never even got the chance to see).
func (s *Service) clearRequestNotification(ctx context.Context, addresseeID, requesterID string) {
	detached := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("friends: clearRequestNotification panic: %v", rec)
			}
		}()
		if err := s.repo.DeleteNotification(detached, addresseeID, NotificationFriendRequest, requesterID); err != nil {
			log.Printf("friends: failed to clear friend-request notification for clerkId=%s: %v", addresseeID, err)
		}
	}()
}

var (
	ErrSelfRequest  = errors.New("cannot send a friend request to yourself")
	ErrUserNotFound = errors.New("user not found")
)

// SendOutcome is what a "send request" actually resolved to, since it isn't
// always a new pending request: sending to someone who already has a pending
// request out to you accepts it instead, and sending to someone you're
// already friends with changes nothing. The handler previously returned a
// bare (nil, nil) for both of those, which serialized to a literal `null`
// body under a 200 — the frontend read that as plain success and drew a
// "request sent, pending" state over someone who was, at that exact moment,
// already a confirmed friend.
type SendOutcome struct {
	Status  string         `json:"status"`
	Request *FriendRequest `json:"request,omitempty"`
}

const (
	SendStatusPending = "PENDING"
	SendStatusFriends = "FRIENDS"
)

// SendRequest is idempotent: resending to someone already pending just
// returns the existing pending row unchanged, resending to someone already
// a friend reports FRIENDS without changing anything, and sending to
// someone who already sent *us* a pending request accepts it on the spot
// (moves it into friendships) instead of creating a second, reverse-direction
// request row. The SendOutcome says which of those happened — see its doc
// comment for why that can't just be inferred from a 200.
func (s *Service) SendRequest(ctx context.Context, requesterID, addresseeID string) (*SendOutcome, error) {
	if requesterID == addresseeID {
		return nil, ErrSelfRequest
	}

	// Both sides are checked, not just the addressee: Insert is an
	// INSERT...SELECT across users for *both* participants, so a caller
	// missing from users (a brand-new account whose Clerk webhook hasn't
	// landed yet) is just as unable to produce a row — and reporting that as
	// "user not found" here is far more useful than the 500 the repository's
	// own guard would otherwise raise.
	for _, clerkID := range []string{requesterID, addresseeID} {
		exists, err := s.usersRepo.ExistsByClerkID(ctx, clerkID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrUserNotFound
		}
	}

	alreadyFriends, err := s.repo.AreFriends(ctx, requesterID, addresseeID)
	if err != nil {
		return nil, err
	}
	if alreadyFriends {
		return &SendOutcome{Status: SendStatusFriends}, nil
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
			return &SendOutcome{Status: SendStatusFriends}, nil
		}
		// We already sent one — idempotent, return the existing pending request.
		return &SendOutcome{Status: SendStatusPending, Request: existing}, nil
	}

	fr := &FriendRequest{RequesterID: requesterID, AddresseeID: addresseeID, CreatedAt: time.Now().UTC()}
	if err := s.repo.Insert(ctx, fr); err != nil {
		return nil, err
	}

	s.notifyAsync(ctx, addresseeID, NotificationFriendRequest, EventFriendRequest, requesterID, fr)

	return &SendOutcome{Status: SendStatusPending, Request: fr}, nil
}

// acceptRequest is the shared "turn a pending request into a friendship"
// step used by both Accept and SendRequest's auto-accept-on-reverse-pending
// branch: insert into friendships, delete every pending request between the
// pair (DeleteBetween rather than DeleteByID, so a duplicate
// reverse-direction row can't outlive the friendship it's now contradicting
// — see FindBetween), notify the original requester.
//
// The friendship insert has to come first and its error has to stop the
// whole thing: deleting the request before knowing the friendship exists is
// how a request could disappear without one replacing it.
func (s *Service) acceptRequest(ctx context.Context, fr *FriendRequest) error {
	if err := s.repo.InsertFriendship(ctx, fr.RequesterID, fr.AddresseeID); err != nil {
		return err
	}
	if err := s.repo.DeleteBetween(ctx, fr.RequesterID, fr.AddresseeID); err != nil {
		return err
	}
	s.clearRequestNotification(ctx, fr.AddresseeID, fr.RequesterID)
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

	// Either way the addressee's "X sent you a friend request" prompt is now
	// pointing at nothing, so it goes with the request itself.
	s.clearRequestNotification(ctx, fr.AddresseeID, fr.RequesterID)

	if callerID == fr.AddresseeID {
		s.notifyAsync(ctx, fr.RequesterID, NotificationFriendRequestDeclined, EventFriendRequestDeclined, callerID, fr)
		return nil
	}

	// The requester canceling their own outgoing request doesn't need a
	// notification — they already know, they just did it — but the addressee
	// does need to hear about it, and used to hear nothing at all. Their
	// incoming list kept showing the withdrawn request until the next poll,
	// and accepting it in that window 404'd with no visible explanation and
	// no list refresh, so the dead entry just sat there. This is a push with
	// no notification row: a request being withdrawn isn't bell-worthy, it
	// just means their list is stale.
	s.pushAsync(ctx, fr.AddresseeID, EventFriendRequestCanceled, fr)
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

// Unfriend also pushes to the person being removed so their own friends
// list, and everything gated on it, stops showing a friendship that no
// longer exists — without it their session kept the ex-friend listed (and
// kept their profile's likes/playlists unlocked, since the frontend derives
// "is a friend" from that same cached list) until a full page reload. No
// notification row: being unfriended is deliberately not something the bell
// announces.
func (s *Service) Unfriend(ctx context.Context, callerID, otherClerkID string) error {
	if err := s.repo.DeleteFriendship(ctx, callerID, otherClerkID); err != nil {
		return err
	}
	s.pushAsync(ctx, otherClerkID, EventFriendRemoved, nil)
	return nil
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
