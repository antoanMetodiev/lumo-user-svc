package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareAllowedOriginGetsCORSHeaders(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	Middleware(next).ServeHTTP(rec, req)

	if !reached {
		t.Fatal("expected the wrapped handler to run for a non-OPTIONS request")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the echoed allowed origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want %q", got, "true")
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, DELETE, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type, X-Requested-With" {
		t.Errorf("Access-Control-Allow-Headers = %q", got)
	}
}

// TestMiddlewareDisallowedOriginGetsNoCORSHeaders checks that an Origin
// outside the allowlist is silently ignored — no headers set, but the
// request still proceeds (this service sits behind the gateway now; a
// browser calling it directly from an unlisted origin just won't be able to
// read the response due to the missing CORS headers, but the request itself
// isn't blocked here).
func TestMiddlewareDisallowedOriginGetsNoCORSHeaders(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	Middleware(next).ServeHTTP(rec, req)

	if !reached {
		t.Fatal("expected the wrapped handler to still run for a disallowed origin")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty for a disallowed origin", got)
	}
}

func TestMiddlewareNoOriginHeaderSkipsCORSHeadersEntirely(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	rec := httptest.NewRecorder()
	Middleware(next).ServeHTTP(rec, req)

	if !reached {
		t.Fatal("expected the wrapped handler to run when there's no Origin header at all")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty with no Origin header", got)
	}
}

// TestMiddlewareOptionsRequestShortCircuits verifies a CORS preflight never
// reaches the wrapped handler at all, regardless of origin.
func TestMiddlewareOptionsRequestShortCircuits(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })

	req := httptest.NewRequest(http.MethodOptions, "/whatever", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	Middleware(next).ServeHTTP(rec, req)

	if reached {
		t.Fatal("expected an OPTIONS preflight to short-circuit before the wrapped handler")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	// A preflight from an allowed origin should still carry the CORS
	// headers the browser needs to decide whether to send the real request.
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin on preflight = %q, want the echoed origin", got)
	}
}

func TestMiddlewareOptionsFromDisallowedOriginStillShortCircuits(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })

	req := httptest.NewRequest(http.MethodOptions, "/whatever", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	Middleware(next).ServeHTTP(rec, req)

	if reached {
		t.Fatal("expected an OPTIONS request to short-circuit even from a disallowed origin")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty for a disallowed origin", got)
	}
}
