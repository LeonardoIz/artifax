// Command artifax runs the Artifax HTML publishing server.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/LeonardoIz/artifax/internal/auth"
	"github.com/LeonardoIz/artifax/internal/blobs"
	"github.com/LeonardoIz/artifax/internal/config"
	"github.com/LeonardoIz/artifax/internal/docs"
	"github.com/LeonardoIz/artifax/internal/server"
	"github.com/LeonardoIz/artifax/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version":
			fmt.Println(version)
			return
		case "healthcheck":
			// Used by the container HEALTHCHECK; the distroless image has no curl.
			if err := healthcheck(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("config:\n%w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := checkDataDir(cfg.DataDir); err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "artifax.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	bl, err := blobs.New(filepath.Join(cfg.DataDir, "blobs"))
	if err != nil {
		return err
	}

	tokens := auth.NewTokens(st)
	if seeded, err := tokens.SeedBootstrap(ctx, cfg.BootstrapToken); err != nil {
		return fmt.Errorf("seed bootstrap token: %w", err)
	} else if seeded {
		log.Info("bootstrap token stored")
	}
	sessions := auth.NewSessions(st, cfg.SessionTTL, cfg.SecureCookies())

	srv, err := server.New(server.Deps{
		Config:   cfg,
		Store:    st,
		Docs:     docs.NewService(st, bl, cfg.MaxUploadBytes, log),
		Tokens:   tokens,
		Sessions: sessions,
		Log:      log,
		Version:  version,
	})
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}

	go purgeSessions(ctx, sessions, log)

	errc := make(chan error, 1)
	go func() {
		log.Info("artifax listening", "addr", cfg.Listen, "url", cfg.URL(""), "auth", authLabel(cfg.AuthMode), "version", version)
		if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func purgeSessions(ctx context.Context, sessions *auth.Sessions, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := sessions.Purge(ctx); err != nil {
				log.Warn("session purge failed", "err", err)
			}
		}
	}
}

// checkDataDir creates the data directory and verifies it is writable, with a hint for
// the usual cause: a bind mount owned by root while the container runs as non-root.
func checkDataDir(dir string) error {
	err := os.MkdirAll(dir, 0o750)
	if err == nil {
		var f *os.File
		if f, err = os.CreateTemp(dir, ".write-test-*"); err == nil {
			f.Close()
			os.Remove(f.Name())
			return nil
		}
	}
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("data directory %s is not writable by uid %d. "+
			"If it is a bind mount, run on the host: chown -R %d:%d <host path>: %w",
			dir, os.Getuid(), os.Getuid(), os.Getgid(), err)
	}
	return fmt.Errorf("data directory %s: %w", dir, err)
}

// healthcheck queries /healthz on the local listen address.
func healthcheck() error {
	listen := os.Getenv("ARTIFAX_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("invalid ARTIFAX_LISTEN %q: %w", listen, err)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}

func authLabel(m config.AuthMode) string {
	if m == config.AuthNone {
		return "disabled"
	}
	return string(m)
}
