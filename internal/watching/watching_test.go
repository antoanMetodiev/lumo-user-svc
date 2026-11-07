package watching

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"

	"lumo-user-svc/internal/friends"
	"lumo-user-svc/internal/users"
)

func intPtr(v int) *int { return &v }

// --- intEq ---

func TestIntEqBothNil(t *testing.T) {
	if !intEq(nil, nil) {
		t.Fatal("expected intEq(nil, nil) to be true")
	}
}

func TestIntEqLeftNil(t *testing.T) {
	if intEq(nil, intPtr(5)) {
		t.Fatal("expected intEq(nil, &5) to be false")
	}
}

func TestIntEqRightNil(t *testing.T) {
	if intEq(intPtr(5), nil) {
		t.Fatal("expected intEq(&5, nil) to be false")
	}
}

func TestIntEqEqualValues(t *testing.T) {
	if !intEq(intPtr(5), intPtr(5)) {
		t.Fatal("expected intEq(&5, &5) to be true")
	}
}

func TestIntEqDifferentValues(t *testing.T) {
	if intEq(intPtr(5), intPtr(6)) {
		t.Fatal("expected intEq(&5, &6) to be false")
	}
}

// --- Status.same ---

func TestStatusSameIdentical(t *testing.T) {
	a := Status{TmdbID: "1", Type: "movie", Season: intPtr(1), Episode: intPtr(2)}
	b := Status{TmdbID: "1", Type: "movie", Season: intPtr(1), Episode: intPtr(2)}
	if !a.same(b) {
		t.Fatal("expected identical statuses to be same")
	}
}

func TestStatusSameDifferentTmdbID(t *testing.T) {
	a := Status{TmdbID: "1", Type: "movie"}
	b := Status{TmdbID: "2", Type: "movie"}
	if a.same(b) {
		t.Fatal("expected different tmdbId to not be same")
	}
}

func TestStatusSameDifferentType(t *testing.T) {
	a := Status{TmdbID: "1", Type: "movie"}
	b := Status{TmdbID: "1", Type: "series"}
	if a.same(b) {
		t.Fatal("expected different type to not be same")
	}
}

func TestStatusSameBothSeasonEpisodeNil(t *testing.T) {
	a := Status{TmdbID: "1", Type: "movie"}
	b := Status{TmdbID: "1", Type: "movie"}
	if !a.same(b) {
		t.Fatal("expected both-nil season/episode to be same")
	}
}

func TestStatusSameOneSeasonNil(t *testing.T) {
	a := Status{TmdbID: "1", Type: "series", Season: nil}
	b := Status{TmdbID: "1", Type: "series", Season: intPtr(1)}
	if a.same(b) {
		t.Fatal("expected one-nil-one-set season to not be same")
	}
}

func TestStatusSameDifferentSeasonValues(t *testing.T) {
	a := Status{TmdbID: "1", Type: "series", Season: intPtr(1)}
	b := Status{TmdbID: "1", Type: "series", Season: intPtr(2)}
	if a.same(b) {
		t.Fatal("expected different season values to not be same")
	}
}

func TestStatusSameSameSeasonEpisodeValues(t *testing.T) {
	a := Status{TmdbID: "1", Type: "series", Season: intPtr(1), Episode: intPtr(3)}
	b := Status{TmdbID: "1", Type: "series", Season: intPtr(1), Episode: intPtr(3)}
	if !a.same(b) {
		t.Fatal("expected same season/episode values to be same")
	}
}

// --- store ---

func TestStoreSetFirstTimeChanged(t *testing.T) {
	st := newStore()
	changed := st.set("user_1", Status{TmdbID: "1", Type: "movie"})
	if !changed {
		t.Fatal("expected changed=true on first-ever set")
	}
}

func TestStoreSetRepeatedIdenticalNotChanged(t *testing.T) {
	st := newStore()
	status := Status{TmdbID: "1", Type: "movie"}
	st.set("user_1", status)

	changed := st.set("user_1", status)
	if changed {
		t.Fatal("expected changed=false on an immediately-repeated identical status")
	}
}

func TestStoreSetDifferentTmdbIDChanged(t *testing.T) {
	st := newStore()
	st.set("user_1", Status{TmdbID: "1", Type: "movie"})

	changed := st.set("user_1", Status{TmdbID: "2", Type: "movie"})
	if !changed {
		t.Fatal("expected changed=true when tmdbId differs from the previous status")
	}
}

