# Artifax

Self-hosted publishing for single HTML files. Agents upload with `curl`, everyone opens a link.

- One Go binary, one data directory: SQLite for metadata, files on disk. No external services.
- Each upload gets a random id: `https://artifax.example.com/d/<id>`. Uploading again to that id replaces the content and keeps the link.
- Three visibilities: `public` (listed on the home page), `unlisted` (anyone with the link), `private` (admin only).
- Admin login via SSO (OpenID Connect, e.g. Authelia) or a single email/password from the environment.
- Minimal admin panel to manage documents and API tokens.

## Quick start (local)

```bash
export ARTIFAX_BASE_URL=http://localhost:8080
export ARTIFAX_DATA_DIR=./data
export ARTIFAX_BOOTSTRAP_TOKEN=ax_local_dev_bootstrap_token
export AUTH_MODE=basic ADMIN_EMAIL=admin@example.com ADMIN_PASSWORD=change-me-please
go run ./cmd/artifax
```

Publish a page:

```bash
curl -X POST http://localhost:8080/api/docs \
  -H "Authorization: Bearer ax_local_dev_bootstrap_token" \
  -F "file=@page.html" -F "visibility=public"
```

Open `http://localhost:8080/` for the index and `/admin` for the panel.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `ARTIFAX_BASE_URL` | (required) | Public URL, e.g. `https://artifax.example.com`. Used for links, cookies and the OIDC callback. |
| `ARTIFAX_LISTEN` | `:8080` | Listen address. |
| `ARTIFAX_DATA_DIR` | `/data` | Holds `artifax.db` and `blobs/`. Back up this directory. |
| `ARTIFAX_MAX_UPLOAD_MB` | `10` | Maximum document size. |
| `ARTIFAX_BOOTSTRAP_TOKEN` | | Optional API token (`ax_…`, 24+ chars), stored only if no token was ever created. |
| `ARTIFAX_TRUST_PROXY` | `false` | Use `X-Forwarded-For` for the client IP (login rate limiting). Enable only behind a reverse proxy. |
| `AUTH_MODE` | | `sso`, `basic`, or empty (no admin; only public and unlisted documents are reachable). |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | | `basic` mode credentials. Password: at least 12 characters. |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | | `sso` mode provider settings. |
| `ADMIN_GROUPS` | `admins` | `sso` mode: comma-separated groups whose members are admin. Anyone else is treated as anonymous. |
| `SESSION_TTL` | `168h` | Admin session lifetime. |

### Authelia

Register Artifax as an OpenID Connect client:

```yaml
identity_providers:
  oidc:
    clients:
      - client_id: artifax
        client_name: Artifax
        client_secret: '$pbkdf2-sha512$...'   # hash of OIDC_CLIENT_SECRET
        authorization_policy: one_factor
        require_pkce: true
        pkce_challenge_method: S256
        redirect_uris:
          - https://artifax.example.com/auth/callback
        scopes: [openid, profile, email, groups]
        token_endpoint_auth_method: client_secret_basic
```

Then set `AUTH_MODE=sso`, `OIDC_ISSUER=https://auth.example.com`, `OIDC_CLIENT_ID=artifax`, `OIDC_CLIENT_SECRET=<plain secret>` and `ADMIN_GROUPS=admins`. No access rules are needed in the reverse proxy: Artifax decides who sees what.

## API

All endpoints take `Authorization: Bearer ax_…`. Tokens are created in the panel (`/admin/tokens`) and have full access. The OpenAPI spec is served at `/openapi.json`.

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/docs` | Create. Multipart (`file`, `title`, `description`, `visibility`) or raw `text/html` body with those fields as query parameters. |
| `PUT` | `/api/docs/{id}` | Replace content, keep id and URL. Metadata is optional. |
| `GET` | `/api/docs` | List (`q`, `visibility`, `limit`, `cursor`). |
| `GET` | `/api/docs/{id}` | Metadata. |
| `GET` | `/api/docs/{id}/raw` | HTML source. |
| `PATCH` | `/api/docs/{id}` | JSON `{title, description, visibility}`. |
| `DELETE` | `/api/docs/{id}` | Delete permanently. |

Errors use `application/problem+json`.

### Agent skill

[`skills/artifax/SKILL.md`](skills/artifax/SKILL.md) teaches an agent (Claude Code or similar) to write compatible HTML and publish it. Copy it into your agent's skills directory and set `ARTIFAX_URL` and `ARTIFAX_TOKEN`.

## Security model

Documents and the admin panel share one host, so documents are served with `Content-Security-Policy: sandbox …` without `allow-same-origin`. Their scripts run in an opaque origin: they can't read the admin session cookie or act on `/admin` or `/api` on the admin's behalf. As a side effect, `localStorage` and cookies are unavailable inside documents. External resources are allowed over HTTPS only.

The admin session cookie is `HttpOnly` and `SameSite=Lax`. Every state-changing browser request is checked with Go's cross-origin protection (`Sec-Fetch-Site`/`Origin`). API tokens and session cookies are stored only as SHA-256 hashes.

## Development

```bash
go test ./...
go vet ./...
go build -o artifax ./cmd/artifax
```

Layout:

```
cmd/artifax          entry point
internal/config      environment configuration
internal/store       SQLite schema, migrations and queries
internal/blobs       content-addressed file storage
internal/docs        document operations (validation, storage, cleanup)
internal/auth        API tokens, sessions, basic login, OIDC
internal/server      HTTP routes, templates and static assets
skills/artifax       agent skill
```
