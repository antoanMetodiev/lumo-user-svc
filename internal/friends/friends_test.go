package friends

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	"lumo-user-svc/internal/users"
)

// SQL text used by more than one test below. pgxmock's default matcher
// strips all whitespace runs (including newlines/tabs) down to single
// spaces before comparing, on both the expected and actual side (see
// pgxmock's stripQuery) — so these single-line strings match the
// multi-line raw-string queries in friends.go regardless of indentation.
const (
	findByIDSQL             = "SELECT " + requestColumns + " FROM friend_requests WHERE id = $1"
	findBetweenSQL          = "SELECT " + requestColumns + " FROM friend_requests WHERE (requester_id = $1 AND addressee_id = $2) OR (requester_id = $2 AND addressee_id = $1) ORDER BY created_at LIMIT 1"
	insertFriendRequestSQL  = "INSERT INTO friend_requests (id, requester_id, requester_email, requester_display_name, addressee_id, addressee_email, addressee_display_name, created_at) SELECT $1, $2, ru.email, ru.display_name, $3, au.email, au.display_name, $4 FROM users ru, users au WHERE ru.clerk_id = $2 AND au.clerk_id = $3"
	deleteRequestByIDSQL    = "DELETE FROM friend_requests WHERE id = $1"
	deleteRequestBetweenSQL = "DELETE FROM friend_requests WHERE (requester_id = $1 AND addressee_id = $2) OR (requester_id = $2 AND addressee_id = $1)"
	deleteNotificationSQL   = "DELETE FROM notifications WHERE clerk_id = $1 AND type = $2 AND actor_id = $3"
	insertFriendshipSQL     = "INSERT INTO friendships (id, user_a_id, user_a_email, user_a_display_name, user_b_id, user_b_email, user_b_display_name, created_at) SELECT $1, $2, ua.email, ua.display_name, $3, ub.email, ub.display_name, $4 FROM users ua, users ub WHERE ua.clerk_id = $2 AND ub.clerk_id = $3 ON CONFLICT (user_a_id, user_b_id) DO NOTHING"
	areFriendsSQL           = "SELECT EXISTS(SELECT 1 FROM friendships WHERE user_a_id = $1 AND user_b_id = $2)"
	deleteFriendshipSQL     = "DELETE FROM friendships WHERE user_a_id = $1 AND user_b_id = $2"
	existsByClerkIDSQL      = "SELECT EXISTS(SELECT 1 FROM users WHERE clerk_id = $1)"
	listFriendshipsSQL      = "SELECT u.clerk_id, u.display_name, u.profile_image_url, f.created_at FROM friendships f JOIN users u ON u.clerk_id = CASE WHEN f.user_a_id = $1 THEN f.user_b_id ELSE f.user_a_id END WHERE f.user_a_id = $1 OR f.user_b_id = $1 ORDER BY f.created_at DESC"
	listIncomingSQL         = "SELECT fr.id, u.clerk_id, u.display_name, u.profile_image_url, fr.created_at FROM friend_requests fr JOIN users u ON u.clerk_id = fr.requester_id WHERE fr.addressee_id = $1 ORDER BY fr.created_at DESC"
	listOutgoingSQL         = "SELECT fr.id, u.clerk_id, u.display_name, u.profile_image_url, fr.created_at FROM friend_requests fr JOIN users u ON u.clerk_id = fr.addressee_id WHERE fr.requester_id = $1 ORDER BY fr.created_at DESC"
	searchUsersSQL          = "SELECT " + searchSelectColumns + searchJoins + " WHERE u.clerk_id <> $1 AND (LOWER(COALESCE(u.display_name, '')) LIKE LOWER('%' || $2 || '%') OR LOWER(COALESCE(u.email, '')) LIKE LOWER('%' || $2 || '%')) ORDER BY CASE WHEN LOWER(COALESCE(u.display_name, '')) LIKE LOWER($2 || '%') THEN 0 ELSE 1 END, u.display_name LIMIT $3"
	recentUsersSQL          = "SELECT " + searchSelectColumns + searchJoins + " WHERE u.clerk_id <> $1 ORDER BY u.created_at DESC LIMIT $2"
	insertNotificationSQL   = "INSERT INTO notifications (id, clerk_id, type, actor_id, created_at) VALUES ($1,$2,$3,$4,$5)"
)

