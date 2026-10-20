# Changelog

All notable changes to this project are documented here, following
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.1.1] - 2026-10-20

### Added

- Pagination has a "Newest" link back to the first page.
- At startup, files left by interrupted uploads are removed.

### Changed

- Uploads are streamed to disk instead of being held in memory, so large files no
  longer need twice their size in RAM.
- Slow connections: downloads have no time limit, and uploads get 2 minutes plus one
  per 5 MB of `ARTIFAX_MAX_UPLOAD_MB`. Both used to be cut after 2 minutes.
- An expired token gets `token expired on <date>` instead of `invalid or revoked token`.
- The `Authorization` scheme is case-insensitive (`bearer` works).
- UI pages send `Cross-Origin-Opener-Policy: same-origin`, so an artifact that opens
  the admin in a popup cannot navigate it later.
- Expired tokens show a "Remove" button instead of "Revoke".
- Dates rendered without JavaScript say "UTC". The 413 error is given in MB.
- The favicon follows the browser's light or dark theme.
- Release notes are taken from this changelog.

### Removed

- The login attempt limit for `AUTH_MODE=basic`, and `ARTIFAX_TRUST_PROXY`, which
  only fed it. Logs show the connecting address (the proxy, behind one).

### Fixed

- Token names are limited to 100 characters, not 100 bytes, so accented names fit.

## [1.1.0] - 2026-10-20

### Added

- API tokens can expire: 7, 30 or 90 days (default), a year, or never. Expired tokens
  are rejected and marked in the admin page. Existing tokens never expire.

### Changed

- Panel and Tokens are now one Admin page with both artifacts and API tokens.
  `/admin/tokens` redirects there.
- New logo: an upload arrow. The name is written "Artifax".
- Text arrows in the UI are now icons (back, older, open, copy, download).
- The UI says "artifact" everywhere instead of "document".
- Calmer page transitions: the header stays still and pages cross-fade, instead of
  titles morphing between pages. The header is now opaque.
- Active token names are unique (ignoring case). Existing duplicates get their id
  appended.
- Upload and Usage start closed; Usage sits above the token form, like Upload above
  the artifact filters.

### Fixed

- The header no longer flickers or looks blurry while navigating between pages.
- Reloading after creating a token no longer asks to resubmit the form or creates a
  second token.
- Collapsible panels no longer stall just before closing.

## [1.0.0] - 2026-10-18

First release.

- Upload any artifact with `curl`: HTML pages, images, PDFs, video, audio, logs, text and
  other files. Each gets a random, stable link at `/d/<id>`; uploading again replaces it.
- Every type is served from a fixed allowlist: HTML sandboxed in an opaque origin, SVG
  sandboxed without scripts, media and PDF natively, text as `text/plain`, anything else
  as a download.
- Visibility: public (listed on the home page), unlisted or private.
- Admin panel with previews, inline visibility changes and API tokens.
- Admin login with any OpenID Connect provider (group-based) or a single email/password.
- `/skill` serves an agent skill with the instance URL filled in.
- One Go binary with SQLite and files on disk. Docker image for amd64/arm64.

[Unreleased]: https://github.com/LeonardoIz/artifax/compare/v1.1.1...HEAD
[1.1.1]: https://github.com/LeonardoIz/artifax/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/LeonardoIz/artifax/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/LeonardoIz/artifax/releases/tag/v1.0.0
