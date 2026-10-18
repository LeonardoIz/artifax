package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/LeonardoIz/artifax/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestTokens(t *testing.T) {
	ctx := context.Background()
	tokens := NewTokens(openStore(t))

	tok, secret, err := tokens.Create(ctx, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := tokens.Verify(ctx, secret); err != nil || got.ID != tok.ID {
		t.Fatalf("verify = %v %v", got, err)
	}
	if _, err := tokens.Verify(ctx, secret+"x"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("wrong secret = %v", err)
	}
	if _, _, err := tokens.Create(ctx, "  "); err == nil {
		t.Error("blank name accepted")
	}
	if err := tokens.Revoke(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Verify(ctx, secret); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("revoked token = %v", err)
	}
}

func TestSeedBootstrapOnlyOnce(t *testing.T) {
	ctx := context.Background()
	tokens := NewTokens(openStore(t))
	secret := "ax_bootstrap_secret_value_123"

	if ok, err := tokens.SeedBootstrap(ctx, secret); !ok || err != nil {
		t.Fatalf("first seed = %v %v", ok, err)
	}
	if ok, _ := tokens.SeedBootstrap(ctx, "ax_another_secret_value_4567"); ok {
		t.Fatal("seeded twice")
	}
	if _, err := tokens.Verify(ctx, secret); err != nil {
		t.Fatalf("bootstrap token rejected: %v", err)
	}
}

func TestBasicCheck(t *testing.T) {
	b := NewBasic("Admin@Example.com", "correct horse battery")
	if !b.Check(" admin@example.com", "correct horse battery") {
		t.Error("valid credentials rejected")
	}
	if b.Check("admin@example.com", "wrong") || b.Check("other@example.com", "correct horse battery") {
		t.Error("invalid credentials accepted")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Now()
	l := NewLimiter(2, time.Minute)
	l.now = func() time.Time { return now }
	l.Fail("ip")
	l.Fail("ip")
	if l.Allowed("ip") {
		t.Fatal("allowed after max failures")
	}
	if !l.Allowed("other") {
		t.Fatal("other key blocked")
	}
	now = now.Add(2 * time.Minute)
	if !l.Allowed("ip") {
		t.Fatal("still blocked after window")
	}
}

// fakeIdP is a minimal OpenID provider: discovery, JWKS, token and userinfo endpoints.
type fakeIdP struct {
	*httptest.Server
	key       *rsa.PrivateKey
	nonce     string
	challenge string
	groups    []string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIdP{key: key}
	mux := http.NewServeMux()
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.URL,
			"authorization_endpoint":                idp.URL + "/authorize",
			"token_endpoint":                        idp.URL + "/token",
			"userinfo_endpoint":                     idp.URL + "/userinfo",
			"jwks_uri":                              idp.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != idp.challenge || r.PostForm.Get("code") != "good-code" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		// Like recent Authelia: no groups in the ID token, only in userinfo.
		idToken := idp.sign(t, map[string]any{
			"iss": idp.URL, "sub": "user-1", "aud": "artifax", "nonce": idp.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access", "token_type": "Bearer", "expires_in": 3600, "id_token": idToken,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"sub": "user-1", "email": "me@example.com", "name": "Me", "groups": idp.groups,
		})
	})
	return idp
}

func (idp *fakeIdP) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: idp.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(claims)
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	out, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOIDCFlow(t *testing.T) {
	idp := newFakeIdP(t)
	o := NewOIDC(idp.URL, "artifax", "secret", "https://artifax.test/auth/callback", []string{"admins"})

	begin := func() Flow {
		target, flow, err := o.Begin("/admin")
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(target)
		q := u.Query()
		if q.Get("state") != flow.State || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != "https://artifax.test/auth/callback" {
			t.Fatalf("unexpected auth URL %s", target)
		}
		idp.nonce, idp.challenge = q.Get("nonce"), q.Get("code_challenge")
		return flow
	}

	t.Run("admin", func(t *testing.T) {
		idp.groups = []string{"users", "admins"}
		flow := begin()
		id, err := o.Finish(context.Background(), flow, flow.State, "good-code")
		if err != nil {
			t.Fatal(err)
		}
		if id.Subject != "user-1" || id.Email != "me@example.com" || !o.IsAdmin(id) {
			t.Fatalf("identity = %+v admin=%v", id, o.IsAdmin(id))
		}
	})

	t.Run("not admin", func(t *testing.T) {
		idp.groups = []string{"users"}
		flow := begin()
		id, err := o.Finish(context.Background(), flow, flow.State, "good-code")
		if err != nil {
			t.Fatal(err)
		}
		if o.IsAdmin(id) {
			t.Fatal("user without admin group treated as admin")
		}
	})

	t.Run("state mismatch", func(t *testing.T) {
		flow := begin()
		if _, err := o.Finish(context.Background(), flow, "forged", "good-code"); err == nil {
			t.Fatal("forged state accepted")
		}
	})

	t.Run("nonce mismatch", func(t *testing.T) {
		flow := begin()
		idp.nonce = "replayed"
		if _, err := o.Finish(context.Background(), flow, flow.State, "good-code"); err == nil {
			t.Fatal("wrong nonce accepted")
		}
	})
}
