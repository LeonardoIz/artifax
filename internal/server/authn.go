package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/LeonardoIz/artifax/internal/auth"
)

const oidcCookie = "artifax_oidc"

type loginData struct {
	Next  string
	Email string
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		s.handleNotFound(w, r)
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	if s.currentSession(r) != nil {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if s.oidc != nil {
		s.beginOIDC(w, r, next)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, http.StatusOK, "login", s.page(nil, "Log in", loginData{Next: next}))
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if s.basic == nil {
		s.handleNotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := r.PostForm.Get("email")
	data := loginData{Next: safeNext(r.PostForm.Get("next")), Email: email}
	fail := func(status int, msg string) {
		p := s.page(nil, "Log in", data)
		p.Error = msg
		s.render(w, status, "login", p)
	}

	ip := s.clientIP(r)
	if !s.limiter.Allowed(ip) {
		fail(http.StatusTooManyRequests, "Too many failed attempts. Try again later.")
		return
	}
	if !s.basic.Check(email, r.PostForm.Get("password")) {
		s.limiter.Fail(ip)
		s.log.Warn("login failed", "ip", ip)
		fail(http.StatusUnauthorized, "Wrong email or password.")
		return
	}
	s.limiter.Reset(ip)
	if err := s.sessions.Start(r.Context(), w, "basic:"+email, email); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("login", "mode", "basic", "ip", ip)
	http.Redirect(w, r, data.Next, http.StatusSeeOther)
}

func (s *Server) beginOIDC(w http.ResponseWriter, r *http.Request, next string) {
	target, flow, err := s.oidc.Begin(next)
	if err != nil {
		s.log.Error("oidc unavailable", "err", err)
		s.render(w, http.StatusBadGateway, "message", s.page(nil, "Login unavailable", message{
			Heading: "Login unavailable",
			Text:    "The identity provider could not be reached. Try again in a moment.",
			LinkURL: "/", LinkText: "Back to documents",
		}))
		return
	}
	raw, _ := json.Marshal(flow)
	http.SetCookie(w, &http.Cookie{
		Name:     oidcCookie,
		Value:    base64.RawURLEncoding.EncodeToString(raw),
		Path:     "/auth/",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		s.handleNotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	retry := message{LinkURL: "/auth/login", LinkText: "Try again"}

	flow, ok := readFlow(r)
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Path: "/auth/", MaxAge: -1, HttpOnly: true,
		Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode})
	if !ok {
		retry.Heading, retry.Text = "Login expired", "The login took too long or was started elsewhere."
		s.render(w, http.StatusBadRequest, "message", s.page(nil, "Login expired", retry))
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.log.Warn("oidc login rejected by provider", "error", e, "description", q.Get("error_description"))
		retry.Heading, retry.Text = "Login failed", "The identity provider did not complete the login."
		s.render(w, http.StatusUnauthorized, "message", s.page(nil, "Login failed", retry))
		return
	}

	id, err := s.oidc.Finish(r.Context(), flow, q.Get("state"), q.Get("code"))
	if err != nil {
		s.log.Warn("oidc login failed", "err", err)
		retry.Heading, retry.Text = "Login failed", "The login could not be verified."
		s.render(w, http.StatusUnauthorized, "message", s.page(nil, "Login failed", retry))
		return
	}
	if !s.oidc.IsAdmin(id) {
		s.log.Warn("oidc login without admin group", "subject", id.Subject, "email", id.Email)
		s.render(w, http.StatusForbidden, "message", s.page(nil, "No access", message{
			Heading: "No access",
			Text:    "Your account is not allowed to manage this Artifax instance.",
			LinkURL: "/", LinkText: "Back to documents",
		}))
		return
	}
	if err := s.sessions.Start(r.Context(), w, "oidc:"+id.Subject, id.Email); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("login", "mode", "sso", "subject", id.Subject)
	http.Redirect(w, r, safeNext(flow.Next), http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.End(w, r); err != nil {
		s.log.Error("logout failed", "err", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func readFlow(r *http.Request) (auth.Flow, bool) {
	var f auth.Flow
	c, err := r.Cookie(oidcCookie)
	if err != nil {
		return f, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || json.Unmarshal(raw, &f) != nil || f.State == "" {
		return f, false
	}
	return f, true
}
