package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
	"sync"
	"time"
)

// Basic checks the single admin's email and password from the environment.
type Basic struct {
	emailHash    [32]byte
	passwordHash [32]byte
}

// NewBasic returns a checker for the given credentials.
func NewBasic(email, password string) *Basic {
	return &Basic{
		emailHash:    sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email)))),
		passwordHash: sha256.Sum256([]byte(password)),
	}
}

// Check reports whether the credentials match, in constant time.
func (b *Basic) Check(email, password string) bool {
	e := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	p := sha256.Sum256([]byte(password))
	okEmail := subtle.ConstantTimeCompare(e[:], b.emailHash[:])
	okPass := subtle.ConstantTimeCompare(p[:], b.passwordHash[:])
	return okEmail&okPass == 1
}

// Limiter is a fixed-window failure counter keyed by client address.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]*limitEntry
}

type limitEntry struct {
	failures int
	reset    time.Time
}

// NewLimiter allows max failures per key within window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, entries: map[string]*limitEntry{}}
}

// Allowed reports whether key may attempt a login.
func (l *Limiter) Allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok || l.now().After(e.reset) {
		return true
	}
	return e.failures < l.max
}

// Fail records a failed attempt.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.entries) > 10_000 {
		for k, e := range l.entries {
			if now.After(e.reset) {
				delete(l.entries, k)
			}
		}
	}
	e, ok := l.entries[key]
	if !ok || now.After(e.reset) {
		e = &limitEntry{reset: now.Add(l.window)}
		l.entries[key] = e
	}
	e.failures++
}

// Reset forgets failures for key after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
