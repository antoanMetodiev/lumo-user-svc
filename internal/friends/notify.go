package friends

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// NotifyClient pushes a best-effort real-time event to lumo-api-gateway's
// in-memory WebSocket hub (see its internal/wsnotify) whenever a friend
// request is sent — same "tell the gateway, log-don't-propagate on failure"
// shape as clerkwebhook.PaymentsSync, the existing precedent for a service
// in this system calling back out to the gateway itself.
type NotifyClient struct {
	http           *http.Client
	gatewayURL     string
	internalSecret string
}

func NewNotifyClient(gatewayURL, internalSecret string) *NotifyClient {
	return &NotifyClient{
		http:           &http.Client{Timeout: 10 * time.Second},
		gatewayURL:     gatewayURL,
		internalSecret: internalSecret,
	}
}

// EventFriendRequest/EventFriendRequestAccepted are the WS message `type`
// values the frontend's useFriendRequestSocket listens for — kept as plain
// strings (not reusing the Notification* constants) since these describe
// the live push, not the persisted notification row, even though today
// every notification insert is paired with exactly one push of the same name.
const (
	EventFriendRequest         = "friend_request"
	EventFriendRequestAccepted = "friend_request_accepted"
	EventFriendRequestDeclined = "friend_request_declined"
)

type wsEvent struct {
	Type    string         `json:"type"`
	Request *FriendRequest `json:"request"`
}

// Notify is best-effort: whatever state change this event describes is
// already committed to the database regardless of whether targetClerkID is
// online to receive a live push right now — a failure here just means they
// see it on their next poll or page load instead of instantly, so it's
// logged, not returned as an error to the caller.
func (n *NotifyClient) Notify(ctx context.Context, targetClerkID, eventType string, fr *FriendRequest) {
	n.NotifyRaw(ctx, targetClerkID, wsEvent{Type: eventType, Request: fr})
}

// NotifyRaw is Notify generalized to any JSON-marshalable payload — for
// packages outside friends (e.g. watching) that want this same best-effort
// gateway push without shoehorning their event into wsEvent's
// FriendRequest-shaped body.
func (n *NotifyClient) NotifyRaw(ctx context.Context, targetClerkID string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("wsnotify: failed to marshal payload for clerkId=%s: %v", targetClerkID, err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.gatewayURL+"/internal/notify/"+targetClerkID, bytes.NewReader(body))
	if err != nil {
		log.Printf("wsnotify: failed to build request for clerkId=%s: %v", targetClerkID, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Api-Key", n.internalSecret)

	resp, err := n.http.Do(req)
	if err != nil {
		log.Printf("wsnotify: push to clerkId=%s failed: %v", targetClerkID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		log.Printf("wsnotify: push to clerkId=%s failed: status %d", targetClerkID, resp.StatusCode)
	}
}
