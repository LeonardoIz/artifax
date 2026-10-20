package server

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LeonardoIz/artifax/internal/auth"
	"github.com/LeonardoIz/artifax/internal/docs"
	"github.com/LeonardoIz/artifax/internal/media"
	"github.com/LeonardoIz/artifax/internal/store"
)

const adminActor = "admin"

// adminData backs the admin page: artifacts and API tokens side by side.
type adminData struct {
	Docs       []store.Doc
	Query      string
	Visibility string
	Pages      pager
	Return     string
	Count      int
	Bytes      int64
	MaxMB      int64
	Tokens     []store.Token
	NewSecret  string
	Expiries   []tokenExpiry
	BaseURL    string
}

type tokenExpiry struct {
	Value, Label string
	TTL          time.Duration // 0: never
	Default      bool
}

const day = 24 * time.Hour

// tokenExpiries are the lifetimes offered when creating a token.
var tokenExpiries = []tokenExpiry{
	{Value: "7d", Label: "7 days", TTL: 7 * day},
	{Value: "30d", Label: "30 days", TTL: 30 * day},
	{Value: "90d", Label: "90 days", TTL: 90 * day, Default: true},
	{Value: "1y", Label: "1 year", TTL: 365 * day},
	{Value: "never", Label: "Never"},
}

type adminDocData struct {
	Doc      *store.Doc
	URL      string
	Kind     media.Kind
	Text     string // preview of text artifacts
	TextMore bool   // the preview is truncated
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request, title string, data any) page {
	p := s.page(sessionFrom(r.Context()), title, data)
	p.Section = "admin"
	p.Flash, p.Error = s.takeFlash(w, r)
	return p
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := store.ListParams{Query: q.Get("q"), Cursor: q.Get("cursor"), Limit: 50}
	vis := store.Visibility(q.Get("visibility"))
	if vis.Valid() {
		p.Visibilities = []store.Visibility{vis}
	} else {
		vis = ""
	}
	list, next, err := s.docs.List(r.Context(), p)
	if errors.Is(err, store.ErrBadCursor) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	count, size, err := s.docs.Stats(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tokens, err := s.tokens.List(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, http.StatusOK, "admin", s.adminPage(w, r, "Admin", adminData{
		Docs:       list,
		Query:      p.Query,
		Visibility: string(vis),
		Pages:      pageLinks("/admin", q, next),
		Return:     r.URL.RequestURI(),
		Count:      count,
		Bytes:      size,
		MaxMB:      s.docs.MaxBytes() >> 20,
		Tokens:     tokens,
		NewSecret:  s.takeNewToken(w, r),
		Expiries:   tokenExpiries,
		BaseURL:    s.cfg.URL(""),
	}))
}

func (s *Server) handleAdminUpload(w http.ResponseWriter, r *http.Request) {
	up, done, err := s.readUpload(w, r)
	defer done()
	if err == nil {
		blankToNil(&up.Fields)
		var d *store.Doc
		if d, err = s.docs.Create(r.Context(), up, adminActor); err == nil {
			s.redirectFlash(w, r, "/admin/docs/"+d.ID, "msg", "Artifact uploaded.")
			return
		}
	}
	s.adminFail(w, r, "/admin", err)
}

func (s *Server) handleAdminDoc(w http.ResponseWriter, r *http.Request) {
	d, err := s.docs.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, docs.ErrNotFound) {
		s.handleNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	title := d.Title
	if title == "" {
		title = d.ID
	}
	s.render(w, http.StatusOK, "admin_doc", s.adminPage(w, r, title, s.docPreview(d)))
}

const previewBytes = 64 << 10

// docPreview builds the detail page data, including the start of text artifacts.
// The text is HTML-escaped by the template, so any content is safe to show.
func (s *Server) docPreview(d *store.Doc) adminDocData {
	data := adminDocData{Doc: d, URL: s.docURL(d.ID), Kind: media.Classify(d.ContentType)}
	if data.Kind != media.Text {
		return data
	}
	f, err := s.docs.Open(d)
	if err != nil {
		s.log.Warn("preview unavailable", "id", d.ID, "err", err)
		return data
	}
	defer f.Close()
	buf := make([]byte, previewBytes+1)
	n, _ := io.ReadFull(f, buf)
	data.TextMore = n > previewBytes
	b := buf[:min(n, previewBytes)]
	for i := 0; i < utf8.UTFMax-1 && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1] // don't split a multi-byte character at the cut
	}
	data.Text = strings.ToValidUTF8(string(b), "�")
	return data
}

func (s *Server) handleAdminUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f := docs.Fields{
		Title:       optionalValue(r.PostForm, "title"),
		Description: optionalValue(r.PostForm, "description"),
	}
	if v := optionalValue(r.PostForm, "visibility"); v != nil {
		vis := store.Visibility(*v)
		f.Visibility = &vis
	}
	back := r.PostForm.Get("return")
	if back == "" {
		back = "/admin/docs/" + id
	}
	_, err := s.docs.Update(r.Context(), id, f, adminActor)
	if r.Header.Get("X-Artifax-Fetch") == "1" {
		// Inline edits from app.js: answer without a redirect or page render.
		var verr *docs.ValidationError
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.As(err, &verr):
			http.Error(w, upperFirst(verr.Msg)+".", http.StatusBadRequest)
		case errors.Is(err, docs.ErrNotFound):
			http.Error(w, "Artifact not found.", http.StatusNotFound)
		default:
			s.serverError(w, r, err)
		}
		return
	}
	if err != nil {
		s.adminFail(w, r, safeNext(back), err)
		return
	}
	s.redirectFlash(w, r, safeNext(back), "msg", "Saved.")
}

