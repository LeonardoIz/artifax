// Package media detects artifact content types and decides how each kind may be served.
//
// Uploads are trusted, but they share an origin with the admin panel, so only a short
// allowlist of types is ever rendered by the browser: HTML (sandboxed with scripts),
// SVG (sandboxed without scripts), images, audio, video and PDF. Text of any kind is
// shown as text/plain, and everything else is only offered as a download.
package media

import (
	"bytes"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"
)

// Kind groups content types by how they are served.
type Kind string

const (
	HTML   Kind = "html"
	SVG    Kind = "svg"
	Image  Kind = "image"
	Video  Kind = "video"
	Audio  Kind = "audio"
	PDF    Kind = "pdf"
	Text   Kind = "text"
	Binary Kind = "binary"
)

// byExt maps file extensions to types. It wins over declared types and sniffing
// because agents name files deliberately. Other text (code, configs…) is sniffed.
var byExt = map[string]string{
	".html": "text/html", ".htm": "text/html", ".xhtml": "application/xhtml+xml",
	".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif", ".ico": "image/x-icon",
	".pdf": "application/pdf",
	".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg", ".m4a": "audio/mp4", ".flac": "audio/flac",
	".txt": "text/plain", ".log": "text/plain", ".md": "text/markdown", ".csv": "text/csv",
	".json": "application/json", ".yaml": "application/yaml", ".yml": "application/yaml", ".xml": "application/xml",
}

// kinds is the allowlist of renderable types. Anything not listed is Binary unless
// it is text (see Classify).
var kinds = map[string]Kind{
	"text/html": HTML, "application/xhtml+xml": HTML,
	"image/svg+xml": SVG,
	"image/png":     Image, "image/jpeg": Image, "image/gif": Image, "image/webp": Image,
	"image/avif": Image, "image/x-icon": Image, "image/vnd.microsoft.icon": Image, "image/bmp": Image,
	"application/pdf": PDF,
	"video/mp4":       Video, "video/webm": Video, "video/quicktime": Video, "video/ogg": Video,
	"audio/mpeg": Audio, "audio/wav": Audio, "audio/x-wav": Audio, "audio/ogg": Audio,
	"audio/mp4": Audio, "audio/flac": Audio, "audio/webm": Audio,
	"application/json": Text, "application/yaml": Text, "application/xml": Text,
	"application/javascript": Text, "application/x-ndjson": Text, "application/toml": Text,
}

// Detect picks the content type of an upload from its file name, the declared type
// (multipart part or request header) and finally the bytes themselves.
func Detect(filename, declared string, content []byte) string {
	if ct, ok := byExt[strings.ToLower(path.Ext(filename))]; ok {
		return ct
	}
	switch ct := Normalize(declared); ct {
	case "", "application/octet-stream", "application/x-www-form-urlencoded", "multipart/form-data":
	default:
		return ct
	}
	switch sniffed := Normalize(http.DetectContentType(content)); {
	case sniffed == "text/xml":
		return "application/xml"
	case sniffed == "application/octet-stream" && looksLikeText(content):
		return "text/plain"
	default:
		return sniffed
	}
}

// Normalize lowercases a media type and drops its parameters. Invalid types yield "".
func Normalize(ct string) string {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil || !strings.Contains(mt, "/") {
		return ""
	}
	return mt
}

// Classify returns the serving kind of a normalized content type.
func Classify(ct string) Kind {
	if k, ok := kinds[ct]; ok {
		return k
	}
	if strings.HasPrefix(ct, "text/") || strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml") {
		return Text
	}
	return Binary
}

// Label is a short human name for a content type, e.g. "PNG" or "Text".
func Label(ct string) string {
	switch ct {
	case "text/plain":
		return "Text"
	case "text/markdown":
		return "Markdown"
	case "application/octet-stream":
		return "Binary"
	case "application/xhtml+xml":
		return "XHTML"
	}
	_, sub, _ := strings.Cut(ct, "/")
	sub = strings.TrimPrefix(strings.TrimPrefix(sub, "x-"), "vnd.")
	if i := strings.IndexAny(sub, "+."); i > 0 {
		sub = sub[:i]
	}
	if sub == "" {
		return "File"
	}
	return strings.ToUpper(sub)
}

// Response describes how an artifact is sent to a browser.
type Response struct {
	ContentType string // header value actually sent
	CSP         string // Content-Security-Policy; empty means none
	Download    bool   // Content-Disposition: attachment
}

const (
	// HTML: scripts run, but in an opaque origin (no cookies, no same-origin
	// access), and may load any HTTPS resource.
	htmlCSP = "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox allow-forms allow-modals allow-downloads; " +
		"default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval' https:; style-src 'unsafe-inline' https:; " +
		"img-src https: data: blob:; media-src https: data: blob:; font-src https: data:; " +
		"connect-src https:; worker-src blob:; frame-src https:; form-action https:; frame-ancestors 'none'; base-uri https:"
	// Passive documents (SVG, text, downloads): nothing executes, nothing is fetched.
	passiveCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; font-src data:; frame-ancestors 'none'"
	// Images and media: only the file itself loads.
	mediaCSP = "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'none'"
)

// Serve returns the safe way to deliver content of type ct for viewing in a browser.
func Serve(ct string) Response {
	switch Classify(ct) {
	case HTML:
		return Response{ContentType: ct + "; charset=utf-8", CSP: htmlCSP}
	case SVG:
		return Response{ContentType: ct, CSP: passiveCSP}
	case Image, Video, Audio:
		return Response{ContentType: ct, CSP: mediaCSP}
	case PDF:
		// Browsers refuse to render PDFs in sandboxed documents; their viewers run
		// isolated from the page origin anyway.
		return Response{ContentType: ct}
	case Text:
		return Response{ContentType: "text/plain; charset=utf-8", CSP: passiveCSP}
	}
	return Response{ContentType: "application/octet-stream", CSP: passiveCSP, Download: true}
}

// looksLikeText reports whether the start of b is plausibly text (UTF-8, no NULs).
func looksLikeText(b []byte) bool {
	b = b[:min(len(b), 8192)]
	for i := 0; i < utf8.UTFMax-1 && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1] // don't reject text cut mid-character
	}
	return len(b) > 0 && utf8.Valid(b) && bytes.IndexByte(b, 0) < 0
}
