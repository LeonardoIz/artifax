package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/LeonardoIz/artifax/internal/ids"
)

// Identity is the user returned by the identity provider.
type Identity struct {
	Subject string
	Email   string
	Name    string
	Groups  []string
}

// OIDC is an OpenID Connect client (e.g. Authelia). Provider discovery is lazy so
// the app can start while the identity provider is still booting.
type OIDC struct {
	issuer       string
	clientID     string
	clientSecret string
	redirectURL  string
	adminGroups  []string
	client       *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

// NewOIDC returns an OIDC client. redirectURL must be <base>/auth/callback.
func NewOIDC(issuer, clientID, clientSecret, redirectURL string, adminGroups []string) *OIDC {
	return &OIDC{
		issuer:       issuer,
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		adminGroups:  adminGroups,
		client:       &http.Client{Timeout: 15 * time.Second},
	}
}

func (o *OIDC) setup() (*oidc.Provider, *oidc.IDTokenVerifier, *oauth2.Config, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return o.provider, o.verifier, o.oauth, nil
	}
	// The provider keeps this context for key set refreshes, so it must not be cancelled.
	ctx := oidc.ClientContext(context.Background(), o.client)
	p, err := oidc.NewProvider(ctx, o.issuer)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("oidc discovery: %w", err)
	}
	o.provider = p
	o.verifier = p.Verifier(&oidc.Config{ClientID: o.clientID})
	o.oauth = &oauth2.Config{
		ClientID:     o.clientID,
		ClientSecret: o.clientSecret,
		RedirectURL:  o.redirectURL,
		Endpoint:     p.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}
	return o.provider, o.verifier, o.oauth, nil
}

// Flow is the per-login state kept in a short-lived cookie between Begin and Finish.
type Flow struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"r"`
}

// Begin starts a login and returns the provider URL to redirect to.
func (o *OIDC) Begin(next string) (string, Flow, error) {
	_, _, cfg, err := o.setup()
	if err != nil {
		return "", Flow{}, err
	}
	f := Flow{
		State:    ids.Secret(24),
		Nonce:    ids.Secret(24),
		Verifier: oauth2.GenerateVerifier(),
		Next:     next,
	}
	url := cfg.AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier))
	return url, f, nil
}

// Finish validates the callback parameters against the flow and returns the identity.
func (o *OIDC) Finish(ctx context.Context, f Flow, state, code string) (*Identity, error) {
	if f.State == "" || subtle.ConstantTimeCompare([]byte(f.State), []byte(state)) != 1 {
		return nil, errors.New("oidc: state mismatch")
	}
	if code == "" {
		return nil, errors.New("oidc: missing code")
	}
	return o.exchange(ctx, code, f.Verifier, f.Nonce)
}

type claims struct {
	Subject           string      `json:"sub"`
	Email             string      `json:"email"`
	Name              string      `json:"name"`
	PreferredUsername string      `json:"preferred_username"`
	Nonce             string      `json:"nonce"`
	Groups            stringOrSet `json:"groups"`
}

// exchange trades the authorization code for a verified identity.
func (o *OIDC) exchange(ctx context.Context, code, verifier, nonce string) (*Identity, error) {
	provider, idVerifier, cfg, err := o.setup()
	if err != nil {
		return nil, err
	}
	ctx = oidc.ClientContext(ctx, o.client)
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("oidc code exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("oidc: no id_token in token response")
	}
	idToken, err := idVerifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("oidc: verify id_token: %w", err)
	}
	var c claims
	if err := idToken.Claims(&c); err != nil {
		return nil, fmt.Errorf("oidc: parse claims: %w", err)
	}
	if c.Nonce != nonce {
		return nil, errors.New("oidc: nonce mismatch")
	}

	// Recent Authelia versions only put groups and email in the userinfo response.
	if len(c.Groups) == 0 || c.Email == "" {
		info, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(tok))
		if err != nil {
			return nil, fmt.Errorf("oidc userinfo: %w", err)
		}
		var extra claims
		if err := info.Claims(&extra); err != nil {
			return nil, fmt.Errorf("oidc: parse userinfo: %w", err)
		}
		if extra.Subject != idToken.Subject {
			return nil, errors.New("oidc: userinfo subject mismatch")
		}
		if len(c.Groups) == 0 {
			c.Groups = extra.Groups
		}
		if c.Email == "" {
			c.Email = extra.Email
		}
		if c.Name == "" {
			c.Name = extra.Name
		}
	}

	name := c.Name
	if name == "" {
		name = c.PreferredUsername
	}
	return &Identity{Subject: idToken.Subject, Email: c.Email, Name: name, Groups: c.Groups}, nil
}

// IsAdmin reports whether the identity belongs to one of the admin groups.
func (o *OIDC) IsAdmin(id *Identity) bool {
	return slices.ContainsFunc(id.Groups, func(g string) bool {
		return slices.Contains(o.adminGroups, g)
	})
}

// stringOrSet accepts a JSON string or array of strings.
type stringOrSet []string

func (s *stringOrSet) UnmarshalJSON(b []byte) error {
	var list []string
	if err := json.Unmarshal(b, &list); err == nil {
		*s = list
		return nil
	}
	var one string
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*s = []string{one}
	return nil
}
