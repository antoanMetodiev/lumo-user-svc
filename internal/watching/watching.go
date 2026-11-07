// Package watching tracks each signed-in user's ephemeral "currently
// watching" status in memory and pushes best-effort live updates to their
// friends through lumo-api-gateway's WebSocket hub (see friends.NotifyClient
// and lumo-api-gateway's internal/wsnotify) — same "who's online" question
// the friends system already answers, just "what are they doing right now"
// instead of "are they my friend".
//
// Deliberately not persisted, same reasoning as wsnotify's own connection
// map: a stale entry after a crashed tab, a redeployed instance, or a dead
// gateway should just disappear, not survive a restart. No Java original to
// mirror (new feature) — follows friends' Repository+Service+Handlers shape
// minus the repository, since there's no table here.
package watching

import (
	"context"
	"sync"
	"time"

	"lumo-user-svc/internal/friends"
	"lumo-user-svc/internal/users"
)

// ttl is how long a status survives without a heartbeat refresh before it's
// swept as stale. The frontend re-sends every ~30s (see my-app's
// use-watching-presence hook) purely to keep this alive, so ttl needs
// margin for one missed beat without flapping — but should still clear a
// genuinely dead tab (crash, lost network, closed laptop lid) within about
// a minute and a half of its last heartbeat.
const ttl = 90 * time.Second

// sweepInterval trades promptness for cheapness: friends of a user whose tab
// just died see the status disappear up to this long after ttl elapses, not
// instantly — fine for a presence indicator, not worth a per-entry timer.
const sweepInterval = 20 * time.Second

// EventFriendWatching is the WS message `type` value the frontend's
// useFriendRequestSocket listens for, alongside friends' own event types.
const EventFriendWatching = "friend_watching"

type Status struct {
	TmdbID  string `json:"tmdbId"`
	Type    string `json:"type"` // "movie" | "series"
	Title   string `json:"title"`
	Season  *int   `json:"season,omitempty"`
	Episode *int   `json:"episode,omitempty"`

	// Where in it, as the player reports: absent for the players that
	// report nothing. Refreshed by every heartbeat and never pushed: friends
	// poll /watching/friends every few seconds anyway, and every push shows
	// them a "now watching" toast.
	Position *float64 `json:"position,omitempty"`
	Duration *float64 `json:"duration,omitempty"`
	Paused   *bool    `json:"paused,omitempty"`
}

// same reports whether two statuses describe the same thing being
// watched — used to skip re-notifying friends on a heartbeat that isn't
// reporting anything new (only the season/episode within the same title
// changing, or the title itself changing, counts as new — not the position
// or a pause, see Status.Position).
func (s Status) same(other Status) bool {
	return s.TmdbID == other.TmdbID && s.Type == other.Type && intEq(s.Season, other.Season) && intEq(s.Episode, other.Episode)
}

func intEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

type entry struct {
	status    Status
	updatedAt time.Time
}

// store is the in-memory clerkId -> status map — see the package doc
// comment for why this is intentionally not a database table.
type store struct {
	mu   sync.Mutex
	byID map[string]entry
}

func newStore() *store {
	return &store{byID: map[string]entry{}}
}

// set records clerkID's status and reports whether it actually changed from
// what was there before (a heartbeat repeating the same title returns
// false) so the caller knows whether friends need re-notifying.
func (st *store) set(clerkID string, status Status) (changed bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	prev, existed := st.byID[clerkID]
	st.byID[clerkID] = entry{status: status, updatedAt: time.Now()}
	return !existed || !prev.status.same(status)
}

// clear removes clerkID's status and reports whether one was actually
// present — clearing an already-absent status is a no-op worth telling the
// caller about, so it doesn't fire a redundant "stopped watching" push.
func (st *store) clear(clerkID string) (existed bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	_, existed = st.byID[clerkID]
	delete(st.byID, clerkID)
	return existed
}

func (st *store) get(clerkID string) *Status {
	st.mu.Lock()
	defer st.mu.Unlock()

	e, ok := st.byID[clerkID]
	if !ok || time.Since(e.updatedAt) > ttl {
		return nil
	}
	status := e.status
	return &status
}

