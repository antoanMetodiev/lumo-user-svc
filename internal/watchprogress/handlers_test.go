package watchprogress

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/pashagolub/pgxmock/v4"

	"lumo-user-svc/internal/gatewayauth"
)

const testSecret = "test-internal-secret"

func newServer(t *testing.T) (*httptest.Server, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mock.Close)
	mux := http.NewServeMux()
	NewHandlers(NewRepository(mock)).Register(mux)
	srv := httptest.NewServer(gatewayauth.Middleware(testSecret, []string{"/watch-progress"}, nil)(mux))
	t.Cleanup(srv.Close)
	return srv, mock
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) int {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.Header.Set("X-Internal-Api-Key", testSecret)
	req.Header.Set("X-Clerk-User-Id", "user_1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestValidKey(t *testing.T) {
	for key, want := range map[string]bool{
		"movie:27205":   true,
		"tv:1396:2:5":   true,
		"tv:1396":       false,
		"movie:abc":     false,
		"movie:1; drop": false,
		"other:1":       false,
		"tv:1:2:3:4":    false,
	} {
		if ValidKey(key) != want {
			t.Errorf("ValidKey(%q) = %v", key, !want)
		}
	}
}

func TestSaveUpserts(t *testing.T) {
	srv, mock := newServer(t)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO watch_progress")).
		WithArgs("user_1", "movie:27205", 2830.5, pgxmock.AnyArg(), false, false, pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM watch_progress WHERE clerk_id = $1 AND progress_key IN")).
		WithArgs("user_1", maxEntries).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	if code := do(t, srv, http.MethodPut, "/watch-progress", `{"key":"movie:27205","seconds":2830.5,"duration":8888,"title":"Генезис"}`); code != http.StatusNoContent {
		t.Fatalf("status %d", code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestSaveRejectsBadInput(t *testing.T) {
	srv, _ := newServer(t)
	for _, body := range []string{
		`{"key":"movie:x","seconds":10}`,
		`{"key":"movie:1","seconds":-1}`,
		`{"key":"movie:1","seconds":10,"duration":99999999}`,
		`not json`,
	} {
		if code := do(t, srv, http.MethodPut, "/watch-progress", body); code != http.StatusBadRequest {
			t.Errorf("%s: status %d", body, code)
		}
	}
}

func TestDelete(t *testing.T) {
	srv, mock := newServer(t)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM watch_progress WHERE clerk_id = $1 AND progress_key = ANY($2)")).
		WithArgs("user_1", []string{"tv:1:1:1", "tv:1:1:2"}).
		WillReturnResult(pgxmock.NewResult("DELETE", 2))

	if code := do(t, srv, http.MethodDelete, "/watch-progress?key=tv:1:1:1&key=tv:1:1:2", ""); code != http.StatusNoContent {
		t.Fatalf("status %d", code)
	}
	if code := do(t, srv, http.MethodDelete, "/watch-progress?key=bad", ""); code != http.StatusBadRequest {
		t.Errorf("bad key: status %d", code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestNeedsAUser(t *testing.T) {
	srv, _ := newServer(t)
	res, err := http.Get(srv.URL + "/watch-progress")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Error("expected the gateway check to refuse a request without its key")
	}
}
