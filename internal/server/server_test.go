package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeonardoIz/artifax/internal/auth"
	"github.com/LeonardoIz/artifax/internal/blobs"
	"github.com/LeonardoIz/artifax/internal/config"
	"github.com/LeonardoIz/artifax/internal/docs"
	"github.com/LeonardoIz/artifax/internal/server"
	"github.com/LeonardoIz/artifax/internal/store"
)

const (
	adminEmail    = "admin@example.com"
	adminPassword = "correct-horse-battery"
)

type env struct {
	t       *testing.T
	handler http.Handler
	token   string
	session *http.Cookie
}

func newEnv(t *testing.T, mutate func(*config.Config)) *env {
	t.Helper()
	dir := t.TempDir()
	base, _ := url.Parse("http://artifax.test")
	cfg := &config.Config{
		BaseURL:        base,
		DataDir:        dir,
		MaxUploadBytes: 64 << 10,
		SessionTTL:     time.Hour,
		AuthMode:       config.AuthBasic,
		AdminEmail:     adminEmail,
		AdminPassword:  adminPassword,
		AdminGroups:    []string{"admins"},
	}
	if mutate != nil {
		mutate(cfg)
	}

	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "artifax.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bl, err := blobs.New(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokens := auth.NewTokens(st)
	_, secret, err := tokens.Create(ctx, "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Deps{
		Config:   cfg,
		Store:    st,
		Docs:     docs.NewService(st, bl, cfg.MaxUploadBytes, log),
		Tokens:   tokens,
		Sessions: auth.NewSessions(st, cfg.SessionTTL, false),
		Log:      log,
		Version:  "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, handler: srv.Handler(), token: secret}
}

func (e *env) do(r *http.Request) *httptest.ResponseRecorder {
	e.t.Helper()
	if e.session != nil {
		r.AddCookie(e.session)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, r)
	return rec
}

func (e *env) api(method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, path, body)
	r.Header.Set("Authorization", "Bearer "+e.token)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return e.do(r)
}

func multipartBody(t *testing.T, content string, fields map[string]string) (*bytes.Buffer, string) {
	return multipartFile(t, "page.html", content, fields)
}

func multipartFile(t *testing.T, filename, content string, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	fw.Write([]byte(content))
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

type docResp struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Visibility  string `json:"visibility"`
	ContentType string `json:"contentType"`
	Kind        string `json:"kind"`
	CreatedBy   string `json:"createdBy"`
	UpdatedBy   string `json:"updatedBy"`
}

// create uploads an HTML page; title and description get defaults unless given.
func (e *env) create(content string, fields map[string]string) docResp {
	e.t.Helper()
	return e.createFile("page.html", content, fields)
}

func (e *env) createFile(filename, content string, fields map[string]string) docResp {
	e.t.Helper()
	all := map[string]string{"title": "Test", "description": "Test artifact"}
	for k, v := range fields {
		all[k] = v
	}
	body, ct := multipartFile(e.t, filename, content, all)
	rec := e.api("POST", "/api/docs", body, ct)
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var d docResp
	json.Unmarshal(rec.Body.Bytes(), &d)
	return d
}

func (e *env) login() {
	e.t.Helper()
	form := url.Values{"email": {adminEmail}, "password": {adminPassword}, "next": {"/admin"}}
	r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := e.do(r)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin" {
		e.t.Fatalf("login: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			e.session = c
		}
	}
	if e.session == nil {
		e.t.Fatal("login did not set a session cookie")
	}
}

func get(path string) *http.Request { return httptest.NewRequest("GET", path, nil) }

func TestAPIRequiresToken(t *testing.T) {
	e := newEnv(t, nil)
	if rec := e.do(get("/api/docs")); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	e.token = "ax_wrong"
	if rec := e.api("GET", "/api/docs", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad token: %d", rec.Code)
	}
	if ct := e.api("GET", "/api/docs", nil, "").Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content type = %q", ct)
	}
}