func TestStoreSetSameTmdbIDDifferentEpisodeChanged(t *testing.T) {
	st := newStore()
	st.set("user_1", Status{TmdbID: "1", Type: "series", Season: intPtr(1), Episode: intPtr(1)})

	changed := st.set("user_1", Status{TmdbID: "1", Type: "series", Season: intPtr(1), Episode: intPtr(2)})
	if !changed {
		t.Fatal("expected changed=true when season/episode differs on the same title")
	}
}

func TestStoreClearExisting(t *testing.T) {
	st := newStore()
	st.set("user_1", Status{TmdbID: "1", Type: "movie"})

	if existed := st.clear("user_1"); !existed {
		t.Fatal("expected existed=true when clearing a present status")
	}
}

func TestStoreClearAbsent(t *testing.T) {
	st := newStore()
	if existed := st.clear("ghost"); existed {
		t.Fatal("expected existed=false when clearing a clerkId with no status")
	}
}

func TestStoreClearTwiceInARow(t *testing.T) {
	st := newStore()
	st.set("user_1", Status{TmdbID: "1", Type: "movie"})
	st.clear("user_1")

	if existed := st.clear("user_1"); existed {
		t.Fatal("expected existed=false on a second consecutive clear")
	}
}

func TestStoreGetUnknownClerkID(t *testing.T) {
	st := newStore()
	if got := st.get("ghost"); got != nil {
		t.Fatalf("expected nil for an unknown clerkId, got %+v", got)
	}
}

func TestStoreGetFreshlySet(t *testing.T) {
	st := newStore()
	status := Status{TmdbID: "1", Type: "movie", Title: "A Movie"}
	st.set("user_1", status)

	got := st.get("user_1")
	if got == nil {
		t.Fatal("expected a status for a freshly-set clerkId")
	}
	if !got.same(status) || got.Title != status.Title {
		t.Fatalf("unexpected status: %+v", got)
	}
}

// TestStoreGetExpiredByTTL backdates the entry's updatedAt directly through
// the same-package internal map (legitimate whitebox access, not a hack)
// rather than sleeping out the real 90s ttl.
func TestStoreGetExpiredByTTL(t *testing.T) {
	st := newStore()
	st.set("user_1", Status{TmdbID: "1", Type: "movie"})

	st.mu.Lock()
	e := st.byID["user_1"]
	e.updatedAt = time.Now().Add(-ttl - time.Second)
	st.byID["user_1"] = e
	st.mu.Unlock()

	if got := st.get("user_1"); got != nil {
		t.Fatalf("expected nil for an entry older than ttl, got %+v", got)
	}
}

func TestStoreSweepExpiredRemovesOnlyStale(t *testing.T) {
	st := newStore()
	st.set("stale_1", Status{TmdbID: "1", Type: "movie"})
	st.set("stale_2", Status{TmdbID: "2", Type: "movie"})
	st.set("fresh", Status{TmdbID: "3", Type: "movie"})

	st.mu.Lock()
	for _, id := range []string{"stale_1", "stale_2"} {
		e := st.byID[id]
		e.updatedAt = time.Now().Add(-ttl - time.Second)
		st.byID[id] = e
	}
	st.mu.Unlock()

	expired := st.sweepExpired()

	if len(expired) != 2 {
		t.Fatalf("expected exactly 2 expired ids, got %v", expired)
	}
	gotSet := map[string]bool{expired[0]: true, expired[1]: true}
	if !gotSet["stale_1"] || !gotSet["stale_2"] {
		t.Fatalf("expected expired ids to be stale_1 and stale_2, got %v", expired)
	}

	if got := st.get("stale_1"); got != nil {
		t.Fatal("expected stale_1 to be removed from the map after sweeping")
	}
	if got := st.get("stale_2"); got != nil {
		t.Fatal("expected stale_2 to be removed from the map after sweeping")
	}
	if got := st.get("fresh"); got == nil {
		t.Fatal("expected fresh entry to survive sweeping untouched")
	}
}

// TestStoreConcurrentAccess is a basic locking-regression check, meant to be
// run with -race — not a stress test.
func TestStoreConcurrentAccess(t *testing.T) {
	st := newStore()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			st.set("user", Status{TmdbID: "1", Type: "movie"})
		}(i)
		go func(i int) {
			defer wg.Done()
			st.get("user")
		}(i)
		go func(i int) {
			defer wg.Done()
			st.clear("user")
		}(i)
	}
	wg.Wait()
}

// --- Service ---

