package server

import (
	_ "embed"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/LeonardoIz/artifax/internal/docs"
	"github.com/LeonardoIz/artifax/internal/media"
	"github.com/LeonardoIz/artifax/internal/store"
)

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
	s.serveContent(w, r, d, r.URL.Query().Has("download"))
}

// serveContent streams an artifact. For viewing, media.Serve decides the content type
// and CSP so only allowlisted kinds render, and never with access to the admin origin.
// Downloads keep the original type but are always sent as attachments.
func (s *Server) serveContent(w http.ResponseWriter, r *http.Request, d *store.Doc, download bool) {
	f, err := s.docs.Open(d)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer f.Close()

	resp := media.Serve(d.ContentType)
	h := w.Header()
	h.Set("ETag", `"`+d.BlobSHA256+`"`)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Del("X-Frame-Options") // frame-ancestors in the CSP covers it; PDFs have no CSP
	if download || resp.Download {
		h.Set("Content-Type", downloadType(d.ContentType))
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": downloadName(d)}))
	} else {
		h.Set("Content-Type", resp.ContentType)
		if resp.CSP != "" {
			h.Set("Content-Security-Policy", resp.CSP)
		} else {
			h.Del("Content-Security-Policy")
			h.Set("X-Frame-Options", "DENY")
		}
	}
	http.ServeContent(w, r, "", d.UpdatedAt, f)
}

// downloadType keeps the stored type for attachments, adding a charset to text.
func downloadType(ct string) string {
	switch media.Classify(ct) {
	case media.HTML, media.Text:
		return ct + "; charset=utf-8"
	}
	return ct
}

// downloadName is the original file name, or the id when there is none.
func downloadName(d *store.Doc) string {
	if d.SourceFilename != "" {
		return d.SourceFilename
	}
	return d.ID
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
