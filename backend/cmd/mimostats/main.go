// Command mimostats runs the probe daemon and serves the public dashboard.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/trick77/mimostats/internal/config"
	"github.com/trick77/mimostats/internal/httpapi"
	"github.com/trick77/mimostats/internal/probe"
	"github.com/trick77/mimostats/internal/retention"
	"github.com/trick77/mimostats/internal/samples"
	"github.com/trick77/mimostats/internal/scheduler"
	"github.com/trick77/mimostats/internal/store"
	"github.com/trick77/mimostats/internal/version"
	"github.com/trick77/mimostats/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		// Logging is not configured yet, so this goes out on the default
		// handler. A config failure must still be legible.
		return err
	}
	setupLogging(cfg.LogLevel)

	slog.Info("starting mimostats",
		"version", version.Version,
		"origin", cfg.Origin,
		"base_url", cfg.BaseURL,
		"db", cfg.DBPath,
	)

	// The container mounts /data as a volume; on a fresh host the directory may
	// exist but a nested path may not. Create it rather than failing to open.
	if dir := filepath.Dir(cfg.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		return err
	}

	static, err := web.Handler()
	if err != nil {
		return err
	}

	sampleStore := samples.New(db)
	sched := scheduler.New(scheduler.Deps{
		Store: sampleStore,
		Prober: probe.NewClient(probe.Config{
			BaseURL:       cfg.BaseURL,
			APIKey:        cfg.APIKey,
			UserAgent:     cfg.ProbeUserAgent,
			SystemPrompt:  cfg.ProbeSystemPrompt,
			DialTimeout:   cfg.DialTimeout,
			HeaderTimeout: cfg.HeaderTimeout,
			TTFTTimeout:   cfg.TTFTTimeout,
			IdleTimeout:   cfg.IdleTimeout,
			Timeout:       cfg.ProbeTimeout,
		}),
		Pinger:     probe.NewPinger(cfg.PingTimeout),
		Origin:     cfg.Origin,
		Models:     cfg.Models,
		MimoHost:   cfg.MimoHost,
		RefSGPHost: cfg.RefSGPHost,
		RefEUHost:  cfg.RefEUHost,
	})
	sweeper := retention.New(sampleStore, cfg.Retention)

	handler := httpapi.New(httpapi.Deps{
		Version: version.Version,
		DB:      db,
		Static:  static,
	})

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
		// ReadHeaderTimeout bounds a slowloris client on a public endpoint.
		// WriteTimeout is deliberately NOT set: /api/events is a long-lived SSE
		// stream and any write deadline would sever it mid-connection.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The probe loop and the sweeper share the signal context, so SIGTERM stops
	// them at the same moment it stops accepting requests. Waited on below via
	// the WaitGroup: a cycle mid-flight must finish its write or abandon it
	// cleanly, never be killed between the INSERT and the COMMIT.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); sched.Run(ctx) }()
	go func() { defer wg.Done(); sweeper.Run(ctx) }()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	wg.Wait()
	return err
}

func setupLogging(level string) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}
