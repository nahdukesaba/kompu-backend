# Kompu ↔ SSO Balai (browser SSO)

Kompu signs users in through **sso-balai** the same way SILAPET does
(`silapet/SSO-BALAI-INTEGRATION.md` is the reference): authorization code +
PKCE S256, confidential backend, no client secret in the browser, no OIDC.

> Placeholders only. Never put real secrets in this file, tickets or chat.

## Flow

1. Browser → `GET /api/auth/sso/start`. Backend stores `SHA-256(state)` in
   `public.kompu_sso_flows` (TTL `SSO_FLOW_TTL`), derives the PKCE verifier as
   `base64url(HMAC-SHA256(SSO_FLOW_HMAC_KEY, "kompu-pkce:" + state))` (never
   stored) and redirects to `{SSO_BASE_URL}/authorize`.
2. sso-balai redirects to the **frontend** `/auth/sso/callback` with
   `code`+`state`, or `state`+`status=pending|denied`.
3. The callback page strips the query from history and POSTs once to
   `/api/auth/sso/exchange`. The backend consumes the state atomically,
   exchanges the code server-to-server (`POST /api/v1/auth/sso/exchange`),
   then requires an **approved** `app_memberships` row for application code
   `kompu` (role `user` or `admin`). Otherwise it revokes the fresh Central
   token session and answers `403 ACCOUNT_*`.
4. The session is the `kompu-session` cookie: AES-256-GCM (AAD = cookie name),
   HttpOnly, `SameSite=Lax`, `Secure` in production, host-only. It holds the
   Central `sid`, access/refresh tokens and the Supabase `session_id`.
5. Every protected request (all mutations, `/auth/me`, `/auth/logout`):
   introspect the `sid` (`POST /api/v1/auth/sso/introspect`, Bearer
   `SSO_INTROSPECTION_SECRET`), check access-token expiry, check that the
   `auth.sessions` row still exists for the user, and re-read the membership.
   Errors fail closed. Expired token or changed role → `401` and the
   frontend refreshes once (`POST /api/auth/refresh`, same `sid`).
6. Logout: `POST /api/auth/logout` → `{centralLogoutRequired:true}` (cookie
   kept); the frontend goes to `/api/auth/sso/logout?cancel_return=/path` →
   sso-balai confirmation. Confirm revokes the `sid` everywhere; Cancel keeps it.
   Both return to `/login`, which calls `/api/auth/me`: still signed in → home.

Kompu's own auth routes are under `/api/auth` (resources stay under `/api/v1`),
matching the launch URL sso-balai registers, as for SILAPET and Klinik.

Design choice (agreed with the user): the access token is **not** verified
via JWKS. It only ever lives inside the sealed cookie; validity comes from
introspection + `auth.sessions` + membership in the shared database.

Kompu shares sso-balai's Supabase database (same project), so it reads
`public.app_users`, `public.app_memberships`, `public.applications` and
`auth.sessions` directly. It never writes them.

## Registration in sso-balai

sso-balai registers Kompu in commit `a388348` ("feat: add Kompu browser SSO
client and portal card"). The client is active only when its env is set and
sso-balai is restarted:

| Item | sso-balai env | Value |
| --- | --- | --- |
| Enable | `SSO_KOMPU_ENABLED` | `true` |
| Client ID | `SSO_KOMPU_CLIENT_ID` | `kompu-web` |
| Application code | `SSO_KOMPU_APPLICATION_CODE` | `kompu` |
| Access policy | `SSO_KOMPU_ACCESS_POLICY` | `AUTO_USER` (first visit creates `user/pending`) |
| Redirect URIs | `SSO_KOMPU_REDIRECT_URIS` | `https://kompu.bp3kpsumatera2.com/auth/sso/callback` (local: `http://kompu.test:8084/auth/sso/callback`) |
| Post-logout URIs | `SSO_KOMPU_POST_LOGOUT_REDIRECT_URIS` | `https://kompu.bp3kpsumatera2.com/login` (local: `http://kompu.test:8084/login`) |
| Introspection secret | `SSO_KOMPU_INTROSPECTION_SECRET` | ≥ 32 chars, identical to Kompu's `SSO_INTROSPECTION_SECRET`; hand over privately |
| Portal launch URL (optional) | `PORTAL_KOMPU_LAUNCH_URL` | `https://kompu.bp3kpsumatera2.com/api/auth/sso/start` (local: `http://kompu.test:8084/api/auth/sso/start`) |

Database (shared Supabase), once:

```sql
INSERT INTO public.applications (code, name, is_active)
VALUES ('kompu', 'Kompu', TRUE)
ON CONFLICT (code) DO NOTHING;
```

Kompu has no admin screen: approve members in sso-balai (or with
`UPDATE public.app_memberships SET status = 'approved' …` for the `kompu`
application).

## Kompu deployment checklist

1. Apply migration `000005_create_kompu_sso_flows` (`go run ./cmd/migrate up`).
2. Set in the backend `.env` (generate with `openssl rand -base64 32` /
   `openssl rand -base64 48`):
   `SSO_BASE_URL=https://sso.bp3kpsumatera2.com`, `SSO_CLIENT_ID=kompu-web`,
   `SSO_REDIRECT_URI=https://kompu.bp3kpsumatera2.com/auth/sso/callback`,
   `SSO_POST_LOGOUT_REDIRECT_URI=https://kompu.bp3kpsumatera2.com/login`,
   `SSO_FLOW_HMAC_KEY`, `SSO_INTROSPECTION_SECRET`, `APP_SESSION_KEY`,
   `APP_SESSION_COOKIE_SECURE=true`,
   `CORS_ALLOWED_ORIGINS=https://kompu.bp3kpsumatera2.com`.
   `APP_ENV=production` refuses to start while any of these is missing.
3. HTTPS is required in production (`Secure` cookie): run certbot for
   `kompu.bp3kpsumatera2.com` first.
4. Frontend build: `VITE_USE_MOCK_API=false`, `VITE_API_BASE_URL=/api/v1`.

## Local development

One origin, as registered: `http://kompu.test:8084` serves kompu-web and
proxies `/api` to kompu-backend on `localhost:8080` (`vite.config.ts`).
`kompu.test` and `sso-balai.test` must resolve to `127.0.0.1`. Backend `.env`:
`APP_SESSION_COOKIE_SECURE=false`, `CORS_ALLOWED_ORIGINS=http://kompu.test:8084`,
and the SSO values from `.env.example`.

## Tests

`internal/handler/auth_handler_test.go` runs the full flow against a fake
sso-balai: approved login, pending and denied results, pending membership
(refused + Central session discarded), replayed/unknown state, wrong
introspection secret (fails closed, cookie cleared), Central logout, stale
role, `/auth/me` 204, and `cancel_return` sanitizing.
`pkg/middleware/sso_test.go` covers the bad-origin check.
