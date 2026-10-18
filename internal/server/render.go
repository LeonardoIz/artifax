package server

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/LeonardoIz/artifax/internal/store"
)

//go:embed ui
var uiFS embed.FS

type views struct {
	pages map[string]*template.Template
}

// page is the data every template receives.
type page struct {
	Title       string
	Admin       bool
	AuthEnabled bool
	Section     string
	Flash       string
	Error       string
	Data        any
}

type message struct {
	Heading  string
	Text     string
	LinkURL  string
	LinkText string
}

func (s *Server) page(sess *store.Session, title string, data any) page {
	return page{Title: title, Admin: sess != nil, AuthEnabled: s.authEnabled(), Data: data}
}

func loadViews() (*views, error) {
	funcs := template.FuncMap{
		"date":         formatDate,
		"iso":          formatISO,
		"bytes":        formatBytes,
		"visibilities": func() []store.Visibility { return store.Visibilities },
	}
	files, err := fs.Glob(uiFS, "ui/templates/pages/*.html")
	if err != nil {
		return nil, err
	}
	v := &views{pages: map[string]*template.Template{}}
	for _, f := range files {
		t, err := template.New("layout").Funcs(funcs).ParseFS(uiFS, "ui/templates/layout.html", f)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		v.pages[strings.TrimSuffix(path.Base(f), ".html")] = t
	}
	return v, nil
}

func (s *Server) render(w http.ResponseWriter, status int, name string, p page) {
	t, ok := s.views.pages[name]
	if !ok {
		s.log.Error("unknown template", "name", name)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", p); err != nil {
		s.log.Error("render failed", "template", name, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func asTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case *time.Time:
		if t != nil {
			return *t, true
		}
	}
	return time.Time{}, false
}

func formatDate(v any) string {
	t, ok := asTime(v)
	if !ok {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}

func formatISO(v any) string {
	t, ok := asTime(v)
	if !ok {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
