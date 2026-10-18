package server

import (
	"context"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/LeonardoIz/artifax/internal/store"
)

type ctxKey int

const (
	actorKey ctxKey = iota
	sessionKey
)

// statusWriter records the response status for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		if r.URL.Path == "/healthz" && sw.status == http.StatusOK {
			return
		}
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"duration", time.Since(start).Round(time.Microsecond),
			"ip", s.clientIP(r),
		)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panic", "path", r.URL.Path, "panic", v)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// uiCSP applies to every Artifax page. Artifacts override it (see media.Serve).
// img-src and media-src 'self' allow artifact previews in the admin panel.
const uiCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"media-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

func baseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", uiCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func staticHandler(fsys fs.FS) http.Handler {
	files := http.StripPrefix("/static/", http.FileServerFS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Has("v") { // fingerprinted by assetURL: the URL changes with the content
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		files.ServeHTTP(w, r)
	})
}

// clientIP returns the caller address, honouring X-Forwarded-For only behind a trusted proxy.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			// The right-most entry is the one appended by our proxy.
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// session returns the admin session attached by s.admin or s.optionalSession.
func sessionFrom(ctx context.Context) *store.Session {
	sess, _ := ctx.Value(sessionKey).(*store.Session)
	return sess
}

// currentSession looks up the admin session for a public page. Errors count as logged out.
func (s *Server) currentSession(r *http.Request) *store.Session {
	if !s.authEnabled() {
		return nil
	}
	sess, err := s.sessions.FromRequest(r)
	if err != nil {
		s.log.Error("session lookup failed", "err", err)
		return nil
	}
	return sess
}

// admin requires an admin session. Unauthenticated GETs are sent to the login page.
func (s *Server) admin(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authEnabled() {
			s.handleNotFound(w, r)
			return
		}
		sess := s.currentSession(r)
		if sess == nil {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, loginURL(r.URL.RequestURI()), http.StatusSeeOther)
				return
			}
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))
	})
}

// api requires a valid bearer token and records its name as the actor.
func (s *Server) api(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="artifax"`)
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "missing bearer token")
			return
		}
		tok, err := s.tokens.Verify(r.Context(), strings.TrimSpace(raw))
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="artifax", error="invalid_token"`)
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "invalid or revoked token")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), actorKey, "token:"+tok.Name)))
	})
}

func actorFrom(ctx context.Context) string {
	a, _ := ctx.Value(actorKey).(string)
	return a
}

func loginURL(next string) string {
	return "/auth/login?next=" + url.QueryEscape(next)
}

// safeNext only allows local absolute paths as post-login redirect targets.
func safeNext(next string) string {
	if next == "" || next[0] != '/' || strings.HasPrefix(next, "//") || strings.Contains(next, `\`) {
		return "/"
	}
	return next
}
