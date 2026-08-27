package gatewayauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testSecret = "s3cr3t-internal-key"

func newTestMiddleware() func(http.Handler) http.Handler {
	return Middleware(testSecret, []string{"/user/me", "/watchlist"}, []string{"/internal"})
}

func nextThatRecordsClerkID(t *testing.T, got *string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = ClerkUserID(r)
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddlewarePublicPathBypassesAuthEntirely(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(http.StatusOK) })
	h := newTestMiddleware()(next)

	req := httptest.NewRequest(http.MethodGet, "/heartbeat", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("expected a path outside both userScopedPaths and internalPaths to reach the handler unauthenticated")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestMiddlewareUserScopedPathRequiresBothHeaders(t *testing.T) {
	var clerkID string
	h := newTestMiddleware()(nextThatRecordsClerkID(t, &clerkID))

	req := httptest.NewRequest(http.MethodGet, "/user/me", nil)
	req.Header.Set(InternalAPIKeyHdr, testSecret)
	req.Header.Set(ClerkUserIDHeader, "user_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if clerkID != "user_abc" {
		t.Fatalf("ClerkUserID(r) inside the handler = %q, want %q", clerkID, "user_abc")
	}
}

func TestMiddlewareUserScopedPathRejectsMissingInternalKey(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })
	h := newTestMiddleware()(next)

	req := httptest.NewRequest(http.MethodGet, "/user/me", nil)
	req.Header.Set(ClerkUserIDHeader, "user_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if reached {
		t.Fatal("handler must not run without a valid X-Internal-Api-Key")
	}
}

func TestMiddlewareUserScopedPathRejectsWrongInternalKey(t *testing.T) {
	h := newTestMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	req := httptest.NewRequest(http.MethodGet, "/user/me", nil)
	req.Header.Set(InternalAPIKeyHdr, "wrong-key-entirely")
	req.Header.Set(ClerkUserIDHeader, "user_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestMiddlewareUserScopedPathRejectsMissingOrBlankClerkUserID covers both a
// wholly absent header and one present but whitespace-only — the code
// explicitly strings.TrimSpace's it before checking, so a header of literal
// spaces must be rejected exactly like a missing one, not treated as a valid
// (if odd) identity.
func TestMiddlewareUserScopedPathRejectsMissingOrBlankClerkUserID(t *testing.T) {
	cases := []struct {
		name        string
		clerkHeader string
		setHeader   bool
	}{
		{"missing header entirely", "", false},
		{"whitespace-only header", "   ", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			h := newTestMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))

			req := httptest.NewRequest(http.MethodGet, "/user/me", nil)
			req.Header.Set(InternalAPIKeyHdr, testSecret)
			if tc.setHeader {
				req.Header.Set(ClerkUserIDHeader, tc.clerkHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if reached {
				t.Fatal("handler must not run without a non-blank X-Clerk-User-Id")
			}
		})
	}
}

// TestMiddlewareInternalPathNeedsOnlyTheSharedSecret checks the second kind
// of protected path: no X-Clerk-User-Id is required (or read into context)
// at all — the target user, if any, is a path variable the handler reads
// directly, per the package doc comment.
func TestMiddlewareInternalPathNeedsOnlyTheSharedSecret(t *testing.T) {
	var clerkID string
	// Sentinel so we can tell "never set" apart from "set to empty string".
	clerkID = "untouched"
	h := newTestMiddleware()(nextThatRecordsClerkID(t, &clerkID))

	req := httptest.NewRequest(http.MethodPost, "/internal/users/user_x/subscription", nil)
	req.Header.Set(InternalAPIKeyHdr, testSecret)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (internal path needs no X-Clerk-User-Id)", rec.Code)
	}
	if clerkID != "" {
		t.Fatalf("ClerkUserID(r) = %q, want \"\" — internal paths never populate it", clerkID)
	}
}

func TestMiddlewareInternalPathRejectsMissingInternalKey(t *testing.T) {
	h := newTestMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	req := httptest.NewRequest(http.MethodPost, "/internal/users/user_x/subscription", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestMiddlewarePathMatchingIsPrefixSlashBounded verifies matches() requires
// either an exact match or "/prefix/..." — NOT a bare string-prefix match —
// so a path like "/user/meltdown" must not be swept in by the "/user/me"
// entry.
func TestMiddlewarePathMatchingIsPrefixSlashBounded(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(http.StatusOK) })
	h := newTestMiddleware()(next)

	req := httptest.NewRequest(http.MethodGet, "/user/meltdown", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("expected /user/meltdown to NOT match the /user/me userScopedPaths entry, and thus pass through unauthenticated")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestMiddlewareExactMatchOfUserScopedPath(t *testing.T) {
	h := newTestMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	req := httptest.NewRequest(http.MethodGet, "/watchlist", nil)
	req.Header.Set(InternalAPIKeyHdr, testSecret)
	req.Header.Set(ClerkUserIDHeader, "user_abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an exact userScopedPaths match", rec.Code)
	}
}

func TestMiddlewareSubpathOfUserScopedPathIsProtected(t *testing.T) {
	h := newTestMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	req := httptest.NewRequest(http.MethodDelete, "/watchlist/some-record-id", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — /watchlist/{recordId} is a subpath of the protected /watchlist prefix", rec.Code)
	}
}

func TestClerkUserIDReturnsEmptyWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/user/me", nil)
	if got := ClerkUserID(req); got != "" {
		t.Fatalf("ClerkUserID on a request that never passed through Middleware = %q, want \"\"", got)
	}
}
