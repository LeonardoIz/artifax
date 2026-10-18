package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/LeonardoIz/artifax/internal/ids"
	"github.com/LeonardoIz/artifax/internal/store"
)

// SessionCookie is the name of the admin session cookie.
const SessionCookie = "artifax_session"

// Sessions manages server-side admin sessions. Only admins ever get a session.
type Sessions struct {
	store  *store.Store
	ttl    time.Duration
	secure bool
	now    func() time.Time
}

// NewSessions returns a session manager.
func NewSessions(st *store.Store, ttl time.Duration, secure bool) *Sessions {
	return &Sessions{store: st, ttl: ttl, secure: secure, now: time.Now}
}

// Start creates a session and sets its cookie.
func (m *Sessions) Start(ctx context.Context, w http.ResponseWriter, subject, email string) error {
	secret := ids.Secret(32)
	now := m.now()
	sess := &store.Session{
		ID:        ids.New(16),
		TokenHash: ids.Hash(secret),
		Subject:   subject,
		Email:     email,
		CreatedAt: now,
		ExpiresAt: now.Add(m.ttl),
	}
	if err := m.store.InsertSession(ctx, sess); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    secret,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// FromRequest returns the request's session, or nil if there is none.
func (m *Sessions) FromRequest(r *http.Request) (*store.Session, error) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	sess, err := m.store.SessionByHash(r.Context(), ids.Hash(c.Value), m.now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return sess, err
}

// End deletes the request's session and clears its cookie.
func (m *Sessions) End(w http.ResponseWriter, r *http.Request) error {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	return m.store.DeleteSessionByHash(r.Context(), ids.Hash(c.Value))
}

// Purge removes expired sessions.
func (m *Sessions) Purge(ctx context.Context) (int64, error) {
	return m.store.DeleteExpiredSessions(ctx, m.now())
}