func strPtr(s string) *string { return &s }

func newMockRepo(t *testing.T) (*Repository, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating pgxmock pool: %v", err)
	}
	t.Cleanup(mock.Close)
	return NewRepository(mock), mock
}

// newMockService wires a Service against the same mock pool for both the
// friends and users repositories (both just need dbx.Querier) plus a
// NotifyClient pointed at a recording httptest server, so tests can both
// drive DB expectations and observe the fire-and-forget notify push.
func newMockService(t *testing.T) (*Service, pgxmock.PgxPoolIface, chan recordedNotify) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating pgxmock pool: %v", err)
	}
	t.Cleanup(mock.Close)

	// Unordered matching because a single Service call now fans out into more
	// than one goroutine that touches the database (notifyAsync's
	// notification insert and clearRequestNotification's delete run
	// concurrently), so there is no one true order for those to arrive in —
	// each expectation still has to be met, and still only by a call with
	// matching SQL and args.
	mock.MatchExpectationsInOrder(false)

	repo := NewRepository(mock)
	usersRepo := users.NewRepository(mock)
	notifier, ch := newNotifyRecorder(t)
	return NewService(repo, usersRepo, notifier), mock, ch
}

// expectSenderAndAddresseeExist covers SendRequest's users-exist precheck,
// which validates both participants rather than only the addressee — see its
// doc comment for why the caller's own row matters too.
func expectSenderAndAddresseeExist(mock pgxmock.PgxPoolIface, requesterID, addresseeID string) {
	for _, clerkID := range []string{requesterID, addresseeID} {
		mock.ExpectQuery(regexp.QuoteMeta(existsByClerkIDSQL)).
			WithArgs(clerkID).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	}
}

// TestOrderPairIsCanonicalRegardlessOfInputOrder is what lets friendships
// use a plain UNIQUE constraint instead of an OR-conditions check
// everywhere: the same two people must land on the same (userA, userB) no
// matter who's "a" in the caller's head.
func TestOrderPairIsCanonicalRegardlessOfInputOrder(t *testing.T) {
	a1, b1 := orderPair("user_a", "user_b")
	a2, b2 := orderPair("user_b", "user_a")
	if a1 != a2 || b1 != b2 {
		t.Fatalf("orderPair depends on argument order: (%s,%s) vs (%s,%s)", a1, b1, a2, b2)
	}
	if a1 != "user_a" || b1 != "user_b" {
		t.Fatalf("expected lexicographic order (user_a, user_b), got (%s, %s)", a1, b1)
	}
}

func TestRepositoryFindByIDFound(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	now := time.Now().UTC()

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}).
			AddRow(id, "user_a", "user_b", now))

	got, err := repo.FindByID(context.Background(), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != id || got.RequesterID != "user_a" || got.AddresseeID != "user_b" {
		t.Fatalf("unexpected request: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRepositoryFindByIDNotFound mirrors Optional.empty(): no matching row
// must come back as (nil, nil), never pgx.ErrNoRows leaking to the caller.
func TestRepositoryFindByIDNotFound(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}))

	got, err := repo.FindByID(context.Background(), id)
	if err != nil {
		t.Fatalf("expected no error for a missing row, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for a missing row, got %+v", got)
	}
}

func TestRepositoryFindByIDQueryError(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	boom := errors.New("connection reset")

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnError(boom)

	_, err := repo.FindByID(context.Background(), id)
	if !errors.Is(err, boom) {
		t.Fatalf("expected the underlying query error to propagate, got %v", err)
	}
}

