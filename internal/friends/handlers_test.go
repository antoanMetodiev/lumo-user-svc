package friends

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"

	"lumo-user-svc/internal/gatewayauth"
)

const testInternalSecret = "test-internal-secret"

// newTestServer wires Handlers behind the real gatewayauth.Middleware —
// exactly like cmd/server/main.go does with userScopedPaths including
// "/friends" — instead of faking the ClerkUserID context value directly
// (impossible from this package anyway, since gatewayauth's context key is
// unexported). This exercises the real header-trust + identity-injection
// path end to end.
func newTestServer(t *testing.T, svc *Service) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	NewHandlers(svc).Register(mux)
	wrapped := gatewayauth.Middleware(testInternalSecret, []string{"/friends"}, nil)(mux)
	srv := httptest.NewServer(wrapped)
	t.Cleanup(srv.Close)
	return srv
}

func newAuthedRequest(t *testing.T, method, url, clerkID string, body []byte) *http.Request {
	t.Helper()
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, url, bytes.NewReader(body))
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set(gatewayauth.InternalAPIKeyHdr, testInternalSecret)
	req.Header.Set(gatewayauth.ClerkUserIDHeader, clerkID)
	return req
}

func TestHandlerSendRequestBlankAddresseeIDReturns400(t *testing.T) {
	svc, _, _ := newMockService(t)
	srv := newTestServer(t, svc)

	req := newAuthedRequest(t, http.MethodPost, srv.URL+"/friends/requests", "user_a", []byte(`{"addresseeId":"   "}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandlerSendRequestSelfRequestReturns400WithMessage(t *testing.T) {
	svc, _, _ := newMockService(t)
	srv := newTestServer(t, svc)

	req := newAuthedRequest(t, http.MethodPost, srv.URL+"/friends/requests", "user_a", []byte(`{"addresseeId":"user_a"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	want := "addresseeId: cannot send a friend request to yourself\n"
	if string(body) != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestHandlerSendRequestUserNotFoundReturns400WithMessage(t *testing.T) {
	svc, mock, _ := newMockService(t)
	srv := newTestServer(t, svc)

	mock.ExpectQuery(regexp.QuoteMeta(existsByClerkIDSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(existsByClerkIDSQL)).
		WithArgs("ghost").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	req := newAuthedRequest(t, http.MethodPost, srv.URL+"/friends/requests", "user_a", []byte(`{"addresseeId":"ghost"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	want := "addresseeId: user not found\n"
	if string(body) != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestHandlerCancelOrDeclineMalformedIDReturns400(t *testing.T) {
	svc, _, _ := newMockService(t)
	srv := newTestServer(t, svc)

	req := newAuthedRequest(t, http.MethodDelete, srv.URL+"/friends/requests/not-a-uuid", "user_a", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandlerAcceptMalformedIDReturns400(t *testing.T) {
	svc, _, _ := newMockService(t)
	srv := newTestServer(t, svc)

	req := newAuthedRequest(t, http.MethodPost, srv.URL+"/friends/requests/not-a-uuid/accept", "user_a", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandlerAcceptNotFoundReturns404(t *testing.T) {
	svc, mock, _ := newMockService(t)
	srv := newTestServer(t, svc)
	id := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta(findByIDSQL)).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"id", "requester_id", "addressee_id", "created_at"}))

	req := newAuthedRequest(t, http.MethodPost, srv.URL+"/friends/requests/"+id.String()+"/accept", "user_b", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandlerListFriendsHappyPathReturns200JSON(t *testing.T) {
	svc, mock, _ := newMockService(t)
	srv := newTestServer(t, svc)
	now := time.Now().UTC()
	name := "Ada"

	mock.ExpectQuery(regexp.QuoteMeta(listFriendshipsSQL)).
		WithArgs("user_a").
		WillReturnRows(pgxmock.NewRows([]string{"clerk_id", "display_name", "profile_image_url", "created_at"}).
			AddRow("user_b", &name, (*string)(nil), now))

	req := newAuthedRequest(t, http.MethodGet, srv.URL+"/friends", "user_a", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []FriendDTO
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].ClerkID != "user_b" {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestHandlerSendRequestHappyPathReturns200JSON(t *testing.T) {
	svc, mock, ch := newMockService(t)
	srv := newTestServer(t, svc)

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

	req := newAuthedRequest(t, http.MethodPost, srv.URL+"/friends/requests", "user_a", []byte(`{"addresseeId":"user_b"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got SendOutcome
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Status != SendStatusPending {
		t.Fatalf("status = %q, want %q", got.Status, SendStatusPending)
	}
	if got.Request == nil || got.Request.RequesterID != "user_a" || got.Request.AddresseeID != "user_b" {
		t.Fatalf("unexpected body: %+v", got.Request)
	}

	waitForNotify(t, ch)
}

// TestHandlerSendRequestAlreadyFriendsReturnsFriendsStatus pins the half of
// the contract the frontend reads: the response body has to say the two are
// already friends. It used to be a bare `null` under a 200, which the UI took
// for a freshly-sent request and drew a pending state over an existing
// friendship.
func TestHandlerSendRequestAlreadyFriendsReturnsFriendsStatus(t *testing.T) {
	svc, mock, _ := newMockService(t)
	srv := newTestServer(t, svc)

	expectSenderAndAddresseeExist(mock, "user_a", "user_b")
	mock.ExpectQuery(regexp.QuoteMeta(areFriendsSQL)).
		WithArgs("user_a", "user_b").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	req := newAuthedRequest(t, http.MethodPost, srv.URL+"/friends/requests", "user_a", []byte(`{"addresseeId":"user_b"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got SendOutcome
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Status != SendStatusFriends {
		t.Fatalf("status = %q, want %q", got.Status, SendStatusFriends)
	}
}