func TestCreateAndServePublic(t *testing.T) {
	e := newEnv(t, nil)
	html := "<!doctype html><title>Hi</title><script>alert(1)</script>"
	d := e.create(html, map[string]string{"title": "Custom", "visibility": "public"})
	if len(d.ID) != 12 || d.Title != "Custom" || d.Visibility != "public" || d.CreatedBy != "token:test-agent" {
		t.Fatalf("doc = %+v", d)
	}
	if d.URL != "http://artifax.test/d/"+d.ID {
		t.Errorf("url = %q", d.URL)
	}

	rec := e.do(get("/d/" + d.ID))
	if rec.Code != http.StatusOK || rec.Body.String() != html {
		t.Fatalf("serve: %d %q", rec.Code, rec.Body)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox allow-scripts") || strings.Contains(csp, "allow-same-origin") {
		t.Errorf("csp = %q", csp)
	}
	if !strings.HasPrefix(rec.Header().Get("Cache-Control"), "public") {
		t.Errorf("cache-control = %q", rec.Header().Get("Cache-Control"))
	}

	cond := get("/d/" + d.ID)
	cond.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	if rec := e.do(cond); rec.Code != http.StatusNotModified {
		t.Errorf("conditional get: %d", rec.Code)
	}
}

func TestServeArtifacts(t *testing.T) {
	e := newEnv(t, nil)
	png := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
	pub := map[string]string{"visibility": "public"}

	tests := []struct {
		filename, content string
		wantType          string
		wantKind          string
		scripts           bool // CSP lets scripts run
		sandboxed         bool
		attachment        bool
	}{
		{"page.html", "<p>hi</p><script>1</script>", "text/html; charset=utf-8", "html", true, true, false},
		{"chart.png", png, "image/png", "image", false, false, false},
		{"logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`, "image/svg+xml", "svg", false, true, false},
		{"build.log", "<script>alert(document.cookie)</script>\nERROR x", "text/plain; charset=utf-8", "text", false, true, false},
		{"feed.xml", `<?xml version="1.0"?><x:script xmlns:x="http://www.w3.org/1999/xhtml">alert(1)</x:script>`, "text/plain; charset=utf-8", "text", false, true, false},
		{"report.pdf", "%PDF-1.7\n", "application/pdf", "pdf", false, false, false},
		{"bundle.zip", "PK\x03\x04data", "application/zip", "binary", false, true, true},
		{"mystery", "\x00\x01\x02binary", "application/octet-stream", "binary", false, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			d := e.createFile(tc.filename, tc.content, pub)
			if d.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q (contentType %q)", d.Kind, tc.wantKind, d.ContentType)
			}
			rec := e.do(get("/d/" + d.ID))
			h := rec.Header()
			if rec.Code != http.StatusOK || rec.Body.String() != tc.content {
				t.Fatalf("serve: %d", rec.Code)
			}
			if h.Get("Content-Type") != tc.wantType {
				t.Errorf("content type = %q, want %q", h.Get("Content-Type"), tc.wantType)
			}
			csp := h.Get("Content-Security-Policy")
			if strings.Contains(csp, "script-src") != tc.scripts || strings.HasPrefix(csp, "sandbox") != tc.sandboxed {
				t.Errorf("csp = %q", csp)
			}
			if strings.Contains(csp, "allow-same-origin") {
				t.Error("sandbox allows same origin")
			}
			if h.Get("X-Content-Type-Options") != "nosniff" {
				t.Error("missing nosniff")
			}
			if strings.HasPrefix(h.Get("Content-Disposition"), "attachment") != tc.attachment {
				t.Errorf("disposition = %q", h.Get("Content-Disposition"))
			}

			// Explicit downloads and the raw API are always attachments named after the file.
			for _, rec := range []*httptest.ResponseRecorder{
				e.do(get("/d/" + d.ID + "?download")),
				e.api("GET", "/api/docs/"+d.ID+"/raw", nil, ""),
			} {
				if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, tc.filename) {
					t.Errorf("download disposition = %q", cd)
				}
			}
		})
	}
}

func TestDownloadNameIsEscaped(t *testing.T) {
	e := newEnv(t, nil)
	d := e.createFile("we\"ird;name.zip", "PK\x03\x04", map[string]string{"visibility": "public"})
	cd := e.do(get("/d/" + d.ID)).Header().Get("Content-Disposition")
	disp, params, err := mime.ParseMediaType(cd)
	if err != nil || disp != "attachment" || params["filename"] != `we"ird;name.zip` || strings.ContainsAny(cd, "\r\n") {
		t.Errorf("disposition %q parsed as %q %v %v", cd, disp, params, err)
	}
}