func newMockRepos(t *testing.T) (*friends.Repository, pgxmock.PgxPoolIface, *users.Repository, pgxmock.PgxPoolIface) {
	t.Helper()
	friendsMock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating friends pgxmock pool: %v", err)
	}
	t.Cleanup(friendsMock.Close)

	usersMock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("creating users pgxmock pool: %v", err)
	}
	t.Cleanup(usersMock.Close)

	return friends.NewRepository(friendsMock), friendsMock, users.NewRepository(usersMock), usersMock
}

// notifyServer captures POSTed notify payloads for assertions and lets tests
// wait (bounded, not via a blind sleep) for the fire-and-forget goroutine.
type notifyServer struct {
	srv     *httptest.Server
	mu      sync.Mutex
	bodies  [][]byte
	arrived chan struct{}
}

func newNotifyServer(t *testing.T) *notifyServer {
	t.Helper()
	ns := &notifyServer{arrived: make(chan struct{}, 64)}
	ns.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ns.mu.Lock()
		ns.bodies = append(ns.bodies, body)
		ns.mu.Unlock()
		ns.arrived <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ns.srv.Close)
	return ns
}

// waitForRequest blocks briefly for one notify POST to arrive, failing the
// test if none does within the timeout.
func (ns *notifyServer) waitForRequest(t *testing.T, timeout time.Duration) []byte {
	t.Helper()
	select {
	case <-ns.arrived:
		ns.mu.Lock()
		defer ns.mu.Unlock()
		return ns.bodies[len(ns.bodies)-1]
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a notify request")
		return nil
	}
}

// expectNoRequest waits a bounded, short interval and fails if a notify POST
// arrives in that window.
func (ns *notifyServer) expectNoRequest(t *testing.T, wait time.Duration) {
	t.Helper()
	select {
	case <-ns.arrived:
		t.Fatal("expected no notify request, but one arrived")
	case <-time.After(wait):
	}
}

const shortWait = 500 * time.Millisecond

func expectFriendsListEmpty(mock pgxmock.PgxPoolIface, clerkID string) {
	rows := pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "created_at"})
	mock.ExpectQuery(regexp.QuoteMeta("FROM friendships f")).WithArgs(clerkID).WillReturnRows(rows)
}

// expectFriendsListOne is used wherever a test needs notifyFriends' fanout
// loop to actually fire a NotifyRaw call — an empty friend list means the
// loop never runs, so nothing gets POSTed no matter how "changed" the
// status was.
func expectFriendsListOne(mock pgxmock.PgxPoolIface, clerkID, friendID string) {
	rows := pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "created_at"}).
		AddRow(friendID, (*string)(nil), (*string)(nil), time.Now())
	mock.ExpectQuery(regexp.QuoteMeta("FROM friendships f")).WithArgs(clerkID).WillReturnRows(rows)
}

func expectUserNotFound(mock pgxmock.PgxPoolIface, clerkID string) {
	rows := pgxmock.NewRows([]string{"clerk_id", "email", "display_name", "profile_image_url", "subscription_type", "created_at", "updated_at"})
	mock.ExpectQuery(regexp.QuoteMeta("FROM users WHERE clerk_id = $1")).WithArgs(clerkID).WillReturnRows(rows)
}

func TestServiceSetFirstCallNotifies(t *testing.T) {
	friendsRepo, friendsMock, usersRepo, usersMock := newMockRepos(t)
	ns := newNotifyServer(t)
	notifier := friends.NewNotifyClient(ns.srv.URL, "secret")
	svc := NewService(friendsRepo, usersRepo, notifier)

	expectFriendsListOne(friendsMock, "user_1", "friend_1")
	expectUserNotFound(usersMock, "user_1")

	svc.Set(context.Background(), "user_1", Status{TmdbID: "1", Type: "movie", Title: "A Movie"})

	ns.waitForRequest(t, shortWait)
}

func TestServiceSetRepeatedIdenticalDoesNotNotify(t *testing.T) {
	friendsRepo, friendsMock, usersRepo, usersMock := newMockRepos(t)
	ns := newNotifyServer(t)
	notifier := friends.NewNotifyClient(ns.srv.URL, "secret")
	svc := NewService(friendsRepo, usersRepo, notifier)

	expectFriendsListOne(friendsMock, "user_1", "friend_1")
	expectUserNotFound(usersMock, "user_1")

	status := Status{TmdbID: "1", Type: "movie", Title: "A Movie"}
	svc.Set(context.Background(), "user_1", status)
	ns.waitForRequest(t, shortWait)

	// Second, identical Set must not trigger another notify at all.
	svc.Set(context.Background(), "user_1", status)
	ns.expectNoRequest(t, 200*time.Millisecond)
}

