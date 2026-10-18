# Changelog

All notable changes to this project are documented here, following
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

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

[1.0.0]: https://github.com/LeonardoIz/artifax/releases/tag/v1.0.0