// sweepExpired removes and returns the clerkIds of every entry whose last
// heartbeat is older than ttl — the backstop for a tab that never got to
// send an explicit "stopped watching" at all.
func (st *store) sweepExpired() []string {
	st.mu.Lock()
	defer st.mu.Unlock()

	var expired []string
	for id, e := range st.byID {
		if time.Since(e.updatedAt) > ttl {
			expired = append(expired, id)
			delete(st.byID, id)
		}
	}
	return expired
}

type Service struct {
	store    *store
	friends  *friends.Repository
	users    *users.Repository
	notifier *friends.NotifyClient
}

func NewService(friendsRepo *friends.Repository, usersRepo *users.Repository, notifier *friends.NotifyClient) *Service {
	return &Service{store: newStore(), friends: friendsRepo, users: usersRepo, notifier: notifier}
}

// watchingPush carries the watcher's own display info alongside the status
// — pushed to friends who may not have this clerkId's FriendDTO loaded yet
// (or at all, on a fresh page) in their own frontend state, so the receiving
// UI never has to cross-reference a separately-fetched friends list just to
// render a name/avatar next to a live push.
type watchingPush struct {
	Type                 string  `json:"type"`
	ClerkID              string  `json:"clerkId"`
	ActorDisplayName     *string `json:"actorDisplayName"`
	ActorProfileImageURL *string `json:"actorProfileImageURL"`
	Watching             *Status `json:"watching"`
}

// notifyFriends is fire-and-forget, same shape as friends.Service's
// notifyAsync — called from an HTTP handler's request context (detached
// below since that context is canceled the instant the handler returns) or
// from the sweeper's long-lived one.
func (s *Service) notifyFriends(ctx context.Context, clerkID string, status *Status) {
	detached := context.WithoutCancel(ctx)
	go func() {
		friendList, err := s.friends.ListFriendships(detached, clerkID)
		if err != nil {
			return
		}

		var displayName, profileImageURL *string
		if actor, err := s.users.FindByClerkID(detached, clerkID); err == nil && actor != nil {
			displayName, profileImageURL = actor.DisplayName, actor.ProfileImageURL
		}

		payload := watchingPush{
			Type:                 EventFriendWatching,
			ClerkID:              clerkID,
			ActorDisplayName:     displayName,
			ActorProfileImageURL: profileImageURL,
			Watching:             status,
		}
		for _, f := range friendList {
			s.notifier.NotifyRaw(detached, f.ClerkID, payload)
		}
	}()
}

// Set records clerkID as currently watching status — called on both the
// initial "opened a title's player" and every heartbeat after. Only pushes
// to friends when this actually changes what they'd see; a heartbeat
// repeating the same title just refreshes the TTL silently.
func (s *Service) Set(ctx context.Context, clerkID string, status Status) {
	if s.store.set(clerkID, status) {
		s.notifyFriends(ctx, clerkID, &status)
	}
}

// Clear ends clerkID's watching status right away (tab closed, navigated
// away, backgrounded) instead of waiting out the TTL.
func (s *Service) Clear(ctx context.Context, clerkID string) {
	if s.store.clear(clerkID) {
		s.notifyFriends(ctx, clerkID, nil)
	}
}

type FriendWatchingDTO struct {
	ClerkID  string `json:"clerkId"`
	Watching Status `json:"watching"`
}

// ListFriendsWatching resolves which of clerkID's friends currently have a
// live (non-expired) status — the initial-load counterpart to the live push,
// for a friends list rendered before any push has happened yet (e.g. the
// friend was already watching something when this page loaded).
func (s *Service) ListFriendsWatching(ctx context.Context, clerkID string) ([]FriendWatchingDTO, error) {
	friendList, err := s.friends.ListFriendships(ctx, clerkID)
	if err != nil {
		return nil, err
	}

	out := []FriendWatchingDTO{}
	for _, f := range friendList {
		if status := s.store.get(f.ClerkID); status != nil {
			out = append(out, FriendWatchingDTO{ClerkID: f.ClerkID, Watching: *status})
		}
	}
	return out, nil
}

// StartSweeper runs until ctx is done, periodically clearing stale entries
// and telling their friends "stopped" — must be started exactly once (see
// cmd/server/main.go).
func (s *Service) StartSweeper(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, clerkID := range s.store.sweepExpired() {
				s.notifyFriends(ctx, clerkID, nil)
			}
		}
	}
}
