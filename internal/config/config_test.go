package config

import "testing"

// clearAllConfigEnvVars ensures each test starts from a clean slate — Load
// reads directly from the process environment, so a var left over from a
// previous test (or the developer's own shell) could otherwise leak in.
// t.Setenv already restores the previous value after the test, but a var
// that was unset before the test needs an explicit unset during it too,
// which t.Setenv can't express — so this clears everything Load reads,
// letting each test then t.Setenv only what it cares about.
func clearAllConfigEnvVars(t *testing.T) {
	t.Helper()
	vars := []string{
		"DATABASE_URL", "DATABASE_USERNAME", "DATABASE_PASSWORD", "PORT",
		"INTERNAL_SERVICE_SECRET", "CLERK_WEBHOOK_SECRET_LOCAL", "CLERK_WEBHOOK_SECRET",
		"GATEWAY_URL", "GRAFANA_OTLP_ENABLED", "GRAFANA_OTLP_ENDPOINT", "GRAFANA_OTLP_AUTH",
	}
	for _, v := range vars {
		t.Setenv(v, "")
	}
}

func TestLoadDefaultsWhenUnset(t *testing.T) {
	clearAllConfigEnvVars(t)

	cfg := Load()

	if cfg.Port != "8083" {
		t.Errorf("Port = %q, want default %q", cfg.Port, "8083")
	}
	if cfg.GatewayURL != "https://lumo-api-gateway.onrender.com" {
		t.Errorf("GatewayURL = %q, want the default gateway URL", cfg.GatewayURL)
	}
	if cfg.GrafanaEnabled {
		t.Error("GrafanaEnabled should default to false when GRAFANA_OTLP_ENABLED is unset")
	}
	if cfg.DatabaseURL != "" || cfg.DBUsername != "" || cfg.DBPassword != "" || cfg.InternalSecret != "" {
		t.Errorf("expected all no-default env vars to be empty, got %+v", cfg)
	}
	if cfg.ClerkWebhookSecret != "" {
		t.Errorf("ClerkWebhookSecret = %q, want empty when neither source var is set", cfg.ClerkWebhookSecret)
	}
}

func TestLoadReadsPlainEnvVarsThrough(t *testing.T) {
	clearAllConfigEnvVars(t)
	t.Setenv("DATABASE_URL", "jdbc:postgresql://host/db")
	t.Setenv("DATABASE_USERNAME", "avnadmin")
	t.Setenv("DATABASE_PASSWORD", "hunter2")
	t.Setenv("PORT", "9090")
	t.Setenv("INTERNAL_SERVICE_SECRET", "shhh")
	t.Setenv("GATEWAY_URL", "https://custom-gateway.example.com")

	cfg := Load()

	if cfg.DatabaseURL != "jdbc:postgresql://host/db" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.DBUsername != "avnadmin" {
		t.Errorf("DBUsername = %q", cfg.DBUsername)
	}
	if cfg.DBPassword != "hunter2" {
		t.Errorf("DBPassword = %q", cfg.DBPassword)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want the overridden value", cfg.Port)
	}
	if cfg.InternalSecret != "shhh" {
		t.Errorf("InternalSecret = %q", cfg.InternalSecret)
	}
	if cfg.GatewayURL != "https://custom-gateway.example.com" {
		t.Errorf("GatewayURL = %q, want the overridden value", cfg.GatewayURL)
	}
}

// TestLoadClerkWebhookSecretPrefersLocalOverride mirrors Spring's
// clerk.webhook-secret=${CLERK_WEBHOOK_SECRET_LOCAL:${CLERK_WEBHOOK_SECRET}}
// property placeholder chain — the local var (set only for dev against an
// ngrok tunnel) must win whenever both are present.
func TestLoadClerkWebhookSecretPrefersLocalOverride(t *testing.T) {
	clearAllConfigEnvVars(t)
	t.Setenv("CLERK_WEBHOOK_SECRET_LOCAL", "whsec_local")
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_prod")

	cfg := Load()

	if cfg.ClerkWebhookSecret != "whsec_local" {
		t.Errorf("ClerkWebhookSecret = %q, want the local override to win", cfg.ClerkWebhookSecret)
	}
}

func TestLoadClerkWebhookSecretFallsBackWhenLocalUnset(t *testing.T) {
	clearAllConfigEnvVars(t)
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_prod")

	cfg := Load()

	if cfg.ClerkWebhookSecret != "whsec_prod" {
		t.Errorf("ClerkWebhookSecret = %q, want the fallback to CLERK_WEBHOOK_SECRET", cfg.ClerkWebhookSecret)
	}
}

// TestLoadGrafanaEnabledOnlyOnExactStringTrue checks the strict
// os.Getenv(...) == "true" comparison — anything else (including "1",
// "TRUE", or "yes") must NOT enable it, since that's exactly what the
// production code compares against, no case-insensitive or truthy parsing.
func TestLoadGrafanaEnabledOnlyOnExactStringTrue(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"TRUE", false},
		{"1", false},
		{"yes", false},
		{"false", false},
		{"", false},
	}

	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			clearAllConfigEnvVars(t)
			t.Setenv("GRAFANA_OTLP_ENABLED", tc.value)

			cfg := Load()
			if cfg.GrafanaEnabled != tc.want {
				t.Errorf("GRAFANA_OTLP_ENABLED=%q -> GrafanaEnabled = %v, want %v", tc.value, cfg.GrafanaEnabled, tc.want)
			}
		})
	}
}

func TestLoadGrafanaEndpointAndAuthPassThrough(t *testing.T) {
	clearAllConfigEnvVars(t)
	t.Setenv("GRAFANA_OTLP_ENDPOINT", "otlp.grafana.example.com:443")
	t.Setenv("GRAFANA_OTLP_AUTH", "Basic dXNlcjpwYXNz")

	cfg := Load()

	if cfg.GrafanaEndpoint != "otlp.grafana.example.com:443" {
		t.Errorf("GrafanaEndpoint = %q", cfg.GrafanaEndpoint)
	}
	if cfg.GrafanaAuth != "Basic dXNlcjpwYXNz" {
		t.Errorf("GrafanaAuth = %q", cfg.GrafanaAuth)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("CONFIG_TEST_ENVOR_VAR", "")
	if got := envOr("CONFIG_TEST_ENVOR_VAR", "fallback"); got != "fallback" {
		t.Errorf("envOr with unset var = %q, want fallback", got)
	}

	t.Setenv("CONFIG_TEST_ENVOR_VAR", "set-value")
	if got := envOr("CONFIG_TEST_ENVOR_VAR", "fallback"); got != "set-value" {
		t.Errorf("envOr with set var = %q, want the set value", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty(); got != "" {
		t.Errorf("firstNonEmpty() with no args = %q, want empty", got)
	}
	if got := firstNonEmpty("", "", ""); got != "" {
		t.Errorf("firstNonEmpty of all-empty = %q, want empty", got)
	}
	if got := firstNonEmpty("", "second", "third"); got != "second" {
		t.Errorf("firstNonEmpty = %q, want the first non-empty value", got)
	}
	if got := firstNonEmpty("first", "second"); got != "first" {
		t.Errorf("firstNonEmpty = %q, want the very first value when it's already non-empty", got)
	}
}
