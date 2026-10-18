package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"ARTIFAX_BASE_URL": "https://artifax.example.com/"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.URL("/d/x"); got != "https://artifax.example.com/d/x" {
		t.Errorf("URL = %q", got)
	}
	if cfg.Listen != ":8080" || cfg.DataDir != "/data" {
		t.Errorf("listen/data = %q %q", cfg.Listen, cfg.DataDir)
	}
	if cfg.MaxUploadBytes != 10<<20 {
		t.Errorf("max upload = %d", cfg.MaxUploadBytes)
	}
	if cfg.SessionTTL != 168*time.Hour {
		t.Errorf("session ttl = %v", cfg.SessionTTL)
	}
	if cfg.AuthMode != AuthNone || !cfg.SecureCookies() {
		t.Errorf("auth=%q secure=%v", cfg.AuthMode, cfg.SecureCookies())
	}
	if len(cfg.AdminGroups) != 1 || cfg.AdminGroups[0] != "admins" {
		t.Errorf("admin groups = %v", cfg.AdminGroups)
	}
}

func TestLoadSSO(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"ARTIFAX_BASE_URL":   "http://localhost:8080",
		"AUTH_MODE":          "SSO",
		"OIDC_ISSUER":        "https://auth.example.com",
		"OIDC_CLIENT_ID":     "artifax",
		"OIDC_CLIENT_SECRET": "secret",
		"ADMIN_GROUPS":       "admins, owners ,",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthMode != AuthSSO {
		t.Errorf("auth mode = %q", cfg.AuthMode)
	}
	if strings.Join(cfg.AdminGroups, "|") != "admins|owners" {
		t.Errorf("admin groups = %v", cfg.AdminGroups)
	}
	if cfg.SecureCookies() {
		t.Error("http base URL must not use secure cookies")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		want string
	}{
		"missing base url": {map[string]string{}, "ARTIFAX_BASE_URL is required"},
		"base url path":    {map[string]string{"ARTIFAX_BASE_URL": "https://x.com/sub"}, "must not contain a path"},
		"bad scheme":       {map[string]string{"ARTIFAX_BASE_URL": "ftp://x.com"}, "absolute http(s) URL"},
		"bad auth mode":    {map[string]string{"ARTIFAX_BASE_URL": "https://x.com", "AUTH_MODE": "ldap"}, "AUTH_MODE must be"},
		"basic missing": {
			map[string]string{"ARTIFAX_BASE_URL": "https://x.com", "AUTH_MODE": "basic", "ADMIN_PASSWORD": "short"},
			"ADMIN_EMAIL is required",
		},
		"basic weak password": {
			map[string]string{"ARTIFAX_BASE_URL": "https://x.com", "AUTH_MODE": "basic", "ADMIN_EMAIL": "a@b.c", "ADMIN_PASSWORD": "short"},
			"at least 12 characters",
		},
		"sso missing": {map[string]string{"ARTIFAX_BASE_URL": "https://x.com", "AUTH_MODE": "sso"}, "OIDC_ISSUER"},
		"bad bootstrap": {
			map[string]string{"ARTIFAX_BASE_URL": "https://x.com", "ARTIFAX_BOOTSTRAP_TOKEN": "nope"},
			"ARTIFAX_BOOTSTRAP_TOKEN",
		},
		"bad upload size": {map[string]string{"ARTIFAX_BASE_URL": "https://x.com", "ARTIFAX_MAX_UPLOAD_MB": "0"}, "ARTIFAX_MAX_UPLOAD_MB"},
		"bad ttl":         {map[string]string{"ARTIFAX_BASE_URL": "https://x.com", "SESSION_TTL": "forever"}, "SESSION_TTL"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(env(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