func TestMetadataRequired(t *testing.T) {
	e := newEnv(t, nil)
	for name, fields := range map[string]map[string]string{
		"no title":       {"description": "d"},
		"no description": {"title": "t"},
	} {
		body, ct := multipartFile(t, "chart.png", "\x89PNG\r\n\x1a\n", fields)
		if rec := e.api("POST", "/api/docs", body, ct); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestRawUploadDefaultsToPrivate(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.api("POST", "/api/docs?description=desc", strings.NewReader("<title>From title</title><p>x</p>"), "text/html")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var d docResp
	json.Unmarshal(rec.Body.Bytes(), &d)
	if d.Visibility != "private" || d.Title != "From title" {
		t.Fatalf("doc = %+v", d)
	}

	anon := e.do(get("/d/" + d.ID))
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/auth/login?next=%2Fd%2F"+d.ID {
		t.Fatalf("anonymous private: %d %q", anon.Code, anon.Header().Get("Location"))
	}

	e.login()
	rec = e.do(get("/d/" + d.ID))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("admin private: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestReplaceKeepsID(t *testing.T) {
	e := newEnv(t, nil)
	d := e.create("<p>v1</p>", map[string]string{"visibility": "unlisted", "title": "Doc"})

	rec := e.api("PUT", "/api/docs/"+d.ID, strings.NewReader("<p>v2</p>"), "text/html")
	if rec.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", rec.Code, rec.Body)
	}
	var d2 docResp
	json.Unmarshal(rec.Body.Bytes(), &d2)
	if d2.ID != d.ID || d2.Title != "Doc" || d2.Visibility != "unlisted" {
		t.Errorf("replaced = %+v", d2)
	}
	if body := e.do(get("/d/" + d.ID)).Body.String(); body != "<p>v2</p>" {
		t.Errorf("served %q", body)
	}
	if raw := e.api("GET", "/api/docs/"+d.ID+"/raw", nil, ""); raw.Body.String() != "<p>v2</p>" {
		t.Errorf("raw %q", raw.Body)
	}
	if rec := e.api("PUT", "/api/docs/AAAAAAAAAAAA", strings.NewReader("<p>x</p>"), "text/html"); rec.Code != http.StatusNotFound {
		t.Errorf("replace unknown: %d", rec.Code)
	}
}

func TestPatchAndDelete(t *testing.T) {
	e := newEnv(t, nil)
	d := e.create("<p>x</p>", nil)

	rec := e.api("PATCH", "/api/docs/"+d.ID, strings.NewReader(`{"visibility":"public","title":"New"}`), "application/json")
	var d2 docResp
	json.Unmarshal(rec.Body.Bytes(), &d2)
	if rec.Code != http.StatusOK || d2.Visibility != "public" || d2.Title != "New" {
		t.Fatalf("patch: %d %+v", rec.Code, d2)
	}
	for _, body := range []string{`{"visibility":"secret"}`, `{"slug":"x"}`, `not json`} {
		if rec := e.api("PATCH", "/api/docs/"+d.ID, strings.NewReader(body), "application/json"); rec.Code != http.StatusBadRequest {
			t.Errorf("patch %s: %d", body, rec.Code)
		}
	}

	if rec := e.api("DELETE", "/api/docs/"+d.ID, nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := e.api("GET", "/api/docs/"+d.ID, nil, ""); rec.Code != http.StatusNotFound {
		t.Errorf("get deleted: %d", rec.Code)
	}
	if rec := e.do(get("/d/" + d.ID)); rec.Code != http.StatusNotFound {
		t.Errorf("serve deleted: %d", rec.Code)
	}
	if rec := e.api("DELETE", "/api/docs/"+d.ID, nil, ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete twice: %d", rec.Code)
	}
}

func TestUploadValidation(t *testing.T) {
	e := newEnv(t, nil)
	tests := map[string]struct {
		body, ct string
		want     int
	}{
		"empty":        {"", "text/html", http.StatusBadRequest},
		"no metadata":  {"<p>no title</p>", "text/html", http.StatusBadRequest},
		"too large":    {"<p>" + strings.Repeat("a", 70<<10) + "</p>", "text/html", http.StatusRequestEntityTooLarge},
		"missing file": {"--x--\r\n", "multipart/form-data; boundary=x", http.StatusBadRequest},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if rec := e.api("POST", "/api/docs", strings.NewReader(tc.body), tc.ct); rec.Code != tc.want {
				t.Errorf("status %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
	if rec := e.api("POST", "/api/docs?visibility=secret", strings.NewReader("<p>x</p>"), "text/html"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad visibility: %d", rec.Code)
	}
}

func TestListPagination(t *testing.T) {
	e := newEnv(t, nil)
	for range 3 {
		e.create("<p>x</p>", nil)
	}
	var page struct {
		Items      []docResp `json:"items"`
		NextCursor *string   `json:"nextCursor"`
	}
	json.Unmarshal(e.api("GET", "/api/docs?limit=2", nil, "").Body.Bytes(), &page)
	if len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("page 1 = %d items, next %v", len(page.Items), page.NextCursor)
	}
	json.Unmarshal(e.api("GET", "/api/docs?limit=2&cursor="+*page.NextCursor, nil, "").Body.Bytes(), &page)
	if len(page.Items) != 1 || page.NextCursor != nil {
		t.Fatalf("page 2 = %d items, next %v", len(page.Items), page.NextCursor)
	}
	if rec := e.api("GET", "/api/docs?limit=500", nil, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("limit 500: %d", rec.Code)
	}
}

func TestIndexVisibility(t *testing.T) {
	e := newEnv(t, nil)
	e.create("<p>x</p>", map[string]string{"title": "PublicDoc", "visibility": "public"})
	e.create("<p>x</p>", map[string]string{"title": "UnlistedDoc", "visibility": "unlisted"})
	e.create("<p>x</p>", map[string]string{"title": "PrivateDoc", "visibility": "private"})

	body := e.do(get("/")).Body.String()
	if !strings.Contains(body, "PublicDoc") || strings.Contains(body, "UnlistedDoc") || strings.Contains(body, "PrivateDoc") {
		t.Errorf("anonymous index leaks or misses docs:\n%s", body)
	}
	if !strings.Contains(body, `href="/auth/login"`) || strings.Contains(body, `href="/admin"`) {
		t.Error("anonymous index should show login, not panel")
	}

	e.login()
	body = e.do(get("/")).Body.String()
	for _, title := range []string{"PublicDoc", "UnlistedDoc", "PrivateDoc"} {
		if !strings.Contains(body, title) {
			t.Errorf("admin index misses %s", title)
		}
	}
	if !strings.Contains(body, `href="/admin"`) {
		t.Error("admin index should link to the panel")
	}

	if body := e.do(get("/?q=Unlisted")).Body.String(); strings.Contains(body, "PublicDoc") || !strings.Contains(body, "UnlistedDoc") {
		t.Error("search did not filter")
	}
}

func TestBasicLoginAndLogout(t *testing.T) {
	e := newEnv(t, nil)
	if rec := e.do(get("/admin")); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/auth/login") {
		t.Fatalf("anonymous admin: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	form := url.Values{"email": {adminEmail}, "password": {"wrong"}}
	r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := e.do(r); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", rec.Code)
	}

	e.login()
	if rec := e.do(get("/admin")); rec.Code != http.StatusOK {
		t.Fatalf("admin with session: %d", rec.Code)
	}

	if rec := e.do(httptest.NewRequest("POST", "/auth/logout", nil)); rec.Code != http.StatusSeeOther {
		t.Fatalf("logout: %d", rec.Code)
	}
	if rec := e.do(get("/admin")); rec.Code != http.StatusSeeOther {
		t.Fatalf("session still valid after logout: %d", rec.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t, nil)
	form := url.Values{"email": {adminEmail}, "password": {"wrong"}}.Encode()
	var last int
	for range 11 {
		r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		last = e.do(r).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("11th attempt: %d", last)
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	e := newEnv(t, nil)
	e.login()
	d := e.create("<p>x</p>", nil)

	for _, hdr := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site"},
		{"Origin": "null"}, // a sandboxed document
		{"Origin": "https://evil.example"},
	} {
		r := httptest.NewRequest("POST", "/admin/docs/"+d.ID+"/delete", nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		if rec := e.do(r); rec.Code != http.StatusForbidden {
			t.Errorf("%v: %d", hdr, rec.Code)
		}
	}
	if rec := e.api("GET", "/api/docs/"+d.ID, nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("document was deleted by a cross-origin request")
	}
}

func TestAdminPanel(t *testing.T) {
	e := newEnv(t, nil)
	if rec := e.do(httptest.NewRequest("POST", "/admin/tokens", nil)); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous admin POST: %d", rec.Code)
	}
	e.login()

	body, ct := multipartBody(t, "<title>Panel upload</title><p>x</p>", map[string]string{"title": "", "description": "From the panel", "visibility": "public"})
	r := httptest.NewRequest("POST", "/admin/docs", body)
	r.Header.Set("Content-Type", ct)
	rec := e.do(r)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/admin/docs/") {
		t.Fatalf("admin upload: %d %s", rec.Code, loc)
	}
	id := strings.TrimPrefix(loc, "/admin/docs/")

	page := e.do(get(loc)).Body.String()
	if !strings.Contains(page, "Panel upload") || !strings.Contains(page, "by admin") {
		t.Errorf("doc page missing title or actor")
	}

	form := url.Values{"visibility": {"private"}, "return": {"/admin"}}
	r = httptest.NewRequest("POST", "/admin/docs/"+id, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := e.do(r); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin" {
		t.Fatalf("visibility change: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	var d docResp
	json.Unmarshal(e.api("GET", "/api/docs/"+id, nil, "").Body.Bytes(), &d)
	if d.Visibility != "private" || d.Title != "Panel upload" {
		t.Errorf("after update: %+v", d)
	}

	// Inline edits from app.js get a bare status instead of a redirect.
	for vis, want := range map[string]int{"unlisted": http.StatusNoContent, "secret": http.StatusBadRequest} {
		r = httptest.NewRequest("POST", "/admin/docs/"+id, strings.NewReader("visibility="+vis))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Artifax-Fetch", "1")
		if rec := e.do(r); rec.Code != want {
			t.Errorf("inline visibility %q: %d, want %d", vis, rec.Code, want)
		}
	}

	raw := e.do(get("/d/" + id + "?download")) // private: needs the admin session
	if !strings.HasPrefix(raw.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("raw download disposition = %q", raw.Header().Get("Content-Disposition"))
	}

	form = url.Values{"name": {"laptop"}}
	r = httptest.NewRequest("POST", "/admin/tokens", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = e.do(r)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), auth.TokenPrefix) {
		t.Fatalf("create token: %d", rec.Code)
	}

	if rec := e.do(httptest.NewRequest("POST", "/admin/docs/"+id+"/delete", nil)); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := e.api("GET", "/api/docs/"+id, nil, ""); rec.Code != http.StatusNotFound {
		t.Errorf("deleted doc still exists: %d", rec.Code)
	}
}

func TestAuthDisabled(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AuthMode = config.AuthNone })
	d := e.create("<p>x</p>", nil) // private by default

	for _, path := range []string{"/admin", "/auth/login", "/d/" + d.ID} {
		if rec := e.do(get(path)); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if body := e.do(get("/")).Body.String(); strings.Contains(body, "/auth/login") {
		t.Error("index shows a login link with auth disabled")
	}
}

func TestSSOCallbackWithoutFlow(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.AuthMode = config.AuthSSO
		c.OIDCIssuer = "http://127.0.0.1:1"
		c.OIDCClientID, c.OIDCClientSecret = "artifax", "secret"
	})
	if rec := e.do(get("/auth/callback?state=x&code=y")); rec.Code != http.StatusBadRequest {
		t.Errorf("callback without flow cookie: %d", rec.Code)
	}
	if rec := e.do(get("/auth/login")); rec.Code != http.StatusBadGateway {
		t.Errorf("login with unreachable IdP: %d", rec.Code)
	}
	if rec := e.do(httptest.NewRequest("POST", "/auth/login", nil)); rec.Code != http.StatusNotFound {
		t.Errorf("password login in sso mode: %d", rec.Code)
	}
}

func TestSkill(t *testing.T) {
	e := newEnv(t, nil)

	raw := e.do(get("/skill"))
	body := raw.Body.String()
	if raw.Code != http.StatusOK || !strings.HasPrefix(raw.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("raw skill: %d %q", raw.Code, raw.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(body, "---\nname: artifax") || !strings.Contains(body, "http://artifax.test") || strings.Contains(body, "artifax.example.com") {
		t.Errorf("raw skill does not carry this instance's URL:\n%.300s", body)
	}

	r := get("/skill")
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	page := e.do(r)
	if !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") || !strings.Contains(page.Body.String(), "curl -fsSL http://artifax.test/skill.md") {
		t.Errorf("skill page: %q", page.Header().Get("Content-Type"))
	}

	dl := e.do(get("/skill.md?download"))
	if dl.Header().Get("Content-Disposition") != `attachment; filename="SKILL.md"` {
		t.Errorf("download disposition = %q", dl.Header().Get("Content-Disposition"))
	}
}

func TestMiscRoutes(t *testing.T) {
	e := newEnv(t, nil)
	if rec := e.do(get("/healthz")); rec.Code != http.StatusOK {
		t.Errorf("healthz: %d", rec.Code)
	}
	if rec := e.do(get("/static/style.css")); rec.Code != http.StatusOK {
		t.Errorf("static: %d", rec.Code)
	}
	if rec := e.do(get("/nope")); rec.Code != http.StatusNotFound {
		t.Errorf("unknown page: %d", rec.Code)
	}
	if rec := e.do(get("/d/not-an-id")); rec.Code != http.StatusNotFound {
		t.Errorf("malformed id: %d", rec.Code)
	}
	if csp := e.do(get("/")).Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("ui csp = %q", csp)
	}
}
