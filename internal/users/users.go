// Package users mirrors UserRepository + UserService + UserController +
// InternalUserController.
package users

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lumo-user-svc/internal/models"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const userColumns = "clerk_id, email, display_name, profile_image_url, subscription_type, created_at, updated_at"

func scanUser(row pgx.CollectableRow) (models.User, error) {
	var u models.User
	err := row.Scan(&u.ClerkID, &u.Email, &u.DisplayName, &u.ProfileImageURL, &u.SubscriptionType, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// FindByClerkID mirrors UserRepository.findById(clerkId). Returns (nil, nil)
// when no row matches, mirroring Optional.empty().
func (r *Repository) FindByClerkID(ctx context.Context, clerkID string) (*models.User, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+userColumns+" FROM users WHERE clerk_id = $1", clerkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	u, err := pgx.CollectExactlyOneRow(rows, scanUser)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// ExistsByClerkID mirrors UserRepository.existsById.
func (r *Repository) ExistsByClerkID(ctx context.Context, clerkID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE clerk_id = $1)", clerkID).Scan(&exists)
	return exists, err
}

// Upsert mirrors UserRepository.save() as used by ClerkWebhookService — an
// INSERT for a new clerkId, an UPDATE for an existing one, same as
// Hibernate's own transient-vs-managed distinction, done here as one
// statement instead of a separate find-then-insert-or-update.
func (r *Repository) Upsert(ctx context.Context, u *models.User) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (clerk_id, email, display_name, profile_image_url, subscription_type, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (clerk_id) DO UPDATE SET
			email = EXCLUDED.email,
			display_name = EXCLUDED.display_name,
			profile_image_url = EXCLUDED.profile_image_url,
			updated_at = EXCLUDED.updated_at`,
		u.ClerkID, u.Email, u.DisplayName, u.ProfileImageURL, u.SubscriptionType, u.CreatedAt, u.UpdatedAt)
	return err
}

// DeleteByClerkID mirrors UserRepository.deleteById.
func (r *Repository) DeleteByClerkID(ctx context.Context, clerkID string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM users WHERE clerk_id = $1", clerkID)
	return err
}

// UpdateSubscriptionType mirrors UserService.updateSubscriptionType — a
// single UPDATE...WHERE checking rows-affected instead of Java's separate
// find-then-save, same net effect (false when the row doesn't exist) in one
// round trip instead of two.
func (r *Repository) UpdateSubscriptionType(ctx context.Context, clerkID string, subscriptionType models.SubscriptionType) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		"UPDATE users SET subscription_type = $2, updated_at = $3 WHERE clerk_id = $1",
		clerkID, subscriptionType, time.Now().UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Service mirrors UserService.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) FindByClerkID(ctx context.Context, clerkID string) (*models.User, error) {
	return s.repo.FindByClerkID(ctx, clerkID)
}

// UpdateSubscriptionType is called via /internal/users/{clerkId}/subscription
// (lumo-payments-svc, through the gateway) after a Stripe webhook changes a
// plan. Returns false if the Clerk webhook hasn't synced this account into
// our DB yet — same race lumo-user-svc already tolerates elsewhere.
func (s *Service) UpdateSubscriptionType(ctx context.Context, clerkID string, subscriptionType models.SubscriptionType) (bool, error) {
	return s.repo.UpdateSubscriptionType(ctx, clerkID, subscriptionType)
}