func TestServiceClearNeverSetDoesNotNotify(t *testing.T) {
	friendsRepo, _, usersRepo, _ := newMockRepos(t)
	ns := newNotifyServer(t)
	notifier := friends.NewNotifyClient(ns.srv.URL, "secret")
	svc := NewService(friendsRepo, usersRepo, notifier)

	svc.Clear(context.Background(), "never_set_user")

	ns.expectNoRequest(t, 200*time.Millisecond)
}

func TestServiceClearPreviouslySetNotifiesWithNilWatching(t *testing.T) {
	friendsRepo, friendsMock, usersRepo, usersMock := newMockRepos(t)
	ns := newNotifyServer(t)
	notifier := friends.NewNotifyClient(ns.srv.URL, "secret")
	svc := NewService(friendsRepo, usersRepo, notifier)

	expectFriendsListOne(friendsMock, "user_1", "friend_1")
	expectUserNotFound(usersMock, "user_1")
	svc.Set(context.Background(), "user_1", Status{TmdbID: "1", Type: "movie", Title: "A Movie"})
	ns.waitForRequest(t, shortWait)

	expectFriendsListOne(friendsMock, "user_1", "friend_1")
	expectUserNotFound(usersMock, "user_1")
	svc.Clear(context.Background(), "user_1")

	body := ns.waitForRequest(t, shortWait)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decoding notify payload: %v", err)
	}
	if v, ok := payload["watching"]; !ok || v != nil {
		t.Fatalf(`expected "watching" to be null in the payload, got %v`, payload["watching"])
	}
}

func TestServiceListFriendsWatchingFiltersToLiveOnly(t *testing.T) {
	friendsRepo, friendsMock, usersRepo, _ := newMockRepos(t)
	notifier := friends.NewNotifyClient("http://unused.invalid", "secret")
	svc := NewService(friendsRepo, usersRepo, notifier)

	rows := pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "created_at"}).
		AddRow("friend_fresh", (*string)(nil), (*string)(nil), time.Now()).
		AddRow("friend_expired", (*string)(nil), (*string)(nil), time.Now()).
		AddRow("friend_never_set", (*string)(nil), (*string)(nil), time.Now())
	friendsMock.ExpectQuery(regexp.QuoteMeta("FROM friendships f")).WithArgs("caller").WillReturnRows(rows)

	freshStatus := Status{TmdbID: "1", Type: "movie", Title: "Fresh"}
	svc.store.set("friend_fresh", freshStatus)

	svc.store.set("friend_expired", Status{TmdbID: "2", Type: "movie", Title: "Expired"})
	svc.store.mu.Lock()
	e := svc.store.byID["friend_expired"]
	e.updatedAt = time.Now().Add(-ttl - time.Second)
	svc.store.byID["friend_expired"] = e
	svc.store.mu.Unlock()

	out, err := svc.ListFriendsWatching(context.Background(), "caller")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected exactly 1 live friend, got %d: %+v", len(out), out)
	}
	if out[0].ClerkID != "friend_fresh" || !out[0].Watching.same(freshStatus) {
		t.Fatalf("unexpected result: %+v", out[0])
	}
}

func TestServiceListFriendsWatchingPropagatesError(t *testing.T) {
	friendsRepo, friendsMock, usersRepo, _ := newMockRepos(t)
	notifier := friends.NewNotifyClient("http://unused.invalid", "secret")
	svc := NewService(friendsRepo, usersRepo, notifier)

	boom := context.DeadlineExceeded
	friendsMock.ExpectQuery(regexp.QuoteMeta("FROM friendships f")).WithArgs("caller").WillReturnError(boom)

	_, err := svc.ListFriendsWatching(context.Background(), "caller")
	if err == nil {
		t.Fatal("expected ListFriendships error to propagate, got nil")
	}
}

func TestStatusSameIgnoresPositionAndPause(t *testing.T) {
	paused, playing := true, false
	at := func(seconds float64) *float64 { return &seconds }

	a := Status{TmdbID: "1", Type: "movie", Position: at(60), Paused: &playing}
	if !a.same(Status{TmdbID: "1", Type: "movie", Position: at(65), Paused: &paused}) {
		t.Fatal("expected a heartbeat that only moves the position or pauses to be same (no toast for friends)")
	}
}
