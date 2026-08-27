package dbx

import (
	"net/url"
	"testing"
	"time"
)

func TestBuildDSNStripsJDBCPrefix(t *testing.T) {
	dsn, err := buildDSN("jdbc:postgresql://host.example.com:5432/mydb", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("resulting DSN did not parse as a URL: %v", err)
	}
	if u.Scheme != "postgresql" {
		t.Errorf("scheme = %q, want %q (jdbc: prefix should be stripped, not the scheme itself)", u.Scheme, "postgresql")
	}
	if u.Host != "host.example.com:5432" {
		t.Errorf("host = %q", u.Host)
	}
	if u.Path != "/mydb" {
		t.Errorf("path = %q", u.Path)
	}
}

func TestBuildDSNWithoutJDBCPrefixUnchanged(t *testing.T) {
	dsn, err := buildDSN("postgresql://host/db", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u, _ := url.Parse(dsn)
	if u.Host != "host" || u.Path != "/db" {
		t.Errorf("unexpected parsed DSN: host=%q path=%q", u.Host, u.Path)
	}
}

func TestBuildDSNDefaultsSSLModeToRequire(t *testing.T) {
	dsn, err := buildDSN("postgresql://host/db", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u, _ := url.Parse(dsn)
	if got := u.Query().Get("sslmode"); got != "require" {
		t.Errorf("sslmode = %q, want %q as the default (Aiven's pg_hba.conf rejects unencrypted connections)", got, "require")
	}
}

// TestBuildDSNSetsUserPasswordFromSeparateArgs mirrors Spring's three-part
// config: DATABASE_URL itself carries no embedded credentials on Render/
// Aiven — username/password always come from the separate env vars.
func TestBuildDSNSetsUserPasswordFromSeparateArgs(t *testing.T) {
	dsn, err := buildDSN("postgresql://host/db", "avnadmin", "hunter2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("resulting DSN did not parse: %v", err)
	}
	if u.User.Username() != "avnadmin" {
		t.Errorf("username = %q", u.User.Username())
	}
	pw, ok := u.User.Password()
	if !ok || pw != "hunter2" {
		t.Errorf("password = %q, ok=%v, want %q, true", pw, ok, "hunter2")
	}
}

// TestBuildDSNUsernameWithoutPassword covers the branch where a username is
// supplied but no password — url.User(username) (no colon in the userinfo
// at all), distinct from a username with an explicitly empty password.
func TestBuildDSNUsernameWithoutPassword(t *testing.T) {
	dsn, err := buildDSN("postgresql://host/db", "avnadmin", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("resulting DSN did not parse: %v", err)
	}
	if u.User.Username() != "avnadmin" {
		t.Errorf("username = %q", u.User.Username())
	}
	if _, ok := u.User.Password(); ok {
		t.Error("expected no password to be set when password arg is empty")
	}
}

func TestBuildDSNNoCredentialsLeavesUserinfoUnset(t *testing.T) {
	dsn, err := buildDSN("postgresql://host/db", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u, _ := url.Parse(dsn)
	if u.User != nil {
		t.Errorf("expected no userinfo in the DSN, got %v", u.User)
	}
}

// TestBuildDSNTranslatesJDBCSSLParam checks the ssl=true/false -> sslmode
// translation this function does specifically because pgconn rejects a raw
// "ssl" startup parameter outright (SQLSTATE 55P02).
func TestBuildDSNTranslatesJDBCSSLParam(t *testing.T) {
	cases := []struct {
		sslVal      string
		wantSSLMode string
	}{
		{"true", "require"},
		{"false", "disable"},
	}
	for _, tc := range cases {
		t.Run("ssl="+tc.sslVal, func(t *testing.T) {
			dsn, err := buildDSN("postgresql://host/db?ssl="+tc.sslVal, "", "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			u, _ := url.Parse(dsn)
			q := u.Query()
			if q.Get("ssl") != "" {
				t.Errorf("expected the literal ssl param to be removed, got %q", q.Get("ssl"))
			}
			if got := q.Get("sslmode"); got != tc.wantSSLMode {
				t.Errorf("sslmode = %q, want %q", got, tc.wantSSLMode)
			}
		})
	}
}

// TestBuildDSNExistingSSLModeWinsOverSSLParam checks that when a caller
// supplies both an "ssl" and an explicit "sslmode" query param, the ssl
// param is still stripped (pgconn would otherwise reject it) but the
// existing sslmode is left alone rather than being overwritten by the
// ssl->sslmode translation.
func TestBuildDSNExistingSSLModeWinsOverSSLParam(t *testing.T) {
	dsn, err := buildDSN("postgresql://host/db?ssl=false&sslmode=verify-full", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u, _ := url.Parse(dsn)
	q := u.Query()
	if q.Get("ssl") != "" {
		t.Error("expected the ssl param to be stripped regardless")
	}
	if got := q.Get("sslmode"); got != "verify-full" {
		t.Errorf("sslmode = %q, want the caller's own explicit value preserved", got)
	}
}

func TestBuildDSNInvalidURLReturnsError(t *testing.T) {
	// An unescaped, malformed percent sequence is one of the few inputs
	// net/url.Parse actually rejects outright.
	_, err := buildDSN("postgresql://host/%zz", "", "")
	if err == nil {
		t.Fatal("expected an error for a malformed URL, got nil")
	}
}

func TestNextMidnightOrNoonFromEarlyMorning(t *testing.T) {
	now := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	if got := nextMidnightOrNoon(now); !got.Equal(want) {
		t.Errorf("nextMidnightOrNoon(%v) = %v, want %v (noon today)", now, got, want)
	}
}

func TestNextMidnightOrNoonFromAfternoon(t *testing.T) {
	now := time.Date(2026, 8, 27, 13, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	if got := nextMidnightOrNoon(now); !got.Equal(want) {
		t.Errorf("nextMidnightOrNoon(%v) = %v, want %v (midnight tomorrow)", now, got, want)
	}
}

// TestNextMidnightOrNoonExactlyAtMidnight checks the boundary: time.Time.After
// is strict (not >=), so "now" being exactly midnight must NOT return that
// same midnight again (which would produce a zero-duration timer firing in a
// tight loop) — it must roll forward to noon.
func TestNextMidnightOrNoonExactlyAtMidnight(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	if got := nextMidnightOrNoon(now); !got.Equal(want) {
		t.Errorf("nextMidnightOrNoon(midnight) = %v, want noon the same day, not the same midnight again", got)
	}
}

func TestNextMidnightOrNoonExactlyAtNoon(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	if got := nextMidnightOrNoon(now); !got.Equal(want) {
		t.Errorf("nextMidnightOrNoon(noon) = %v, want midnight tomorrow, not the same noon again", got)
	}
}

func TestNextMidnightOrNoonLastMomentOfDay(t *testing.T) {
	now := time.Date(2026, 8, 27, 23, 59, 59, 999999999, time.UTC)
	want := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	if got := nextMidnightOrNoon(now); !got.Equal(want) {
		t.Errorf("nextMidnightOrNoon(23:59:59.999999999) = %v, want midnight tomorrow", got)
	}
}
