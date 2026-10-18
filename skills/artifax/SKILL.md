---
name: artifax
description: Use when the user asks for an HTML page, report, dashboard or visual explanation they will open in a browser, or pastes an Artifax link (/d/<id>). Writes one self-contained HTML file and publishes it to Artifax with curl.
---

# Artifax

Artifax hosts single HTML files. Each upload gets a random id and lives at `$ARTIFAX_URL/d/<id>`. Uploading again to the same id replaces the content and keeps the link.

Requires `ARTIFAX_URL` (e.g. `https://artifax.example.com`, no trailing slash) and `ARTIFAX_TOKEN` (`ax_…`). Stop and tell the user if either is unset. The full contract is at `$ARTIFAX_URL/openapi.json`.

## 1. Resolve the target

- An Artifax link `…/d/<id>`: the id is the last path segment (12 alphanumeric characters). Fetch the source with `GET /api/docs/<id>/raw` before editing. Never edit from a rendered or summarized copy.
- "Update it" after publishing earlier in this conversation: reuse that id.
- Otherwise create a new document.

```bash
curl -sS -H "Authorization: Bearer $ARTIFAX_TOKEN" "$ARTIFAX_URL/api/docs/$ID/raw" -o page.html
curl -sS -H "Authorization: Bearer $ARTIFAX_TOKEN" "$ARTIFAX_URL/api/docs?q=keyword"
```

## 2. Write the file

One UTF-8 HTML file, at most 10 MB by default. It is served under this policy:

- Inline `<style>` and `<script>` work. External scripts, styles, fonts, images and `fetch()` only over `https:`. `data:` and `blob:` URLs work.
- The page runs in a sandbox with an opaque origin. **`localStorage`, `sessionStorage`, `indexedDB` and cookies throw.** Keep state in memory or in the URL hash.
- It can't be embedded in iframes. Links that open new tabs work.

Prefer zero dependencies. If a library is needed (charts, diagrams), load it from an HTTPS CDN with a pinned version. Include `<meta charset="utf-8">`, `<meta name="viewport" content="width=device-width, initial-scale=1">` and a descriptive `<title>`. The title becomes the document title when none is sent.

## 3. Publish

Create:

```bash
curl -sS -X POST "$ARTIFAX_URL/api/docs" \
  -H "Authorization: Bearer $ARTIFAX_TOKEN" \
  -F "file=@page.html" \
  -F "title=Short title" \
  -F "description=One line about what this is" \
  -F "visibility=unlisted"
```

Replace the content of an existing document (same link):

```bash
curl -sS -X PUT "$ARTIFAX_URL/api/docs/$ID" \
  -H "Authorization: Bearer $ARTIFAX_TOKEN" \
  -F "file=@page.html"
```

Visibility:

- `private` (default): only the admin, after logging in.
- `unlisted`: anyone with the link; not listed on the home page.
- `public`: listed on the home page.

Use what the user asked for. When unsure, use `unlisted` for things meant to be shared and `private` for anything personal or sensitive.

Change metadata without re-uploading:

```bash
curl -sS -X PATCH "$ARTIFAX_URL/api/docs/$ID" \
  -H "Authorization: Bearer $ARTIFAX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"visibility":"public","title":"New title"}'
```

Delete permanently (there is no trash; only when the user asks):

```bash
curl -sS -X DELETE "$ARTIFAX_URL/api/docs/$ID" -H "Authorization: Bearer $ARTIFAX_TOKEN"
```

## 4. Report

The response JSON has `id` and `url`. Give the user the `url` and remember the `id` for later updates. Errors come back as `application/problem+json` with a `detail` field. Fix the cause and retry once.
