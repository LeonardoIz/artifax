# Artifax

Self-hosted sharing for artifacts (HTML, images, PDFs, logs, any file) uploaded by agents with curl. Single user: there is one admin; everyone else is anonymous.

## Principles

- Keep it minimal: stdlib first, no new dependencies without a strong reason. Direct deps today: `modernc.org/sqlite`, `go-oidc`, `oauth2`.
- No frontend build. Server-rendered `html/template`, one CSS and one JS file. Every page must work without JavaScript; JS only enhances.
- Security first for anything served from `/d/{id}`: it shares an origin with the admin panel. Serving rules live in `internal/media` (allowlist; HTML sandboxed without `allow-same-origin`, text always `text/plain`, unknown types download only). Never relax them casually.

## Layout

```
cmd/artifax       main: config, wiring, graceful shutdown, `healthcheck` subcommand
internal/config   env vars (see README)
internal/store    SQLite: schema in migrations/NNN_*.sql (embedded, PRAGMA user_version)
internal/blobs    content-addressed files under DATA_DIR/blobs
internal/docs     artifact service: detect type, required metadata, blob GC
internal/media    type detection, kinds, per-kind headers/CSP
internal/auth     API tokens, sessions, basic login, OIDC
internal/server   routes, handlers, ui/templates, ui/static (embedded)
skills/artifax    agent skill, served at /skill with the instance URL filled in
```

Internally artifacts are still called "docs" (package, table, `/api/docs`); the UI says "Artifacts".

## Commands

```bash
go test ./... && go vet ./...   # must pass; CI also checks gofmt and runs -race
go run ./cmd/artifax            # needs ARTIFAX_BASE_URL, ARTIFAX_DATA_DIR, AUTH_MODE…
```

Local dev: `ARTIFAX_BASE_URL=http://localhost:8080 ARTIFAX_DATA_DIR=./data AUTH_MODE=basic ADMIN_EMAIL=admin@admin.local ADMIN_PASSWORD=admin123`. Docker is not available locally; images are built by GitHub Actions.

## Conventions

- Tests: table-driven, `httptest` against the real handler (`internal/server/server_test.go`), a fake OIDC provider in `internal/auth`. Add tests for behaviour changes, especially serving/security.
- Schema changes: add a new migration file, never edit an applied one.
- UI text in English. Keep the visual style: neutral palette, light/dark via `prefers-color-scheme`, subtle motion that honours `prefers-reduced-motion`.
- Static assets are fingerprinted (`asset` template func); no cache busting by hand.
- Update `README.md`, `skills/artifax/SKILL.md` and `CHANGELOG.md` (Keep a Changelog, under `Unreleased`) when behaviour or the API changes.
- Releases: semver tags `vX.Y.Z` trigger CI to publish `ghcr.io/leonardoiz/artifax` and binaries.
- Commit and push only when asked.
