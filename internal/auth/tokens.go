// Package auth handles API tokens, admin sessions and admin login (basic or OIDC).
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeonardoIz/artifax/internal/ids"
	"github.com/LeonardoIz/artifax/internal/store"
)

// TokenPrefix starts every API token secret.
const TokenPrefix = "ax_"

// ErrInvalidToken is returned for unknown, revoked or malformed tokens.
var ErrInvalidToken = errors.New("invalid token")

// Tokens issues and verifies API tokens.
type Tokens struct {
	store *store.Store
	now   func() time.Time
}

// NewTokens returns a token manager.
func NewTokens(st *store.Store) *Tokens {
	return &Tokens{store: st, now: time.Now}
}

// Create issues a new token and returns it together with its one-time secret.
func (t *Tokens) Create(ctx context.Context, name string) (*store.Token, string, error) {
	secret := TokenPrefix + ids.Secret(32)
	tok, err := t.insert(ctx, name, secret)
	return tok, secret, err
}

// Verify returns the active token matching secret and records its use.
func (t *Tokens) Verify(ctx context.Context, secret string) (*store.Token, error) {
	if !strings.HasPrefix(secret, TokenPrefix) {
		return nil, ErrInvalidToken
	}
	tok, err := t.store.TokenByHash(ctx, ids.Hash(secret))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	// Throttle writes: one last-used update per minute is plenty.
	now := t.now()
	if tok.LastUsedAt == nil || now.Sub(*tok.LastUsedAt) > time.Minute {
		if err := t.store.TouchToken(ctx, tok.ID, now); err != nil {
			return nil, err
		}
	}
	return tok, nil
}

// List returns active tokens.
func (t *Tokens) List(ctx context.Context) ([]store.Token, error) {
	return t.store.ActiveTokens(ctx)
}

// Revoke disables a token.
func (t *Tokens) Revoke(ctx context.Context, id string) error {
	return t.store.RevokeToken(ctx, id, t.now())
}

// SeedBootstrap stores secret as the "bootstrap" token if no token was ever created.
// It reports whether the token was seeded.
func (t *Tokens) SeedBootstrap(ctx context.Context, secret string) (bool, error) {
	if secret == "" {
		return false, nil
	}
	n, err := t.store.CountTokens(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	if _, err := t.insert(ctx, "bootstrap", secret); err != nil {
		return false, err
	}
	return true, nil
}

func (t *Tokens) insert(ctx context.Context, name, secret string) (*store.Token, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, fmt.Errorf("token name must be 1-100 characters")
	}
	tok := &store.Token{
		ID:         ids.New(16),
		Name:       name,
		Prefix:     secret[:len(TokenPrefix)+6],
		SecretHash: ids.Hash(secret),
		CreatedAt:  t.now(),
	}
	if err := t.store.InsertToken(ctx, tok); err != nil {
		return nil, err
	}
	return tok, nil
}
