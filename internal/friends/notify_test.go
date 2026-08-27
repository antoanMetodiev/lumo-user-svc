package friends

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// recordedNotify captures one push NotifyClient made to the (fake) gateway,
// for tests to inspect after the fact.
type recordedNotify struct {
	path   string
	apiKey string
	body   []byte
}

// newNotifyRecorder starts an httptest server standing in for the gateway's
// /internal/notify/{clerkId} endpoint and returns a NotifyClient pointed at
// it, plus a channel receiving one recordedNotify per request. The channel
// is buffered so a handful of fire-and-forget pushes in one test never
// block the server goroutine on a slow-draining test.
func newNotifyRecorder(t *testing.T) (*NotifyClient, chan recordedNotify) {
	t.Helper()
	ch := make(chan recordedNotify, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ch <- recordedNotify{path: r.URL.Path, apiKey: r.Header.Get("X-Internal-Api-Key"), body: body}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return NewNotifyClient(srv.URL, "test-internal-secret"), ch
}

// waitForNotify blocks briefly for the next recorded push, failing the test
// if notifyAsync's goroutine hasn't fired within a second — long enough to
// never flake on a loaded machine, short enough to fail fast on a real bug.
// Prefer this channel-based wait over a blind sleep.
func waitForNotify(t *testing.T, ch <-chan recordedNotify) recordedNotify {
	t.Helper()
	select {
	case rec := <-ch:
		return rec
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for a notify push")
		return recordedNotify{}
	}
}

func TestNotifyClientNotifyPostsToGatewayWithHeaderAndBody(t *testing.T) {
	client, ch := newNotifyRecorder(t)
	fr := &FriendRequest{ID: uuid.New(), RequesterID: "user_a", AddresseeID: "user_b", CreatedAt: time.Now().UTC()}

	client.Notify(context.Background(), "user_b", EventFriendRequest, fr)

	rec := waitForNotify(t, ch)
	if rec.path != "/internal/notify/user_b" {
		t.Fatalf("path = %s, want /internal/notify/user_b", rec.path)
	}
	if rec.apiKey != "test-internal-secret" {
		t.Fatalf("X-Internal-Api-Key = %q, want test-internal-secret", rec.apiKey)
	}

	var got wsEvent
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got.Type != EventFriendRequest || got.Request == nil || got.Request.ID != fr.ID {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestNotifyClientNotifyRawMarshalsArbitraryPayload(t *testing.T) {
	client, ch := newNotifyRecorder(t)
	type customPayload struct {
		Foo string `json:"foo"`
	}

	client.NotifyRaw(context.Background(), "user_x", customPayload{Foo: "bar"})

	rec := waitForNotify(t, ch)
	if rec.path != "/internal/notify/user_x" {
		t.Fatalf("path = %s, want /internal/notify/user_x", rec.path)
	}
	if string(rec.body) != `{"foo":"bar"}` {
		t.Fatalf("body = %s, want {\"foo\":\"bar\"}", rec.body)
	}
}

// TestNotifyClientNotifyIsBestEffortOnUnreachableGateway checks Notify's
// documented contract: a push failure (nothing listening here) must be
// swallowed/logged, never returned to or panicked at the caller, since the
// underlying state change is already committed regardless.
func TestNotifyClientNotifyIsBestEffortOnUnreachableGateway(t *testing.T) {
	client := NewNotifyClient("http://127.0.0.1:1", "secret") // nothing listening on this port
	fr := &FriendRequest{ID: uuid.New(), RequesterID: "user_a", AddresseeID: "user_b", CreatedAt: time.Now().UTC()}

	client.Notify(context.Background(), "user_b", EventFriendRequest, fr) // must not panic
}

// TestNotifyClientNotifyIsBestEffortOnErrorStatus is the sibling case: the
// gateway responds, just not with success — also just logged.
func TestNotifyClientNotifyIsBestEffortOnErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	client := NewNotifyClient(srv.URL, "secret")
	client.Notify(context.Background(), "user_b", EventFriendRequest, &FriendRequest{}) // must not panic
}
