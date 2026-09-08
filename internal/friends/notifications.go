package friends

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	NotificationFriendRequest         = "FRIEND_REQUEST"
	NotificationFriendRequestAccepted = "FRIEND_REQUEST_ACCEPTED"
	NotificationFriendRequestDeclined = "FRIEND_REQUEST_DECLINED"
)

type NotificationDTO struct {
	ID              uuid.UUID `json:"id"`
	Type            string    `json:"type"`
	ActorID         string    `json:"actorId"`
	ActorName       *string   `json:"actorDisplayName"`
	ActorProfileURL *string   `json:"actorProfileImageURL"`
	CreatedAt       time.Time `json:"createdAt"`
}

func scanNotification(row pgx.CollectableRow) (NotificationDTO, error) {
	var n NotificationDTO
	err := row.Scan(&n.ID, &n.Type, &n.ActorID, &n.ActorName, &n.ActorProfileURL, &n.CreatedAt)
	return n, err
}

func (r *Repository) InsertNotification(ctx context.Context, clerkID, notifType, actorID string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO notifications (id, clerk_id, type, actor_id, created_at) VALUES ($1,$2,$3,$4,$5)`,
		uuid.New(), clerkID, notifType, actorID, time.Now().UTC())
	return err
}

func (r *Repository) CountNotifications(ctx context.Context, clerkID string) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM notifications WHERE clerk_id = $1", clerkID).Scan(&count)
	return count, err
}

// DeleteNotification removes the caller's notifications of one type from one
// actor — used to retract a friend-request prompt once that request is gone
// (see Service.clearRequestNotification). Type + actor rather than a row id
// because the caller resolving the request doesn't know, and shouldn't have
// to look up, which notification row announced it.
func (r *Repository) DeleteNotification(ctx context.Context, clerkID, notifType, actorID string) error {
	_, err := r.pool.Exec(ctx,
		"DELETE FROM notifications WHERE clerk_id = $1 AND type = $2 AND actor_id = $3",
		clerkID, notifType, actorID)
	return err
}

// ConsumeNotifications atomically deletes every one of the caller's
// notifications and returns what it deleted (a CTE around DELETE...RETURNING,
// joined against users for the actor's display info) — "checking" the
// notifications list *is* clearing it, per the user's explicit requirement,
// not a separate mark-as-read step.
//
// The users join must be a LEFT JOIN, and that isn't cosmetic: the DELETE
// runs to completion regardless of what the SELECT around it can join, so an
// inner join here didn't hide those notifications, it destroyed them — every
// row whose actor was missing from users (an account deleted since, or one
// whose Clerk webhook hasn't synced yet) was deleted and then filtered out of
// the very response that was supposed to be the user's one chance to see it.
// It also desynced the bell, whose count comes from CountNotifications and
// counts rows without joining anything: the badge said 3, opening the tray
// showed 2, and the third was gone for good.
func (r *Repository) ConsumeNotifications(ctx context.Context, clerkID string) ([]NotificationDTO, error) {
	rows, err := r.pool.Query(ctx, `
		WITH deleted AS (
			DELETE FROM notifications WHERE clerk_id = $1 RETURNING id, type, actor_id, created_at
		)
		SELECT d.id, d.type, d.actor_id, u.display_name, u.profile_image_url, d.created_at
		FROM deleted d
		LEFT JOIN users u ON u.clerk_id = d.actor_id
		ORDER BY d.created_at DESC`,
		clerkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, scanNotification)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []NotificationDTO{}
	}
	return out, nil
}

func (s *Service) NotificationCount(ctx context.Context, clerkID string) (int64, error) {
	return s.repo.CountNotifications(ctx, clerkID)
}

func (s *Service) ConsumeNotifications(ctx context.Context, clerkID string) ([]NotificationDTO, error) {
	return s.repo.ConsumeNotifications(ctx, clerkID)
}
