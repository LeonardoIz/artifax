package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
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
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "page.html")
	fw.Write([]byte(content))
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

type docResp struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	Visibility string `json:"visibility"`
	CreatedBy  string `json:"createdBy"`
	UpdatedBy  string `json:"updatedBy"`
}

func (e *env) create(content string, fields map[string]string) docResp {
	e.t.Helper()
	body, ct := multipartBody(e.t, content, fields)
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
		"plain text":   {"hello", "text/html", http.StatusBadRequest},
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

	body, ct := multipartBody(t, "<title>Panel upload</title><p>x</p>", map[string]string{"title": "", "visibility": "public"})
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

	raw := e.do(get("/admin/docs/" + id + "/raw"))
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

func TestMiscRoutes(t *testing.T) {
	e := newEnv(t, nil)
	if rec := e.do(get("/healthz")); rec.Code != http.StatusOK {
		t.Errorf("healthz: %d", rec.Code)
	}
	rec := e.do(get("/openapi.json"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"url": "http://artifax.test"`) {
		t.Errorf("openapi: %d", rec.Code)
	}
	if !json.Valid(rec.Body.Bytes()) {
		t.Error("openapi.json is not valid JSON")
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
