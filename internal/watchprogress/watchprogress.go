// Package watchprogress keeps where a signed-in viewer stopped in each movie
// and episode ("continue watching", resume, an episode's "Watched" mark), so
// it follows the account from device to device. A new feature with no Java
// original. Signed out, the website and the app keep it on the device
// instead; nothing from there is ever copied in here.
//
// One row per (clerk_id, progress_key). The key is the clients' own:
// "movie:<tmdbId>" or "tv:<tmdbId>:<season>:<episode>". The clients decide
// what counts as finished and when a position is worth saving; this only
// stores it.
package watchprogress

import (
	"context"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"lumo-user-svc/internal/dbx"
)

// maxEntries mirrors MAX_ENTRIES in the clients' on-device version.
const maxEntries = 300

// Schema is created by the service itself on start (there's no migration
// runner here), and is safe to run again.
const Schema = `
CREATE TABLE IF NOT EXISTS watch_progress (
	clerk_id     text             NOT NULL,
	progress_key text             NOT NULL,
	seconds      double precision NOT NULL,
	duration     double precision,
	finished     boolean          NOT NULL DEFAULT false,
	approximate  boolean          NOT NULL DEFAULT false,
	title        text,
	image        text,
	updated_at   timestamptz      NOT NULL DEFAULT now(),
	PRIMARY KEY (clerk_id, progress_key)
);
CREATE INDEX IF NOT EXISTS watch_progress_recent ON watch_progress (clerk_id, updated_at DESC);
`

var keyPattern = regexp.MustCompile(`^(movie:\d{1,12}|tv:\d{1,12}:\d{1,4}:\d{1,5})$`)

// ValidKey reports whether key has the clients' shape.
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// Entry is one saved position, as the clients send and read it.
type Entry struct {
	Key         string   `json:"key"`
	Seconds     float64  `json:"seconds"`
	Duration    *float64 `json:"duration"`
	Finished    bool     `json:"finished"`
	Approximate bool     `json:"approximate"`
	Title       *string  `json:"title,omitempty"`
	Image       *string  `json:"image,omitempty"`
	// UpdatedAt is milliseconds since the epoch, like the clients' Date.now().
	UpdatedAt int64 `json:"updatedAt"`
}

type Repository struct {
	pool dbx.Querier
}

func NewRepository(pool dbx.Querier) *Repository {
	return &Repository{pool: pool}
}

// EnsureSchema creates the table when it isn't there yet.
func (r *Repository) EnsureSchema(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, Schema)
	return err
}

func (r *Repository) List(ctx context.Context, clerkID string) ([]Entry, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT progress_key, seconds, duration, finished, approximate, title, image, updated_at
		   FROM watch_progress WHERE clerk_id = $1 ORDER BY updated_at DESC LIMIT $2`,
		clerkID, maxEntries)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Entry, error) {
		var e Entry
		var updated time.Time
		err := row.Scan(&e.Key, &e.Seconds, &e.Duration, &e.Finished, &e.Approximate, &e.Title, &e.Image, &updated)
		e.UpdatedAt = updated.UnixMilli()
		return e, err
	})
}

// Save writes one position. A save without a runtime, title or picture keeps
// the ones already stored: some players say their runtime only once.
func (r *Repository) Save(ctx context.Context, clerkID string, e Entry) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO watch_progress (clerk_id, progress_key, seconds, duration, finished, approximate, title, image, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		 ON CONFLICT (clerk_id, progress_key) DO UPDATE SET
		   seconds     = EXCLUDED.seconds,
		   duration    = COALESCE(EXCLUDED.duration, watch_progress.duration),
		   finished    = EXCLUDED.finished,
		   approximate = EXCLUDED.approximate,
		   title       = COALESCE(EXCLUDED.title, watch_progress.title),
		   image       = COALESCE(EXCLUDED.image, watch_progress.image),
		   updated_at  = now()`,
		clerkID, e.Key, e.Seconds, e.Duration, e.Finished, e.Approximate, e.Title, e.Image)
	if err != nil {
		return err
	}
	// Only the newest maxEntries are kept, same as on the device.
	_, err = r.pool.Exec(ctx,
		`DELETE FROM watch_progress WHERE clerk_id = $1 AND progress_key IN (
		   SELECT progress_key FROM watch_progress WHERE clerk_id = $1
		    ORDER BY updated_at DESC OFFSET $2)`,
		clerkID, maxEntries)
	return err
}

func (r *Repository) Delete(ctx context.Context, clerkID string, keys []string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM watch_progress WHERE clerk_id = $1 AND progress_key = ANY($2)`,
		clerkID, keys)
	return err
}
