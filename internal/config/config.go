// Package config reads the exact same environment variables the Java service
// used (application.properties), so this drop-in replacement needs zero
// changes to whatever's already set on Render.
package config

import "os"

type Config struct {
	DatabaseURL    string // raw DATABASE_URL (may carry a "jdbc:" prefix), see dbx.Dial
	DBUsername     string
	DBPassword     string
	Port           string
	InternalSecret string // internal.service-secret — shared with lumo-api-gateway/lumo-payments-svc

	// clerk.webhook-secret=${CLERK_WEBHOOK_SECRET_LOCAL:${CLERK_WEBHOOK_SECRET}}
	// — the local var (only ever set for local dev against an ngrok tunnel)
	// takes precedence when set, otherwise falls through to the real one.
	ClerkWebhookSecret string

	// gateway.url — where lumo-api-gateway lives, used to tell
	// lumo-payments-svc (via the gateway) about deleted accounts.
	GatewayURL string

	GrafanaEnabled  bool
	GrafanaEndpoint string
	GrafanaAuth     string
}

func Load() Config {
	return Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		DBUsername:     os.Getenv("DATABASE_USERNAME"),
		DBPassword:     os.Getenv("DATABASE_PASSWORD"),
		Port:           envOr("PORT", "8083"), // Java's server.port=8083
		InternalSecret: os.Getenv("INTERNAL_SERVICE_SECRET"),

		ClerkWebhookSecret: firstNonEmpty(os.Getenv("CLERK_WEBHOOK_SECRET_LOCAL"), os.Getenv("CLERK_WEBHOOK_SECRET")),

		GatewayURL: envOr("GATEWAY_URL", "https://lumo-api-gateway.onrender.com"),

		GrafanaEnabled:  os.Getenv("GRAFANA_OTLP_ENABLED") == "true",
		GrafanaEndpoint: os.Getenv("GRAFANA_OTLP_ENDPOINT"),
		GrafanaAuth:     os.Getenv("GRAFANA_OTLP_AUTH"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
