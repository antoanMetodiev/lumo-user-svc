package heartbeat

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterRootAndHeartbeatReturn200(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	for _, path := range []string{"/", "/heartbeat"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("GET %s = %d, want 200", path, rec.Code)
			}
		})
	}
}

// TestRegisterRootIsExactMatchOnly checks that "GET /{$}" (root exact-match)
// doesn't accidentally swallow an unrelated path — net/http's ServeMux
// treats "/{$}" as matching only "/" itself, not a wildcard prefix.
func TestRegisterRootIsExactMatchOnly(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/some-other-path", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /some-other-path = %d, want 404 (no route registered for it)", rec.Code)
	}
}

func TestRegisterOnlyAcceptsGET(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/heartbeat", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /heartbeat = %d, want 405", rec.Code)
	}
}
