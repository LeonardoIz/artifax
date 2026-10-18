# Artifax

Self-hosted sharing for artifacts: HTML pages, images, PDFs, logs, any file. Agents upload with `curl` and get back a link.

- One Go binary and one data directory (SQLite plus files on disk).
- Each upload gets a random id at `/d/<id>`. Uploading again to that id replaces it and keeps the link.
- Visibility: `public` (listed on the home page), `unlisted` (anyone with the link) or `private` (admin only).
- Admin panel for artifacts and API tokens. Login with any OIDC provider or a single email/password.

## Run

```bash
docker compose up -d   # see compose.yaml and .env.example
```

Or from source: `ARTIFAX_BASE_URL=http://localhost:8080 ARTIFAX_DATA_DIR=./data AUTH_MODE=basic ADMIN_EMAIL=me@example.com ADMIN_PASSWORD=change-me go run ./cmd/artifax`

Images for `amd64` and `arm64` are on `ghcr.io/leonardoiz/artifax`. The container runs as `1000:1000`, so a bind-mounted data directory must be writable by that uid. Put Artifax behind a reverse proxy that terminates TLS.

## Configuration

| Variable | Default | |
|---|---|---|
| `ARTIFAX_BASE_URL` | required | Public URL, e.g. `https://artifax.example.com` |
| `ARTIFAX_DATA_DIR` | `/data` | Database and files. Back this up |
| `ARTIFAX_LISTEN` | `:8080` | |
| `ARTIFAX_MAX_UPLOAD_MB` | `25` | |
| `ARTIFAX_BOOTSTRAP_TOKEN` | | API token (`ax_…`) created on first start |
| `ARTIFAX_TRUST_PROXY` | `false` | Use `X-Forwarded-For` for the client IP |
| `AUTH_MODE` | | `sso`, `basic` or empty (no admin, public and unlisted artifacts only) |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | | `basic` mode |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | | `sso` mode |
| `ADMIN_GROUPS` | `admins` | `sso` mode: users in these groups are admin; everyone else is anonymous |
| `SESSION_TTL` | `168h` | |

### SSO

Works with any OpenID Connect provider (Authelia, Authentik, Keycloak, Pocket ID, Zitadel…). Register a confidential client with:

- Redirect URI: `<ARTIFAX_BASE_URL>/auth/callback`
- Scopes: `openid profile email groups`
- Authorization code flow with PKCE (S256), client secret sent with HTTP basic auth (`client_secret_basic`)
- A `groups` claim in the ID token or userinfo

Artifax itself must be able to reach `OIDC_ISSUER`, not only the browser. No proxy rules are needed: Artifax decides who sees what.

## API

Every request needs `Authorization: Bearer ax_…`. Create tokens in the panel.

```bash
curl -X POST "$ARTIFAX_URL/api/docs" -H "Authorization: Bearer $ARTIFAX_TOKEN" \
  -F "file=@chart.png" -F "title=Weekly signups" -F "description=Signups per day" -F "visibility=unlisted"
```

| | | |
|---|---|---|
| `POST` | `/api/docs` | Create. `title` and `description` are required; HTML can take the title from `<title>` |
| `PUT` | `/api/docs/{id}` | Replace the content |
| `PATCH` | `/api/docs/{id}` | JSON `{title, description, visibility}` |
| `GET` | `/api/docs` | List (`q`, `visibility`, `limit`, `cursor`) |
| `GET` | `/api/docs/{id}` | Metadata |
| `GET` | `/api/docs/{id}/raw` | Original file |
| `DELETE` | `/api/docs/{id}` | Delete permanently |

Each instance serves an agent skill with its own URL at `/skill`:

```bash
mkdir -p ~/.claude/skills/artifax && curl -fsSL "$ARTIFAX_URL/skill.md" -o ~/.claude/skills/artifax/SKILL.md
```

## Security

Artifacts share a host with the admin panel, so each type is served from a fixed allowlist:

- **HTML:** sandboxed in an opaque origin. Scripts run but have no cookies and no access to the panel or API.
- **SVG:** sandboxed, and scripts never run.
- **Images, video, audio and PDF:** shown natively.
- **Text** (logs, JSON, XML, code…): always shown as `text/plain`.
- **Anything else:** download only.

Responses use `nosniff`. Sessions and tokens are stored as hashes, and browser writes are checked for cross-origin requests.

## Development

```bash
go test ./... && go vet ./...
```

[MIT](LICENSE)