func (s *Server) handleAdminReplace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	back := "/admin/docs/" + id
	up, done, err := s.readUpload(w, r)
	defer done()
	if err == nil {
		up.Fields = docs.Fields{} // content only; metadata is edited separately
		if _, err = s.docs.Replace(r.Context(), id, up, adminActor); err == nil {
			s.redirectFlash(w, r, back, "msg", "Content replaced.")
			return
		}
	}
	s.adminFail(w, r, back, err)
}

func (s *Server) handleAdminDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.docs.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.adminFail(w, r, "/admin", err)
		return
	}
	s.redirectFlash(w, r, "/admin", "msg", "Artifact deleted.")
}

// handleAdminTokens keeps old links working: tokens now live on the admin page.
func (s *Server) handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin#tokens", http.StatusMovedPermanently)
}

func (s *Server) handleAdminCreateToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	exp, ok := findExpiry(r.PostForm.Get("expires"))
	if !ok {
		s.redirectFlash(w, r, "/admin#tokens", "err", "Choose when the token expires.")
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	_, secret, err := s.tokens.Create(r.Context(), name, exp.TTL)
	switch {
	case errors.Is(err, auth.ErrTokenName):
		s.redirectFlash(w, r, "/admin#tokens", "err", "Token name must be 1-100 characters.")
	case errors.Is(err, store.ErrNameTaken):
		s.redirectFlash(w, r, "/admin#tokens", "err", "A token named “"+name+"” already exists.")
	case err != nil:
		s.serverError(w, r, err)
	default:
		// Post/redirect/get, so reloading cannot create a second token. The secret
		// rides in a short-lived cookie read once by the admin page, never in a URL.
		http.SetCookie(w, &http.Cookie{
			Name:     newTokenCookie,
			Value:    secret,
			Path:     "/admin",
			MaxAge:   60,
			HttpOnly: true,
			Secure:   s.cfg.SecureCookies(),
			SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/admin#tokens", http.StatusSeeOther)
	}
}

const newTokenCookie = "artifax_new_token"

// takeNewToken returns and clears the secret of a token that was just created.
func (s *Server) takeNewToken(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(newTokenCookie)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: newTokenCookie, Path: "/admin", MaxAge: -1, HttpOnly: true,
		Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteStrictMode})
	if !strings.HasPrefix(c.Value, auth.TokenPrefix) {
		return ""
	}
	return c.Value
}

func findExpiry(value string) (tokenExpiry, bool) {
	for _, e := range tokenExpiries {
		if e.Value == value {
			return e, true
		}
	}
	return tokenExpiry{}, false
}

func (s *Server) handleAdminRevokeToken(w http.ResponseWriter, r *http.Request) {
	if err := s.tokens.Revoke(r.Context(), r.PathValue("id")); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	msg := "Token revoked."
	if r.PostFormValue("expired") != "" {
		msg = "Token removed."
	}
	s.redirectFlash(w, r, "/admin#tokens", "msg", msg)
}

// adminFail redirects back with a readable error, or fails hard for unexpected errors.
func (s *Server) adminFail(w http.ResponseWriter, r *http.Request, back string, err error) {
	var verr *docs.ValidationError
	switch {
	case errors.As(err, &verr):
		s.redirectFlash(w, r, back, "err", upperFirst(verr.Msg)+".")
	case errors.Is(err, docs.ErrTooLarge):
		s.redirectFlash(w, r, back, "err", "The file is larger than the upload limit.")
	case errors.Is(err, docs.ErrNotFound):
		s.redirectFlash(w, r, "/admin", "err", "Artifact not found.")
	default:
		s.serverError(w, r, err)
	}
}

const flashCookie = "artifax_flash"

// redirectFlash redirects and shows msg once on the next admin page. kind is "msg" or "err".
func (s *Server) redirectFlash(w http.ResponseWriter, r *http.Request, target, kind, msg string) {
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookie,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(kind + "\n" + msg)),
		Path:     "/",
		MaxAge:   60,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// takeFlash reads and clears the pending flash message.
func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) (msg, errMsg string) {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return "", ""
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode})
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return "", ""
	}
	kind, text, _ := strings.Cut(string(raw), "\n")
	if kind == "err" {
		return "", text
	}
	return text, ""
}

// blankToNil treats empty form fields as "not provided" so defaults still apply.
func blankToNil(f *docs.Fields) {
	if f.Title != nil && strings.TrimSpace(*f.Title) == "" {
		f.Title = nil
	}
	if f.Description != nil && strings.TrimSpace(*f.Description) == "" {
		f.Description = nil
	}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
