package media

import (
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	tests := []struct {
		name, filename, declared string
		content                  []byte
		want                     string
	}{
		{"extension wins", "shot.PNG", "application/octet-stream", png, "image/png"},
		{"extension beats declared", "notes.txt", "text/html", []byte("<script>x</script>"), "text/plain"},
		{"declared type", "", "image/webp", []byte("RIFF....WEBP"), "image/webp"},
		{"declared with params", "", "text/html; charset=utf-8", []byte("<p>x</p>"), "text/html"},
		{"form default ignored, sniff html", "", "application/x-www-form-urlencoded", []byte("<!doctype html><p>x"), "text/html"},
		{"sniff png", "", "", png, "image/png"},
		{"sniff text", "", "", []byte("2026-10-18 INFO started\n"), "text/plain"},
		{"utf8 text without hints", "", "", []byte("héllo wörld ✓\n"), "text/plain"},
		{"binary", "blob", "", []byte{0x00, 0x01, 0xfe, 0xff, 0x00}, "application/octet-stream"},
		{"log extension", "app.log", "", []byte("<html>not really</html>"), "text/plain"},
		{"xml sniff", "", "", []byte("<?xml version=\"1.0\"?><a/>"), "application/xml"},
	}
	for _, tc := range tests {
		if got := Detect(tc.filename, tc.declared, tc.content); got != tc.want {
			t.Errorf("%s: Detect = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestServeIsSafe: nothing but HTML may run scripts, and only allowlisted types render.
func TestServeIsSafe(t *testing.T) {
	tests := []struct {
		ct         string
		wantType   string
		scripts    bool // CSP allows scripts
		sandboxed  bool
		download   bool
		noCSPAllow bool // PDF: no CSP at all
	}{
		{"text/html", "text/html; charset=utf-8", true, true, false, false},
		{"application/xhtml+xml", "application/xhtml+xml; charset=utf-8", true, true, false, false},
		{"image/svg+xml", "image/svg+xml", false, true, false, false},
		{"image/png", "image/png", false, false, false, false},
		{"video/mp4", "video/mp4", false, false, false, false},
		{"application/pdf", "application/pdf", false, false, false, true},
		{"text/plain", "text/plain; charset=utf-8", false, true, false, false},
		{"application/xml", "text/plain; charset=utf-8", false, true, false, false},
		{"text/xml", "text/plain; charset=utf-8", false, true, false, false},
		{"application/json", "text/plain; charset=utf-8", false, true, false, false},
		{"text/javascript", "text/plain; charset=utf-8", false, true, false, false},
		{"application/rss+xml", "text/plain; charset=utf-8", false, true, false, false},
		{"application/zip", "application/octet-stream", false, true, true, false},
		{"application/x-shockwave-flash", "application/octet-stream", false, true, true, false},
		{"image/tiff", "application/octet-stream", false, true, true, false},
	}
	for _, tc := range tests {
		r := Serve(tc.ct)
		if r.ContentType != tc.wantType {
			t.Errorf("%s: content type %q, want %q", tc.ct, r.ContentType, tc.wantType)
		}
		if got := strings.Contains(r.CSP, "script-src"); got != tc.scripts {
			t.Errorf("%s: scripts allowed = %v, want %v (csp %q)", tc.ct, got, tc.scripts, r.CSP)
		}
		if got := strings.HasPrefix(r.CSP, "sandbox"); got != tc.sandboxed {
			t.Errorf("%s: sandboxed = %v, want %v", tc.ct, got, tc.sandboxed)
		}
		if strings.Contains(r.CSP, "allow-same-origin") {
			t.Errorf("%s: sandbox must never allow same origin", tc.ct)
		}
		if r.Download != tc.download {
			t.Errorf("%s: download = %v, want %v", tc.ct, r.Download, tc.download)
		}
		if (r.CSP == "") != tc.noCSPAllow {
			t.Errorf("%s: unexpected CSP presence %q", tc.ct, r.CSP)
		}
	}
}
