package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/LeonardoIz/artifax/internal/docs"
	"github.com/LeonardoIz/artifax/internal/store"
)

const adminActor = "admin"

type adminDocsData struct {
	Docs       []store.Doc
	Query      string
	Visibility string
	NextURL    string
	Return     string
	Count      int
	Bytes      int64
}

type adminDocData struct {
	Doc *store.Doc
	URL string
}

type adminTokensData struct {
	Tokens    []store.Token
	NewSecret string
	BaseURL   string
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request, section, title string, data any) page {
	p := s.page(sessionFrom(r.Context()), title, data)
	p.Section = section
	p.Flash, p.Error = s.takeFlash(w, r)
	return p
}

func (s *Server) handleAdminDocs(w http.ResponseWriter, r *http.Request) {
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
	s.render(w, http.StatusOK, "admin_docs", s.adminPage(w, r, "docs", "Panel", adminDocsData{
		Docs:       list,
		Query:      p.Query,
		Visibility: string(vis),
		NextURL:    nextURL("/admin", q, next),
		Return:     r.URL.RequestURI(),
		Count:      count,
		Bytes:      size,
	}))
}

func (s *Server) handleAdminUpload(w http.ResponseWriter, r *http.Request) {
	up, err := s.readUpload(w, r)
	if err == nil {
		blankToNil(&up.Fields)
		var d *store.Doc
		if d, err = s.docs.Create(r.Context(), up, adminActor); err == nil {
			s.redirectFlash(w, r, "/admin/docs/"+d.ID, "msg", "Document uploaded.")
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
	s.render(w, http.StatusOK, "admin_doc", s.adminPage(w, r, "docs", title, adminDocData{Doc: d, URL: s.docURL(d.ID)}))
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
	if _, err := s.docs.Update(r.Context(), id, f, adminActor); err != nil {
		s.adminFail(w, r, safeNext(back), err)
		return
	}
	s.redirectFlash(w, r, safeNext(back), "msg", "Saved.")
}

func (s *Server) handleAdminReplace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	back := "/admin/docs/" + id
	up, err := s.readUpload(w, r)
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
	s.redirectFlash(w, r, "/admin", "msg", "Document deleted.")
}

func (s *Server) handleAdminRaw(w http.ResponseWriter, r *http.Request) {
	d, err := s.docs.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, docs.ErrNotFound) {
		s.handleNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.serveContent(w, r, d, true)
}

func (s *Server) handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	s.renderTokens(w, r, "")
}

func (s *Server) handleAdminCreateToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_, secret, err := s.tokens.Create(r.Context(), r.PostForm.Get("name"))
	if err != nil {
		s.redirectFlash(w, r, "/admin/tokens", "err", "Token name must be 1-100 characters.")
		return
	}
	// Rendered directly (not redirected) so the secret never lands in a URL or history.
	s.renderTokens(w, r, secret)
}

func (s *Server) handleAdminRevokeToken(w http.ResponseWriter, r *http.Request) {
	if err := s.tokens.Revoke(r.Context(), r.PathValue("id")); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	s.redirectFlash(w, r, "/admin/tokens", "msg", "Token revoked.")
}

func (s *Server) renderTokens(w http.ResponseWriter, r *http.Request, secret string) {
	list, err := s.tokens.List(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, http.StatusOK, "admin_tokens", s.adminPage(w, r, "tokens", "Tokens", adminTokensData{
		Tokens:    list,
		NewSecret: secret,
		BaseURL:   s.cfg.URL(""),
	}))
}

// adminFail redirects back with a readable error, or fails hard for unexpected errors.
func (s *Server) adminFail(w http.ResponseWriter, r *http.Request, back string, err error) {
	var verr *docs.ValidationError
	switch {
	case errors.As(err, &verr):
		s.redirectFlash(w, r, back, "err", upperFirst(verr.Msg)+".")
	case errors.Is(err, errTooLarge):
		s.redirectFlash(w, r, back, "err", "The file is larger than the upload limit.")
	case errors.Is(err, docs.ErrNotFound):
		s.redirectFlash(w, r, "/admin", "err", "Document not found.")
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
