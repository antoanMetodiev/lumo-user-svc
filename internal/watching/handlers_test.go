package watching

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"

	"lumo-user-svc/internal/friends"
	"lumo-user-svc/internal/gatewayauth"
	"lumo-user-svc/internal/users"
)

const testInternalSecret = "test-internal-secret"

// newTestServer wraps the real watching handlers with the real
// gatewayauth.Middleware, the same way cmd/server/main.go does, so tests
// exercise the actual X-Internal-Api-Key / X-Clerk-User-Id contract instead
// of reaching into gatewayauth's unexported context key directly.
func newTestServer(t *testing.T, svc *Service) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	NewHandlers(svc).Register(mux)
	wrapped := gatewayauth.Middleware(testInternalSecret, []string{"/watching"}, nil)(mux)
	srv := httptest.NewServer(wrapped)
	t.Cleanup(srv.Close)
	return srv
}

func authedRequest(t *testing.T, method, url, clerkID string, body []byte) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, testInternalSecret)
	req.Header.Set(gatewayauth.ClerkUserIDHeader, clerkID)
	return req
}

func newHandlerTestService(t *testing.T, notifyURL string) (*Service, *friends.Repository, pgxmock.PgxPoolIface, *users.Repository, pgxmock.PgxPoolIface) {
	t.Helper()
	friendsRepo, friendsMock, usersRepo, usersMock := newMockRepos(t)
	notifier := friends.NewNotifyClient(notifyURL, "secret")
	svc := NewService(friendsRepo, usersRepo, notifier)
	return svc, friendsRepo, friendsMock, usersRepo, usersMock
}

// --- PUT /watching ---

func TestHandlerSetBlankTmdbID(t *testing.T) {
	svc, _, _, _, _ := newHandlerTestService(t, "http://unused.invalid")
	srv := newTestServer(t, svc)

	body, _ := json.Marshal(setBody{TmdbID: "  ", Type: "movie", Title: "A Movie"})
	req := authedRequest(t, http.MethodPut, srv.URL+"/watching", "user_1", body)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	msg, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(msg), "tmdbId and title must not be blank") {
		t.Fatalf("unexpected error body: %s", msg)
	}
}

func TestHandlerSetBlankTitle(t *testing.T) {
	svc, _, _, _, _ := newHandlerTestService(t, "http://unused.invalid")
	srv := newTestServer(t, svc)

	body, _ := json.Marshal(setBody{TmdbID: "1", Type: "movie", Title: "   "})
	req := authedRequest(t, http.MethodPut, srv.URL+"/watching", "user_1", body)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	msg, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(msg), "tmdbId and title must not be blank") {
		t.Fatalf("unexpected error body: %s", msg)
	}
}

func TestHandlerSetInvalidType(t *testing.T) {
	svc, _, _, _, _ := newHandlerTestService(t, "http://unused.invalid")
	srv := newTestServer(t, svc)

	body, _ := json.Marshal(setBody{TmdbID: "1", Type: "book", Title: "A Book"})
	req := authedRequest(t, http.MethodPut, srv.URL+"/watching", "user_1", body)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	msg, _ := io.ReadAll(resp.Body)
	want := `type must be "movie" or "series"`
	if !strings.Contains(string(msg), want) {
		t.Fatalf("expected error body to contain %q, got %s", want, msg)
	}
}

func TestHandlerSetEmptyType(t *testing.T) {
	svc, _, _, _, _ := newHandlerTestService(t, "http://unused.invalid")
	srv := newTestServer(t, svc)

	body, _ := json.Marshal(setBody{TmdbID: "1", Type: "", Title: "A Title"})
	req := authedRequest(t, http.MethodPut, srv.URL+"/watching", "user_1", body)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	msg, _ := io.ReadAll(resp.Body)
	want := `type must be "movie" or "series"`
	if !strings.Contains(string(msg), want) {
		t.Fatalf("expected error body to contain %q, got %s", want, msg)
	}
}

// expectOneFriendAndNoUser sets up the mock expectations notifyFriends'
// background goroutine needs to complete cleanly (one friend to fan out to,
// no matching actor user row) so the test can wait for the resulting POST
// before its t.Cleanup closes the mock pools out from under it. An empty
// friend list would make notifyFriends' fanout loop never fire at all, so
// there must be at least one friend for a notify request to actually arrive.
func expectOneFriendAndNoUser(friendsMock, usersMock pgxmock.PgxPoolIface, clerkID, friendID string) {
	friendsRows := pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "created_at"}).
		AddRow(friendID, (*string)(nil), (*string)(nil), time.Now())
	friendsMock.ExpectQuery(regexp.QuoteMeta("FROM friendships f")).WithArgs(clerkID).WillReturnRows(friendsRows)

	userRows := pgxmock.NewRows([]string{"clerk_id", "email", "display_name", "profile_image_url", "subscription_type", "created_at", "updated_at"})
	usersMock.ExpectQuery(regexp.QuoteMeta("FROM users WHERE clerk_id = $1")).WithArgs(clerkID).WillReturnRows(userRows)
}

