// Package server wires the HTTP routes: public index and documents, admin panel, auth and API.
package server

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/LeonardoIz/artifax/internal/auth"
	"github.com/LeonardoIz/artifax/internal/config"
	"github.com/LeonardoIz/artifax/internal/docs"
	"github.com/LeonardoIz/artifax/internal/store"
)

// Server holds the HTTP handlers and their dependencies.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	docs     *docs.Service
	tokens   *auth.Tokens
	sessions *auth.Sessions
	basic    *auth.Basic // set when AUTH_MODE=basic
	oidc     *auth.OIDC  // set when AUTH_MODE=sso
	views    *views
	log      *slog.Logger
	version  string
}

// Deps are the collaborators a Server needs.
type Deps struct {
	Config   *config.Config
	Store    *store.Store
	Docs     *docs.Service
	Tokens   *auth.Tokens
	Sessions *auth.Sessions
	Log      *slog.Logger
	Version  string
}

// New builds a Server.
func New(d Deps) (*Server, error) {
	v, err := loadViews()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:      d.Config,
		store:    d.Store,
		docs:     d.Docs,
		tokens:   d.Tokens,
		sessions: d.Sessions,
		views:    v,
		log:      d.Log,
		version:  d.Version,
	}
	switch d.Config.AuthMode {
	case config.AuthBasic:
		s.basic = auth.NewBasic(d.Config.AdminEmail, d.Config.AdminPassword)
	case config.AuthSSO:
		s.oidc = auth.NewOIDC(d.Config.OIDCIssuer, d.Config.OIDCClientID, d.Config.OIDCClientSecret,
			d.Config.URL("/auth/callback"), d.Config.AdminGroups)
	}
	return s, nil
}

func (s *Server) authEnabled() bool { return s.cfg.AuthMode != config.AuthNone }

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /d/{id}", s.handleDoc)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /skill", s.handleSkill)
	mux.HandleFunc("GET /skill.md", s.handleSkillRaw)
	static, _ := fs.Sub(uiFS, "ui/static")
	mux.Handle("GET /static/", staticHandler(static))

	// Auth.
	mux.HandleFunc("GET /auth/login", s.handleLoginPage)
	mux.HandleFunc("POST /auth/login", s.handleLoginSubmit)
	mux.HandleFunc("GET /auth/callback", s.handleCallback)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)

	// Admin.
	mux.Handle("GET /admin", s.admin(s.handleAdmin))
	mux.Handle("POST /admin/docs", s.admin(s.handleAdminUpload))
	mux.Handle("GET /admin/docs/{id}", s.admin(s.handleAdminDoc))
	mux.Handle("POST /admin/docs/{id}", s.admin(s.handleAdminUpdate))
	mux.Handle("POST /admin/docs/{id}/content", s.admin(s.handleAdminReplace))
	mux.Handle("POST /admin/docs/{id}/delete", s.admin(s.handleAdminDelete))
	mux.Handle("GET /admin/tokens", s.admin(s.handleAdminTokens))
	mux.Handle("POST /admin/tokens", s.admin(s.handleAdminCreateToken))
	mux.Handle("POST /admin/tokens/{id}/revoke", s.admin(s.handleAdminRevokeToken))

	// API.
	mux.Handle("POST /api/docs", s.api(s.apiCreate))
	mux.Handle("GET /api/docs", s.api(s.apiList))
	mux.Handle("GET /api/docs/{id}", s.api(s.apiGet))
	mux.Handle("PUT /api/docs/{id}", s.api(s.apiReplace))
	mux.Handle("PATCH /api/docs/{id}", s.api(s.apiUpdate))
	mux.Handle("DELETE /api/docs/{id}", s.api(s.apiDelete))
	mux.Handle("GET /api/docs/{id}/raw", s.api(s.apiRaw))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusNotFound, "Not Found", "unknown API route")
	})

	mux.HandleFunc("/", s.handleNotFound)

	csrf := http.NewCrossOriginProtection()
	_ = csrf.AddTrustedOrigin(s.cfg.URL(""))

	var h http.Handler = mux
	h = csrf.Handler(h)
	h = baseHeaders(h)
	h = s.recoverer(h)
	h = s.logRequests(h)
	return h
}
