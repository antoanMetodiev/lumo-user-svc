// Package gatewayauth mirrors GatewayAuthenticationFilter: trusts headers
// set by lumo-api-gateway instead of verifying a Clerk session JWT itself —
// the gateway is the single place that talks to Clerk's JWKS. Two kinds of
// protected paths:
//
//   - userScopedPaths ("/user/me", "/watchlist", "/last-viewed"): a real end
//     user is asking about themselves. Requires X-Internal-Api-Key (proves
//     the caller is the gateway) AND X-Clerk-User-Id (the Clerk id the
//     gateway already verified) — the latter becomes the request's identity.
//   - internalPaths ("/internal"): a service-to-service call with no live
//     end user behind it (e.g. lumo-payments-svc telling us a Stripe webhook
//     changed someone's plan) — only X-Internal-Api-Key is required; the
//     target user is a path variable the handler reads directly.
//
// X-Internal-Api-Key is the only thing standing between "trust whatever
// these headers say" and full impersonation, since this service still has a
// public URL for now (no private network yet) — a stopgap until it does.
//
// Public endpoints (the Clerk webhook, heartbeat) skip this entirely.
package gatewayauth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

const (
	ClerkUserIDHeader = "X-Clerk-User-Id"
	InternalAPIKeyHdr = "X-Internal-Api-Key"
)

type ctxKey struct{}

var clerkUserIDKey = ctxKey{}

// ClerkUserID reads the verified caller id a handler behind Middleware can
// rely on once the request passed the user-scoped path check.
func ClerkUserID(r *http.Request) string {
	v, _ := r.Context().Value(clerkUserIDKey).(string)
	return v
}

// Middleware wraps a handler, enforcing the gateway trust check on
// userScopedPaths and internalPaths (exact match or "/prefix/..."), exactly
// like SecurityConfig's two path lists.
func Middleware(internalSecret string, userScopedPaths, internalPaths []string) func(http.Handler) http.Handler {
	secretBytes := []byte(internalSecret)

	matches := func(uri string, prefixes []string) bool {
		for _, p := range prefixes {
			if uri == p || strings.HasPrefix(uri, p+"/") {
				return true
			}
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			isUserScoped := matches(r.URL.Path, userScopedPaths)
			isInternal := matches(r.URL.Path, internalPaths)

			if !isUserScoped && !isInternal {
				next.ServeHTTP(w, r)
				return
			}

			provided := r.Header.Get(InternalAPIKeyHdr)
			if provided == "" || subtle.ConstantTimeCompare([]byte(provided), secretBytes) != 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			if isUserScoped {
				clerkUserID := r.Header.Get(ClerkUserIDHeader)
				if strings.TrimSpace(clerkUserID) == "" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}

				ctx := context.WithValue(r.Context(), clerkUserIDKey, clerkUserID)
				r = r.WithContext(ctx)
			}

			next.ServeHTTP(w, r)
		})
	}
}