func TestHandlerSetValidBodyWithSeasonEpisode(t *testing.T) {
	ns := newNotifyServer(t)

	svc, _, friendsMock, _, usersMock := newHandlerTestService(t, ns.srv.URL)
	srv := newTestServer(t, svc)
	expectOneFriendAndNoUser(friendsMock, usersMock, "user_1", "friend_1")

	season, episode := 2, 5
	body, _ := json.Marshal(setBody{TmdbID: "42", Type: "series", Title: "A Series", Season: &season, Episode: &episode})
	req := authedRequest(t, http.MethodPut, srv.URL+"/watching", "user_1", body)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	got := svc.store.get("user_1")
	if got == nil {
		t.Fatal("expected a stored status after a valid Set")
	}
	if got.Season == nil || *got.Season != season || got.Episode == nil || *got.Episode != episode {
		t.Fatalf("expected season=%d episode=%d to round-trip, got %+v", season, episode, got)
	}

	ns.waitForRequest(t, shortWait) // drain the async notify before cleanup closes the mocks
}

func TestHandlerSetValidBodyWithoutSeasonEpisode(t *testing.T) {
	ns := newNotifyServer(t)

	svc, _, friendsMock, _, usersMock := newHandlerTestService(t, ns.srv.URL)
	srv := newTestServer(t, svc)
	expectOneFriendAndNoUser(friendsMock, usersMock, "user_1", "friend_1")

	body, _ := json.Marshal(setBody{TmdbID: "42", Type: "movie", Title: "A Movie"})
	req := authedRequest(t, http.MethodPut, srv.URL+"/watching", "user_1", body)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	got := svc.store.get("user_1")
	if got == nil {
		t.Fatal("expected a stored status after a valid Set")
	}
	if got.Season != nil || got.Episode != nil {
		t.Fatalf("expected season/episode to stay nil when omitted from the JSON body, got %+v", got)
	}

	ns.waitForRequest(t, shortWait)
}

// --- DELETE /watching ---

func TestHandlerClearAlwaysReturns204(t *testing.T) {
	svc, _, _, _, _ := newHandlerTestService(t, "http://unused.invalid")
	srv := newTestServer(t, svc)

	// clerkID never had a status set, so Clear's internal store.clear finds
	// nothing and no notify goroutine is spawned — keeps this test free of
	// any async wait while still checking the handler's unconditional 204.
	req := authedRequest(t, http.MethodDelete, srv.URL+"/watching", "user_never_set", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

// --- GET /watching/friends ---

func TestHandlerListFriendsWatching200(t *testing.T) {
	svc, _, friendsMock, _, _ := newHandlerTestService(t, "http://unused.invalid")
	srv := newTestServer(t, svc)

	rows := pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "created_at"}).
		AddRow("friend_live", (*string)(nil), (*string)(nil), time.Now()).
		AddRow("friend_offline", (*string)(nil), (*string)(nil), time.Now())
	friendsMock.ExpectQuery(regexp.QuoteMeta("FROM friendships f")).WithArgs("caller").WillReturnRows(rows)

	liveStatus := Status{TmdbID: "7", Type: "movie", Title: "Live One"}
	svc.store.set("friend_live", liveStatus)

	req := authedRequest(t, http.MethodGet, srv.URL+"/watching/friends", "caller", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out []FriendWatchingDTO
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(out) != 1 || out[0].ClerkID != "friend_live" || !out[0].Watching.same(liveStatus) {
		t.Fatalf("unexpected response body: %+v", out)
	}
}

func TestHandlerListFriendsWatchingErrorReturns500(t *testing.T) {
	svc, _, friendsMock, _, _ := newHandlerTestService(t, "http://unused.invalid")
	srv := newTestServer(t, svc)

	friendsMock.ExpectQuery(regexp.QuoteMeta("FROM friendships f")).WithArgs("caller").WillReturnError(context.DeadlineExceeded)

	req := authedRequest(t, http.MethodGet, srv.URL+"/watching/friends", "caller", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestHandlerSetKeepsOnlySanePositions(t *testing.T) {
	ns := newNotifyServer(t)

	svc, _, friendsMock, _, usersMock := newHandlerTestService(t, ns.srv.URL)
	srv := newTestServer(t, svc)
	expectOneFriendAndNoUser(friendsMock, usersMock, "user_1", "friend_1")

	body := []byte(`{"tmdbId":"42","type":"movie","title":"A Movie","position":2820.4,"duration":-5,"paused":true}`)
	req := authedRequest(t, http.MethodPut, srv.URL+"/watching", "user_1", body)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	got := svc.store.get("user_1")
	if got == nil || got.Position == nil || *got.Position != 2820 {
		t.Fatalf("expected the position rounded to 2820, got %+v", got)
	}
	if got.Duration != nil {
		t.Fatalf("expected a negative duration to be dropped, got %v", *got.Duration)
	}
	if got.Paused == nil || !*got.Paused {
		t.Fatal("expected paused to round-trip")
	}

	ns.waitForRequest(t, shortWait)
}
