// Package dbx wires up the Postgres connection pool. Max/min conn counts
// mirror the Java service's HikariCP settings (application.properties):
// max 5, min idle 1 — this DB tier's connection-slot budget is shared
// across every lumo-* service, so an idle Go instance shouldn't camp on 5
// open connections either.
//
// MaxConnIdleTime deliberately does NOT mirror HikariCP's 30s here (that
// was traced as a real, measured cause of sluggish search results, 2026-08:
// with only 1-2 users clicking around interactively — gaps of way more
// than 30s between requests — the pool's one MinConns connection kept
// getting recycled, so nearly every request paid a fresh TCP+TLS+Postgres
// handshake, measured at ~360ms against this Aiven instance, versus ~50ms
// for the same query on an already-warm connection). 5 minutes comfortably
// covers normal interactive gaps while still not camping on a connection
// during genuinely idle stretches.
package dbx

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// buildDSN turns Spring's three-part config (a possibly "jdbc:"-prefixed
// DATABASE_URL plus separate DATABASE_USERNAME/DATABASE_PASSWORD) into a
// single postgres:// DSN pgx understands. Render/Aiven's URL here carries no
// embedded credentials — those come from the separate env vars, same as the
// Java datasource config.
func buildDSN(rawURL, username, password string) (string, error) {
	trimmed := strings.TrimPrefix(rawURL, "jdbc:")

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}

	if username != "" {
		if password != "" {
			u.User = url.UserPassword(username, password)
		} else {
			u.User = url.User(username)
		}
	}

	// JDBC's own "ssl=true" query param (the driver's connection-property
	// name) isn't something pgx understands as a connection option — pgconn
	// forwards any query param it doesn't recognize straight through as a
	// literal Postgres startup-packet parameter, and "ssl" is a reserved
	// name there, which the server rejects outright (SQLSTATE 55P02). pgx's
	// own equivalent is "sslmode" — translate one to the other, defaulting
	// to "require" since Aiven's pg_hba.conf rejects unencrypted connections
	// entirely regardless.
	q := u.Query()
	if sslVal := q.Get("ssl"); sslVal != "" {
		q.Del("ssl")
		if q.Get("sslmode") == "" {
			if sslVal == "false" {
				q.Set("sslmode", "disable")
			} else {
				q.Set("sslmode", "require")
			}
		}
	}
	if q.Get("sslmode") == "" {
		q.Set("sslmode", "require")
	}
	u.RawQuery = q.Encode()

	return u.String(), nil
}

func Dial(ctx context.Context, rawURL, username, password string) (*pgxpool.Pool, error) {
	dsn, err := buildDSN(rawURL, username, password)
	if err != nil {
		return nil, err
	}

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	poolCfg.MaxConns = 5
	poolCfg.MinConns = 1
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.ConnConfig.ConnectTimeout = 10 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}

// StartKeepAlivePing mirrors DatabaseRequestPerDay: a trivial query at
// 00:00 and 12:00 Europe/Sofia every day, just to keep the DB from idling
// out on tiers that spin connections down. Runs until ctx is cancelled.
func StartKeepAlivePing(ctx context.Context, pool *pgxpool.Pool, logf func(format string, args ...any)) {
	loc, err := time.LoadLocation("Europe/Sofia")
	if err != nil {
		loc = time.UTC
	}

	go func() {
		for {
			next := nextMidnightOrNoon(time.Now().In(loc))
			timer := time.NewTimer(time.Until(next))

			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				var one int
				pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := pool.QueryRow(pingCtx, "SELECT 1").Scan(&one)
				cancel()
				if err != nil {
					logf("database keep-alive ping failed: %v", err)
				} else {
					logf("database keep-alive ping ok")
				}
			}
		}
	}()
}

func nextMidnightOrNoon(now time.Time) time.Time {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())

	for _, t := range []time.Time{midnight, noon, midnight.AddDate(0, 0, 1)} {
		if t.After(now) {
			return t
		}
	}
	return midnight.AddDate(0, 0, 1)
}
