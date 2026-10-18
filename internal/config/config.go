// Package config loads and validates Artifax settings from the environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// AuthMode selects how the admin logs in.
type AuthMode string

const (
	AuthNone  AuthMode = ""
	AuthBasic AuthMode = "basic"
	AuthSSO   AuthMode = "sso"
)

// Config holds every runtime setting.
type Config struct {
	BaseURL        *url.URL
	Listen         string
	DataDir        string
	MaxUploadBytes int64
	BootstrapToken string
	TrustProxy     bool

	AuthMode      AuthMode
	AdminEmail    string
	AdminPassword string

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	AdminGroups      []string

	SessionTTL time.Duration
}

// SecureCookies reports whether cookies must carry the Secure flag.
func (c *Config) SecureCookies() bool { return c.BaseURL.Scheme == "https" }

// URL resolves an absolute path against the base URL.
func (c *Config) URL(path string) string {
	return strings.TrimSuffix(c.BaseURL.String(), "/") + path
}

// Load reads the configuration using getenv (usually os.Getenv).
func Load(getenv func(string) string) (*Config, error) {
	get := func(key, fallback string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return fallback
	}

	var errs []error
	cfg := &Config{
		Listen:           get("ARTIFAX_LISTEN", ":8080"),
		DataDir:          get("ARTIFAX_DATA_DIR", "/data"),
		BootstrapToken:   get("ARTIFAX_BOOTSTRAP_TOKEN", ""),
		AuthMode:         AuthMode(strings.ToLower(get("AUTH_MODE", ""))),
		AdminEmail:       get("ADMIN_EMAIL", ""),
		AdminPassword:    getenv("ADMIN_PASSWORD"),
		OIDCIssuer:       get("OIDC_ISSUER", ""),
		OIDCClientID:     get("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: get("OIDC_CLIENT_SECRET", ""),
	}

	base, err := parseBaseURL(get("ARTIFAX_BASE_URL", ""))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.BaseURL = base

	maxMB, err := strconv.Atoi(get("ARTIFAX_MAX_UPLOAD_MB", "25"))
	if err != nil || maxMB < 1 {
		errs = append(errs, errors.New("ARTIFAX_MAX_UPLOAD_MB must be a positive integer"))
	}
	cfg.MaxUploadBytes = int64(maxMB) << 20

	if cfg.TrustProxy, err = strconv.ParseBool(get("ARTIFAX_TRUST_PROXY", "false")); err != nil {
		errs = append(errs, errors.New("ARTIFAX_TRUST_PROXY must be true or false"))
	}

	if cfg.SessionTTL, err = time.ParseDuration(get("SESSION_TTL", "168h")); err != nil || cfg.SessionTTL <= 0 {
		errs = append(errs, errors.New("SESSION_TTL must be a positive duration such as 168h"))
	}

	if t := cfg.BootstrapToken; t != "" && (!strings.HasPrefix(t, "ax_") || len(t) < 24) {
		errs = append(errs, errors.New("ARTIFAX_BOOTSTRAP_TOKEN must start with ax_ and be at least 24 characters"))
	}

	for _, g := range strings.Split(get("ADMIN_GROUPS", "admins"), ",") {
		if g = strings.TrimSpace(g); g != "" {
			cfg.AdminGroups = append(cfg.AdminGroups, g)
		}
	}

	switch cfg.AuthMode {
	case AuthNone:
	case AuthBasic:
		if cfg.AdminEmail == "" {
			errs = append(errs, errors.New("ADMIN_EMAIL is required when AUTH_MODE=basic"))
		}
		if len(cfg.AdminPassword) < 8 {
			errs = append(errs, errors.New("ADMIN_PASSWORD must be at least 8 characters when AUTH_MODE=basic"))
		}
	case AuthSSO:
		if cfg.OIDCIssuer == "" || cfg.OIDCClientID == "" || cfg.OIDCClientSecret == "" {
			errs = append(errs, errors.New("OIDC_ISSUER, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET are required when AUTH_MODE=sso"))
		}
		if len(cfg.AdminGroups) == 0 {
			errs = append(errs, errors.New("ADMIN_GROUPS must list at least one group when AUTH_MODE=sso"))
		}
	default:
		errs = append(errs, fmt.Errorf("AUTH_MODE must be empty, basic or sso (got %q)", cfg.AuthMode))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("ARTIFAX_BASE_URL is required, e.g. https://artifax.example.com")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("ARTIFAX_BASE_URL must be an absolute http(s) URL (got %q)", raw)
	}
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("ARTIFAX_BASE_URL must not contain a path, query or fragment")
	}
	u.Path = ""
	return u, nil
}
