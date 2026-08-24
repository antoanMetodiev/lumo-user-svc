# Clerk setup for lumo-user-svc

This service no longer does its own login/register/password hashing — Clerk
owns identity now. This service only:

1. Keeps a local `users` row per Clerk account in sync, via a webhook Clerk
   calls whenever a user signs up / updates their profile / deletes their
   account (`internal/clerkwebhook` → `POST /webhooks/clerk`).
2. Trusts that whoever's asking on the one endpoint that needs to know who's
   asking (`GET /user/me`, plus `/watchlist` and `/last-viewed`) has already
   been verified — by `lumo-api-gateway`, not by this service.

**This service no longer talks to Clerk's JWKS at all.** Verifying the
session JWT (`Authorization: Bearer <token>`) now happens exactly once, in
`lumo-api-gateway`. This service instead trusts two headers the gateway
forwards on every request it proxies through — see `internal/gatewayauth`:

- `X-Clerk-User-Id` — the Clerk user id the gateway already verified.
- `X-Internal-Api-Key` — a shared secret proving the caller really is the
  gateway. This service still has a public URL for now (no private network
  yet), so this is what stands between "trust whatever `X-Clerk-User-Id`
  says" and full account impersonation by anyone who finds this service's
  URL — a stopgap until it moves behind a private network.

If you're calling this service directly (e.g. local testing without the
gateway in front of it), you need to send both headers yourself — there's no
more `getToken()` shortcut here.

## 1. Environment variables

| Variable | Where to find it |
|---|---|
| `CLERK_WEBHOOK_SECRET` | Created in step 2 below — looks like `whsec_...`. This is the **production** value; leave it as-is once set. |
| `CLERK_WEBHOOK_SECRET_LOCAL` | Optional, local dev only. When you register an ngrok tunnel as a webhook endpoint for local testing, Clerk gives that endpoint its own separate signing secret — put that one here instead of overwriting `CLERK_WEBHOOK_SECRET`. Unset in production, so it always falls through to the real one there. |
| `INTERNAL_SERVICE_SECRET` | Any long random string you generate yourself — must be the **exact same value** set on `lumo-api-gateway` (and `lumo-payments-svc`). |
| `GATEWAY_URL` | Where `lumo-api-gateway` lives — defaults to the production URL; override for local dev. |

Plus the existing `DATABASE_URL`, `DATABASE_USERNAME`, `DATABASE_PASSWORD` for
the Postgres database (the Go binary connects with `pgx` directly — see
`internal/dbx` — no ORM/auto-migration; the schema below must already exist).

`CLERK_ISSUER` / `CLERK_JWKS_URL` live on `lumo-api-gateway` instead — that's
the only service left that needs them.

## 2. Register the webhook in the Clerk dashboard

1. Clerk dashboard → **Configure → Webhooks → Add Endpoint**.
2. Endpoint URL: `http://localhost:8083/webhooks/clerk` while testing locally
   (Clerk needs to reach it — use a tunnel like `ngrok http 8083` if Clerk
   can't hit `localhost` directly, then use the tunnel URL). Once the service
   is deployed on Render, switch this to the Render URL.
3. Subscribe to events: `user.created`, `user.updated`, `user.deleted`.
4. Copy the **Signing Secret** it gives you (`whsec_...`) into
   `CLERK_WEBHOOK_SECRET`.

## 3. Enable sign-in methods

Clerk dashboard → **Configure → SSO Connections** → enable **Google**.
Email/password is on by default under **Configure → Email, Phone, Username**.
No code changes needed on either side for this — the `<SignIn />`/`<SignUp />`
components on the frontend automatically show whatever's enabled here.

## 4. Database

Three tables, already existing in production (created back when this was a
Hibernate `ddl-auto=update` service — the Go rewrite reads/writes them with
plain SQL via `internal/dbx`, no migrations of its own):

| Table | Notes |
|---|---|
| `users` | `clerk_id` (PK, e.g. `user_2abc...`), `email`, `display_name`, `profile_image_url`, `subscription_type` (`REGULAR_USER`/`PRO_USER`), `created_at`/`updated_at`. |
| `watchlist_items` | Per-user saved titles, unique on `(clerk_id, record_id)`, capped at 20/user (oldest evicted). |
| `last_viewed_items` | Per-user view history, unique on `(clerk_id, title)`, capped at 20/user (oldest evicted). |

## 5. Testing it end to end

1. Start this service (`CLERK_WEBHOOK_SECRET`, `INTERNAL_SERVICE_SECRET`,
   `DATABASE_*` all set).
2. Start `lumo-api-gateway` too (`CLERK_ISSUER`, `CLERK_JWKS_URL`, and the
   same `INTERNAL_SERVICE_SECRET` as this service).
3. Start the Next.js app (`npm run dev` in `my-app`, with its own Clerk keys
   in `.env.local`).
4. Sign up on the frontend (`/sign-up`, or the "Sign up" button in the nav).
5. Check the webhook fired: either the Clerk dashboard's webhook logs, or a
   new row in the `users` table.
6. Call `GET http://localhost:8080/user/me` (the gateway's port, not this
   service's) with `Authorization: Bearer <token>` — the token comes from
   `await getToken()` via Clerk's `useAuth()` hook on the frontend, or the
   `__session` value if you want to grab it manually for a curl test. The
   gateway verifies it and forwards the request here.
7. To call this service directly instead (bypassing the gateway), you'd need
   to send `X-Clerk-User-Id` and `X-Internal-Api-Key` yourself — there's no
   session token this service understands anymore.
