---
name: artifax
description: Use when the user wants something published or shared as a link - an HTML page, report or dashboard, an image, chart, screenshot, PDF, log, dataset or any other file - or pastes an Artifax link (/d/<id>). Uploads artifacts to Artifax with curl and returns their URL.
---

# Artifax

Artifax hosts artifacts: HTML pages, images, PDFs, video, audio, logs, text, JSON and any other file. Each upload gets a random id and lives at `$ARTIFAX_URL/d/<id>`. Uploading again to that id replaces the content and keeps the link.

Requires `ARTIFAX_URL` (e.g. `https://artifax.example.com`, no trailing slash) and `ARTIFAX_TOKEN` (`ax_…`). Stop and tell the user if either is unset.

## How each kind is shown

The type is detected from the file name, then the declared type, then the content. Always upload with a real file name and extension.

| Kind | At `/d/<id>` |
|---|---|
| HTML (`.html`) | Rendered as a page, sandboxed (see section 2) |
| Images (`.png .jpg .gif .webp .avif .svg`) | Shown directly. SVG scripts never run |
| PDF, video (`.mp4 .webm`), audio (`.mp3 .wav .ogg`) | Browser viewer or player |
| Text (`.txt .log .md .json .csv .yaml .xml`, code…) | Plain text, never rendered as HTML |
| Anything else (`.zip`, binaries…) | Downloaded |

Append `?download` to any artifact URL to force a download with the original file name.

## 1. Resolve the target

- An Artifax link `…/d/<id>`: the id is the last path segment (12 alphanumeric characters). Fetch the original bytes with `GET /api/docs/<id>/raw` before editing. Never edit from a rendered or summarized copy.
- "Update it" after publishing earlier in this conversation: reuse that id.
- Otherwise create a new artifact.

```bash
curl -sS -H "Authorization: Bearer $ARTIFAX_TOKEN" "$ARTIFAX_URL/api/docs/$ID/raw" -o file.ext
curl -sS -H "Authorization: Bearer $ARTIFAX_TOKEN" "$ARTIFAX_URL/api/docs?q=keyword"
```

## 2. Prepare the file

Any file up to the instance limit (25 MB by default) is accepted. Other file types need no preparation. HTML pages must be one self-contained UTF-8 file and run under this policy:

- Inline `<style>` and `<script>` work. External scripts, styles, fonts, images and `fetch()` load only over `https:`. `data:` and `blob:` URLs work.
- The page runs in a sandbox with an opaque origin. **`localStorage`, `sessionStorage`, `indexedDB` and cookies throw.** Keep state in memory or in the URL hash.
- It can't be embedded in iframes. Links that open new tabs work.

Prefer zero dependencies. If a library is needed (charts, diagrams), load it from an HTTPS CDN with a pinned version. Include `<meta charset="utf-8">`, `<meta name="viewport" content="width=device-width, initial-scale=1">` and a descriptive `<title>`.

To put images in a page, upload each image as its own artifact with `unlisted` or `public` visibility, then use its `url` in `<img src>`. Private images do not load inside pages.

## 3. Publish

**`title` and `description` are required** when creating. HTML pages may omit `title` if they have a `<title>`. Write a short title (under ~60 characters) and a one-sentence description of what the artifact is.

Create:

```bash
curl -sS -X POST "$ARTIFAX_URL/api/docs" \
  -H "Authorization: Bearer $ARTIFAX_TOKEN" \
  -F "file=@chart.png" \
  -F "title=Weekly signups" \
  -F "description=Signups per day for the last 4 weeks, by channel" \
  -F "visibility=unlisted"
```

Content generated on the fly can be piped in. Give it a file name so the type is detected:

```bash
some-command 2>&1 | curl -sS -X POST "$ARTIFAX_URL/api/docs" \
  -H "Authorization: Bearer $ARTIFAX_TOKEN" \
  -F "file=@-;filename=build.log" -F "title=CI build log" -F "description=Output of the failing build"
```

Replace the content of an existing artifact. The link, title and description stay the same; the type may change:

```bash
curl -sS -X PUT "$ARTIFAX_URL/api/docs/$ID" \
  -H "Authorization: Bearer $ARTIFAX_TOKEN" \
  -F "file=@chart.png"
```

Visibility:

- `private` (default): only the admin, after logging in.
- `unlisted`: anyone with the link; not listed on the home page.
- `public`: listed on the home page.

Use what the user asked for. When unsure, use `unlisted` for things meant to be shared and `private` for anything personal or sensitive (logs and data often are).

Change metadata without re-uploading:

```bash
curl -sS -X PATCH "$ARTIFAX_URL/api/docs/$ID" \
  -H "Authorization: Bearer $ARTIFAX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"visibility":"public","title":"New title","description":"New description"}'
```

Delete permanently (there is no trash; only when the user asks):

```bash
curl -sS -X DELETE "$ARTIFAX_URL/api/docs/$ID" -H "Authorization: Bearer $ARTIFAX_TOKEN"
```

## 4. Report

The response JSON has `id`, `url`, `kind` (`html`, `image`, `svg`, `pdf`, `video`, `audio`, `text`, `binary`) and `contentType`. Give the user the `url` and remember the `id` for later updates. If `kind` is not what you expected, fix the file name or extension and replace. Errors come back as `application/problem+json` with a `detail` field. Fix the cause and retry once.
