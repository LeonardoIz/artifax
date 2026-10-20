# Changelog

All notable changes to this project are documented here, following
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/LeonardoIz/artifax/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/LeonardoIz/artifax/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/LeonardoIz/artifax/releases/tag/v1.0.0
