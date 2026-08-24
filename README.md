# lumo-user-svc

User/profile microservice for AuroraFlix — rewritten from Java/Spring Boot
to Go, endpoint-for-endpoint and behavior-for-behavior compatible, to
compare real-world RAM usage on Render's free tier (same reason as
lumo-movies-svc).

Keeps a local `users` table in sync with Clerk (the identity provider) via a
Svix-signed webhook, and serves per-user watchlist/last-viewed data. Called
exclusively by `lumo-api-gateway` for anything user-scoped — see
`README-CLERK.md` for the full picture of how identity flows through this
system.

## Run locally

```bash
go run ./cmd/server
```

Needs the same environment variables the Java service used: `DATABASE_URL`,
`DATABASE_USERNAME`, `DATABASE_PASSWORD`, `CLERK_WEBHOOK_SECRET` (+ optional
`CLERK_WEBHOOK_SECRET_LOCAL`), `INTERNAL_SERVICE_SECRET`, `GATEWAY_URL`, and
optionally `GRAFANA_OTLP_ENABLED` / `GRAFANA_OTLP_ENDPOINT` / `GRAFANA_OTLP_AUTH`.
See `README-CLERK.md` for what each of these is and how to obtain them.

## Deploy

`Dockerfile` builds a static binary on a distroless base image — no JDK, no
Gradle. Render picks it up the same way it did the Java Dockerfile.

## Structure

- `cmd/server` — entrypoint, wiring
- `internal/models` — wire types (JSON field names match the old Jackson
  output exactly)
- `internal/users` — the `users` table, `GET /user/me`, and
  `POST /internal/users/{clerkId}/subscription` (mirrors UserController +
  InternalUserController + UserService)
- `internal/watchlist`, `internal/lastviewed` — per-user saved-title lists,
  each capped at 20 with oldest-eviction (mirrors WatchlistController/Service
  and LastViewedController/Service)
- `internal/clerkwebhook` — the Clerk→local-DB sync (`POST /webhooks/clerk`),
  including a hand-rolled Svix signature verifier (`svix.go`, unit-tested
  against Svix's own published example vector) instead of pulling in the
  official SDK, plus the best-effort call telling lumo-payments-svc about
  deleted accounts (mirrors ClerkWebhookController/Service +
  PaymentsSyncService)
- `internal/gatewayauth` — trusts `X-Clerk-User-Id`/`X-Internal-Api-Key`
  headers forwarded by lumo-api-gateway instead of verifying a session JWT
  itself (mirrors GatewayAuthenticationFilter)
- `internal/heartbeat`, `internal/cors`, `internal/reqlog`, `internal/metrics`,
  `internal/dbx`, `internal/config` — supporting pieces, same shape as
  lumo-movies-svc's own