func TestRepositoryFindBetweenFound(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	now := time.Now().UTC()

	mock.ExpectQuery(regexp.QuoteMeta(findBetweenSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}).
			AddRow(id, "user_b", "user_a", now))

	got, err := repo.FindBetween(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != id {
		t.Fatalf("unexpected request: %+v", got)
	}
}

func TestRepositoryInsertSetsFreshID(t *testing.T) {
	repo, mock := newMockRepo(t)
	fr := &FriendRequest{RequesterID: "user_a", AddresseeID: "user_b", CreatedAt: time.Now().UTC()}

	mock.ExpectExec(regexp.QuoteMeta(insertFriendRequestSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", "user_b", fr.CreatedAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.Insert(context.Background(), fr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr.ID == uuid.Nil {
		t.Fatal("expected Insert to stamp a fresh non-zero UUID onto fr.ID")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRepositoryInsertReportsMissingUsers: Insert is an INSERT...SELECT over
// users, so a missing users row for either participant inserts nothing while
// pgx still reports success. That silently produced a FriendRequest with an
// id no row has — sent as a real "you have a friend request" push to someone
// whose incoming list then stayed empty forever.
func TestRepositoryInsertReportsMissingUsers(t *testing.T) {
	repo, mock := newMockRepo(t)
	fr := &FriendRequest{RequesterID: "user_a", AddresseeID: "ghost", CreatedAt: time.Now().UTC()}

	mock.ExpectExec(regexp.QuoteMeta(insertFriendRequestSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", "ghost", fr.CreatedAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 0))

	if err := repo.Insert(context.Background(), fr); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound when no row was inserted", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRepositoryInsertFriendshipTreatsExistingFriendshipAsSuccess: zero rows
// affected is ambiguous here, because ON CONFLICT DO NOTHING also affects
// none when the pair is already friends — which is a legitimate success (it's
// what makes accepting idempotent under a race), so it must not be reported
// as a missing user.
func TestRepositoryInsertFriendshipTreatsExistingFriendshipAsSuccess(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta(insertFriendshipSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", "user_b", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectQuery(regexp.QuoteMeta(areFriendsSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	if err := repo.InsertFriendship(context.Background(), "user_a", "user_b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRepositoryInsertFriendshipReportsMissingUsers is the other reading of
// the same zero rows: no conflicting friendship exists either, so the
// INSERT...SELECT found no users rows to build from. Reporting that is what
// stops acceptRequest from going on to delete the request row — which is how
// an accepted request used to vanish leaving no friendship behind it.
func TestRepositoryInsertFriendshipReportsMissingUsers(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta(insertFriendshipSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", "user_b", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectQuery(regexp.QuoteMeta(areFriendsSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	if err := repo.InsertFriendship(context.Background(), "user_a", "user_b"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRepositoryFindBetweenToleratesDuplicateRows: nothing in the schema
// stops a pair from ending up with both an A→B and a B→A pending row (two
// sends racing past each other's FindBetween check). CollectExactlyOneRow
// turned that into a permanent ErrTooManyRows, so every later send and accept
// between those two people 500'd forever with no way out from the UI.
func TestRepositoryFindBetweenToleratesDuplicateRows(t *testing.T) {
	repo, mock := newMockRepo(t)
	oldest, newest := uuid.New(), uuid.New()
	now := time.Now().UTC()

	mock.ExpectQuery(regexp.QuoteMeta(findBetweenSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}).
			AddRow(oldest, "user_a", "user_b", now.Add(-time.Minute)).
			AddRow(newest, "user_b", "user_a", now))

	got, err := repo.FindBetween(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != oldest {
		t.Fatalf("expected the oldest of the duplicate rows, got %+v", got)
	}
}

func TestRepositoryDeleteBetween(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta(deleteRequestBetweenSQL)).
		WithArgs("user_a", "user_b").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := repo.DeleteBetween(context.Background(), "user_a", "user_b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryDeleteByID(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()

	mock.ExpectExec(regexp.QuoteMeta(deleteRequestByIDSQL)).
		WithArgs(id).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := repo.DeleteByID(context.Background(), id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestRepositoryInsertFriendshipOrdersPair passes the pair backwards
// (b, a) and expects the canonical (a, b) order in the args — InsertFriendship
// must run everything through orderPair itself, not trust the caller.
func TestRepositoryInsertFriendshipOrdersPair(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta(insertFriendshipSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", "user_b", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.InsertFriendship(context.Background(), "user_b", "user_a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryAreFriendsTrue(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta(areFriendsSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	ok, err := repo.AreFriends(context.Background(), "user_b", "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestRepositoryAreFriendsFalse(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta(areFriendsSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	ok, err := repo.AreFriends(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false")
	}
}

func TestRepositoryDeleteFriendshipOrdersPair(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectExec(regexp.QuoteMeta(deleteFriendshipSQL)).
		WithArgs("user_a", "user_b").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := repo.DeleteFriendship(context.Background(), "user_b", "user_a"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepositoryListFriendshipsPopulated(t *testing.T) {
	repo, mock := newMockRepo(t)
	now := time.Now().UTC()
	name := "Ada"

	mock.ExpectQuery(regexp.QuoteMeta(listFriendshipsSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "created_at"}).
			AddRow("user_b", &name, (*string)(nil), now))

	got, err := repo.ListFriendships(context.Background(), "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ClerkID != "user_b" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestRepositoryListFriendshipsEmptyIsNonNilSlice(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta(listFriendshipsSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "created_at"}))

	got, err := repo.ListFriendships(context.Background(), "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected a non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty slice, got %+v", got)
	}
}

func TestRepositoryListIncomingPopulated(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	now := time.Now().UTC()
	name := "Bob"

	mock.ExpectQuery(regexp.QuoteMeta(listIncomingSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"id", "clerk_id", "display_name", "profile_image_url", "created_at"}).
			AddRow(id, "user_b", &name, (*string)(nil), now))

	got, err := repo.ListIncoming(context.Background(), "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != id || got[0].ClerkID != "user_b" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestRepositoryListIncomingEmptyIsNonNilSlice(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta(listIncomingSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"id", "clerk_id", "display_name", "profile_image_url", "created_at"}))

	got, err := repo.ListIncoming(context.Background(), "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected a non-nil empty slice, got %+v", got)
	}
}

func TestRepositoryListOutgoingPopulated(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	now := time.Now().UTC()
	name := "Carol"

	mock.ExpectQuery(regexp.QuoteMeta(listOutgoingSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"id", "clerk_id", "display_name", "profile_image_url", "created_at"}).
			AddRow(id, "user_c", &name, (*string)(nil), now))

	got, err := repo.ListOutgoing(context.Background(), "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != id || got[0].ClerkID != "user_c" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestRepositoryListOutgoingEmptyIsNonNilSlice(t *testing.T) {
	repo, mock := newMockRepo(t)

	mock.ExpectQuery(regexp.QuoteMeta(listOutgoingSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"id", "clerk_id", "display_name", "profile_image_url", "created_at"}))

	got, err := repo.ListOutgoing(context.Background(), "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected a non-nil empty slice, got %+v", got)
	}
}

// TestRepositorySearchUsersClassifiesRelationships drives all four
// scanSearchResults branches through one query: an accepted friend, a
// stranger with no history, someone the caller already sent a request to,
// and someone who sent the caller a request.
func TestRepositorySearchUsersClassifiesRelationships(t *testing.T) {
	repo, mock := newMockRepo(t)
	callerID := "caller_1"

	rows := pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "is_friend", "requester_id"}).
		AddRow("friend_1", strPtr("Friend One"), (*string)(nil), true, (*string)(nil)).
		AddRow("stranger_1", strPtr("Stranger"), (*string)(nil), false, (*string)(nil)).
		AddRow("pending_sent_1", strPtr("Sent To"), (*string)(nil), false, strPtr(callerID)).
		AddRow("pending_received_1", strPtr("Received From"), (*string)(nil), false, strPtr("pending_received_1"))

	mock.ExpectQuery(regexp.QuoteMeta(searchUsersSQL)).
		WithArgs(callerID, "ada", 20).
		WillReturnRows(rows)

	got, err := repo.SearchUsers(context.Background(), callerID, "ada", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 results, got %d: %+v", len(got), got)
	}

	want := map[string]string{
		"friend_1":           RelationshipFriends,
		"stranger_1":         RelationshipNone,
		"pending_sent_1":     RelationshipPendingSent,
		"pending_received_1": RelationshipPendingReceived,
	}
	for _, r := range got {
		if r.Relationship != want[r.ClerkID] {
			t.Errorf("clerkId=%s: relationship = %s, want %s", r.ClerkID, r.Relationship, want[r.ClerkID])
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRepositoryRecentUsersOrdersBySignupRecency(t *testing.T) {
	repo, mock := newMockRepo(t)
	callerID := "caller_1"

	rows := pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "is_friend", "requester_id"}).
		AddRow("newcomer_1", strPtr("New Comer"), (*string)(nil), false, (*string)(nil)).
		AddRow("friend_2", strPtr("Old Friend"), (*string)(nil), true, (*string)(nil))

	mock.ExpectQuery(regexp.QuoteMeta(recentUsersSQL)).
		WithArgs(callerID, 20).
		WillReturnRows(rows)

	got, err := repo.RecentUsers(context.Background(), callerID, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Relationship != RelationshipNone || got[1].Relationship != RelationshipFriends {
		t.Fatalf("unexpected results: %+v", got)
	}
}

// TestServiceSendRequestSelfRequestFails checks the earliest-possible-return
// branch: requesterID == addresseeID must fail before any repository call,
// so no mock expectations are configured at all here.
func TestServiceSendRequestSelfRequestFails(t *testing.T) {
	svc, mock, _ := newMockService(t)

	fr, err := svc.SendRequest(context.Background(), "user_a", "user_a")
	if !errors.Is(err, ErrSelfRequest) {
		t.Fatalf("err = %v, want ErrSelfRequest", err)
	}
	if fr != nil {
		t.Fatalf("expected nil request, got %+v", fr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expected no repository calls for a self-request, but: %v", err)
	}
}

func TestServiceSendRequestUserNotFoundWhenAddresseeMissing(t *testing.T) {
	svc, mock, _ := newMockService(t)

	mock.ExpectQuery(regexp.QuoteMeta(existsByClerkIDSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(existsByClerkIDSQL)).
		WithArgs("ghost").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	fr, err := svc.SendRequest(context.Background(), "user_a", "ghost")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
	if fr != nil {
		t.Fatalf("expected nil request, got %+v", fr)
	}
}

// TestServiceSendRequestUserNotFoundWhenRequesterMissing is the half that
// used to slip through: only the addressee was checked, so a caller with no
// users row (a brand-new account whose Clerk webhook hasn't landed) got a
// 200 and a "request sent" UI for a request the INSERT...SELECT never
// actually created.
func TestServiceSendRequestUserNotFoundWhenRequesterMissing(t *testing.T) {
	svc, mock, _ := newMockService(t)

	mock.ExpectQuery(regexp.QuoteMeta(existsByClerkIDSQL)).
		WithArgs("ghost").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	fr, err := svc.SendRequest(context.Background(), "ghost", "user_b")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
	if fr != nil {
		t.Fatalf("expected nil outcome, got %+v", fr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestServiceSendRequestAlreadyFriendsReportsFriends: nothing changes, but
// the caller must be told it's a friendship and not a freshly-sent pending
// request — the old (nil, nil) return serialized to a bare `null` the
// frontend read as "sent, now pending".
func TestServiceSendRequestAlreadyFriendsReportsFriends(t *testing.T) {
	svc, mock, _ := newMockService(t)

	expectSenderAndAddresseeExist(mock, "user_a", "user_b")
	mock.ExpectQuery(regexp.QuoteMeta(areFriendsSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	out, err := svc.SendRequest(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil || out.Status != SendStatusFriends {
		t.Fatalf("expected a FRIENDS outcome when already friends, got %+v", out)
	}
	if out.Request != nil {
		t.Fatalf("expected no request on a FRIENDS outcome, got %+v", out.Request)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestServiceSendRequestExistingSameDirectionReturnsUnchanged(t *testing.T) {
	svc, mock, _ := newMockService(t)
	existingID := uuid.New()
	createdAt := time.Now().UTC()

	expectSenderAndAddresseeExist(mock, "user_a", "user_b")
	mock.ExpectQuery(regexp.QuoteMeta(areFriendsSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(regexp.QuoteMeta(findBetweenSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}).
			AddRow(existingID, "user_a", "user_b", createdAt))

	out, err := svc.SendRequest(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil || out.Status != SendStatusPending {
		t.Fatalf("expected a PENDING outcome, got %+v", out)
	}
	if out.Request == nil || out.Request.ID != existingID {
		t.Fatalf("expected the existing pending request back unchanged, got %+v", out.Request)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestServiceSendRequestReverseDirectionAutoAccepts covers SendRequest's
// "they already asked us" branch: it must accept on the spot (friendship
// inserted, request deleted) instead of creating a second, reverse-direction
// request row, and notify with the *accepted* event, not a new request event.
func TestServiceSendRequestReverseDirectionAutoAccepts(t *testing.T) {
	svc, mock, ch := newMockService(t)
	existingID := uuid.New()
	createdAt := time.Now().UTC()

	expectSenderAndAddresseeExist(mock, "user_a", "user_b")
	mock.ExpectQuery(regexp.QuoteMeta(areFriendsSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(regexp.QuoteMeta(findBetweenSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}).
			AddRow(existingID, "user_b", "user_a", createdAt))
	mock.ExpectExec(regexp.QuoteMeta(insertFriendshipSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", "user_b", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteRequestBetweenSQL)).
		WithArgs("user_b", "user_a").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteNotificationSQL)).
		WithArgs("user_a", NotificationFriendRequest, "user_b").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(regexp.QuoteMeta(insertNotificationSQL)).
		WithArgs(pgxmock.AnyArg(), "user_b", NotificationFriendRequestAccepted, "user_a", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	out, err := svc.SendRequest(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil || out.Status != SendStatusFriends {
		t.Fatalf("auto-accept must report FRIENDS, not a pending request, got %+v", out)
	}

	rec := waitForNotify(t, ch)
	if rec.path != "/internal/notify/user_b" {
		t.Fatalf("notified path = %s, want /internal/notify/user_b", rec.path)
	}
	var evt wsEvent
	if err := json.Unmarshal(rec.body, &evt); err != nil {
		t.Fatalf("decoding notify body: %v", err)
	}
	if evt.Type != EventFriendRequestAccepted {
		t.Fatalf("event type = %s, want %s", evt.Type, EventFriendRequestAccepted)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestServiceSendRequestHappyPathInsertsAndNotifies(t *testing.T) {
	svc, mock, ch := newMockService(t)

	expectSenderAndAddresseeExist(mock, "user_a", "user_b")
	mock.ExpectQuery(regexp.QuoteMeta(areFriendsSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(regexp.QuoteMeta(findBetweenSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}))
	mock.ExpectExec(regexp.QuoteMeta(insertFriendRequestSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", "user_b", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(regexp.QuoteMeta(insertNotificationSQL)).
		WithArgs(pgxmock.AnyArg(), "user_b", NotificationFriendRequest, "user_a", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	out, err := svc.SendRequest(context.Background(), "user_a", "user_b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil || out.Status != SendStatusPending {
		t.Fatalf("expected a PENDING outcome, got %+v", out)
	}
	if out.Request == nil || out.Request.RequesterID != "user_a" || out.Request.AddresseeID != "user_b" {
		t.Fatalf("unexpected request: %+v", out.Request)
	}

	rec := waitForNotify(t, ch)
	if rec.path != "/internal/notify/user_b" {
		t.Fatalf("notified path = %s, want /internal/notify/user_b", rec.path)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestServiceCancelOrDeclineNoopWhenRequestMissing(t *testing.T) {
	svc, mock, _ := newMockService(t)
	id := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}))

	if err := svc.CancelOrDecline(context.Background(), "user_a", id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestServiceCancelOrDeclineNoopWhenCallerNotParticipant(t *testing.T) {
	svc, mock, _ := newMockService(t)
	id := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}).
			AddRow(id, "user_a", "user_b", time.Now().UTC()))

	if err := svc.CancelOrDecline(context.Background(), "someone_else", id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestServiceCancelOrDeclineByRequesterPushesToAddresseeWithoutNotification
// is the asymmetric half of CancelOrDecline's contract: the requester
// canceling their own outgoing request must not write a notification row
// (they already know, they just did it) but must still push to the
// addressee, whose incoming list is now showing a request that no longer
// exists — and must retract the "you have a friend request" notification
// that request had already left in the addressee's bell.
func TestServiceCancelOrDeclineByRequesterPushesToAddresseeWithoutNotification(t *testing.T) {
	svc, mock, ch := newMockService(t)
	id := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}).
			AddRow(id, "user_a", "user_b", time.Now().UTC()))
	mock.ExpectExec(regexp.QuoteMeta(deleteRequestByIDSQL)).
		WithArgs(id).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteNotificationSQL)).
		WithArgs("user_b", NotificationFriendRequest, "user_a").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := svc.CancelOrDecline(context.Background(), "user_a", id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec := waitForNotify(t, ch)
	if rec.path != "/internal/notify/user_b" {
		t.Fatalf("notified path = %s, want /internal/notify/user_b", rec.path)
	}
	var evt wsEvent
	if err := json.Unmarshal(rec.body, &evt); err != nil {
		t.Fatalf("decoding notify body: %v", err)
	}
	if evt.Type != EventFriendRequestCanceled {
		t.Fatalf("event type = %s, want %s", evt.Type, EventFriendRequestCanceled)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestServiceCancelOrDeclineByAddresseeNotifiesRequester is the other half
// of the asymmetry: the addressee declining someone else's request must
// notify the original requester.
func TestServiceCancelOrDeclineByAddresseeNotifiesRequester(t *testing.T) {
	svc, mock, ch := newMockService(t)
	id := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}).
			AddRow(id, "user_a", "user_b", time.Now().UTC()))
	mock.ExpectExec(regexp.QuoteMeta(deleteRequestByIDSQL)).
		WithArgs(id).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteNotificationSQL)).
		WithArgs("user_b", NotificationFriendRequest, "user_a").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(regexp.QuoteMeta(insertNotificationSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", NotificationFriendRequestDeclined, "user_b", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := svc.CancelOrDecline(context.Background(), "user_b", id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec := waitForNotify(t, ch)
	if rec.path != "/internal/notify/user_a" {
		t.Fatalf("notified path = %s, want /internal/notify/user_a", rec.path)
	}
	var evt wsEvent
	if err := json.Unmarshal(rec.body, &evt); err != nil {
		t.Fatalf("decoding notify body: %v", err)
	}
	if evt.Type != EventFriendRequestDeclined {
		t.Fatalf("event type = %s, want %s", evt.Type, EventFriendRequestDeclined)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestServiceAcceptFailsWhenRequestMissing(t *testing.T) {
	svc, mock, _ := newMockService(t)
	id := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}))

	ok, err := svc.Accept(context.Background(), "user_b", id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false when the request doesn't exist")
	}
}

// TestServiceAcceptFailsWhenCallerIsRequester checks that only the
// addressee may accept — the requester calling Accept on their own
// outgoing request (a plausible bug: mixing up the two ids) must fail too.
func TestServiceAcceptFailsWhenCallerIsRequester(t *testing.T) {
	svc, mock, _ := newMockService(t)
	id := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}).
			AddRow(id, "user_a", "user_b", time.Now().UTC()))

	ok, err := svc.Accept(context.Background(), "user_a", id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("only the addressee may accept, but the requester's own Accept call succeeded")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestServiceAcceptSucceedsForAddressee(t *testing.T) {
	svc, mock, ch := newMockService(t)
	id := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}).
			AddRow(id, "user_a", "user_b", time.Now().UTC()))
	mock.ExpectExec(regexp.QuoteMeta(insertFriendshipSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", "user_b", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteRequestBetweenSQL)).
		WithArgs("user_a", "user_b").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(regexp.QuoteMeta(deleteNotificationSQL)).
		WithArgs("user_b", NotificationFriendRequest, "user_a").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(regexp.QuoteMeta(insertNotificationSQL)).
		WithArgs(pgxmock.AnyArg(), "user_a", NotificationFriendRequestAccepted, "user_b", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	ok, err := svc.Accept(context.Background(), "user_b", id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true when the addressee accepts")
	}

	waitForNotify(t, ch)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestServiceUnfriendPushesToTheRemovedFriend: the person being removed has
// to hear about it, or their session keeps listing an ex-friend — and keeps
// that ex-friend's friendship-gated profile unlocked, since the frontend
// derives "is a friend" from the same cached list — until a full reload.
func TestServiceUnfriendPushesToTheRemovedFriend(t *testing.T) {
	svc, mock, ch := newMockService(t)

	mock.ExpectExec(regexp.QuoteMeta(deleteFriendshipSQL)).
		WithArgs("user_a", "user_b").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	if err := svc.Unfriend(context.Background(), "user_a", "user_b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec := waitForNotify(t, ch)
	if rec.path != "/internal/notify/user_b" {
		t.Fatalf("notified path = %s, want /internal/notify/user_b", rec.path)
	}
	var evt wsEvent
	if err := json.Unmarshal(rec.body, &evt); err != nil {
		t.Fatalf("decoding notify body: %v", err)
	}
	if evt.Type != EventFriendRemoved {
		t.Fatalf("event type = %s, want %s", evt.Type, EventFriendRemoved)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestServiceListFriends(t *testing.T) {
	svc, mock, _ := newMockService(t)

	mock.ExpectQuery(regexp.QuoteMeta(listFriendshipsSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "created_at"}))

	got, err := svc.ListFriends(context.Background(), "user_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected an empty (non-nil) slice, got %+v", got)
	}
}

func TestServiceListIncoming(t *testing.T) {
	svc, mock, _ := newMockService(t)

	mock.ExpectQuery(regexp.QuoteMeta(listIncomingSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"id", "clerk_id", "display_name", "profile_image_url", "created_at"}))

	got, err := svc.ListIncoming(context.Background(), "user_a")
	if err != nil || got == nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestServiceListOutgoing(t *testing.T) {
	svc, mock, _ := newMockService(t)

	mock.ExpectQuery(regexp.QuoteMeta(listOutgoingSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"id", "clerk_id", "display_name", "profile_image_url", "created_at"}))

	got, err := svc.ListOutgoing(context.Background(), "user_a")
	if err != nil || got == nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestServiceSearchBlankQueryUsesRecentUsers(t *testing.T) {
	svc, mock, _ := newMockService(t)

	mock.ExpectQuery(regexp.QuoteMeta(recentUsersSQL)).
		WithArgs("user_a", 20).
		WillReturnRows(pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "is_friend", "requester_id"}))

	if _, err := svc.Search(context.Background(), "user_a", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestServiceSearchNonBlankQueryUsesSearchUsers(t *testing.T) {
	svc, mock, _ := newMockService(t)

	mock.ExpectQuery(regexp.QuoteMeta(searchUsersSQL)).
		WithArgs("user_a", "ada", 20).
		WillReturnRows(pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "is_friend", "requester_id"}))

	if _, err := svc.Search(context.Background(), "user_a", "ada"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
