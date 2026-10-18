package server

import (
	"bytes"
	_ "embed"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/LeonardoIz/artifax/internal/docs"
	"github.com/LeonardoIz/artifax/internal/store"
)

//go:embed openapi.json
var openapiSpec []byte

type indexData struct {
	Docs    []store.Doc
	Query   string
	NextURL string
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	q := r.URL.Query()
	p := store.ListParams{Query: q.Get("q"), Cursor: q.Get("cursor"), Limit: 30}
	if sess == nil {
		p.Visibilities = []store.Visibility{store.Public}
	}
	list, next, err := s.docs.List(r.Context(), p)
	if errors.Is(err, store.ErrBadCursor) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if sess != nil {
		w.Header().Set("Cache-Control", "no-store")
	}
	s.render(w, http.StatusOK, "index", s.page(sess, "", indexData{
		Docs:    list,
		Query:   p.Query,
		NextURL: nextURL("/", q, next),
	}))
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	d, err := s.docs.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, docs.ErrNotFound) {
		s.handleNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	h := w.Header()
	switch d.Visibility {
	case store.Private:
		if s.currentSession(r) == nil {
			if s.authEnabled() {
				http.Redirect(w, r, loginURL(r.URL.RequestURI()), http.StatusSeeOther)
			} else {
				s.handleNotFound(w, r)
			}
			return
		}
		h.Set("Cache-Control", "private, no-store")
		h.Set("X-Robots-Tag", "noindex")
	case store.Unlisted:
		h.Set("Cache-Control", "public, max-age=60, must-revalidate")
		h.Set("X-Robots-Tag", "noindex")
	default:
		h.Set("Cache-Control", "public, max-age=60, must-revalidate")
	}
	s.serveContent(w, r, d, false)
}

// serveContent streams a document under the sandboxing CSP.
func (s *Server) serveContent(w http.ResponseWriter, r *http.Request, d *store.Doc, download bool) {
	f, err := s.docs.Open(d)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer f.Close()

	h := w.Header()
	h.Set("Content-Security-Policy", docCSP)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("ETag", `"`+d.BlobSHA256+`"`)
	h.Set("Referrer-Policy", "no-referrer")
	if download {
		h.Set("Content-Disposition", `attachment; filename="`+d.ID+`.html"`)
	}
	http.ServeContent(w, r, "", d.UpdatedAt, f)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := s.store.Ping(r.Context()); err != nil {
		s.log.Error("health check failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(bytes.ReplaceAll(openapiSpec, []byte("{{BASE_URL}}"), []byte(s.cfg.URL(""))))
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeProblem(w, http.StatusNotFound, "Not Found", "unknown API route")
		return
	}
	s.render(w, http.StatusNotFound, "message", s.page(s.currentSession(r), "Not found", message{
		Heading: "Not found",
		Text:    "There is nothing at this address.",
	}))
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// nextURL builds the "older" pagination link, keeping the current filters.
func nextURL(path string, q url.Values, cursor string) string {
	if cursor == "" {
		return ""
	}
	v := url.Values{}
	for _, k := range []string{"q", "visibility"} {
		if q.Get(k) != "" {
			v.Set(k, q.Get(k))
		}
	}
	v.Set("cursor", cursor)
	return path + "?" + v.Encode()
}
