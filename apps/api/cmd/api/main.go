// Command api is the Viro control-plane HTTP server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/config"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/httpx"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/version"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

	st, err := buildStore(context.Background(), cfg, logger)
	if err != nil {
		logger.Error("init store", "err", err)
		os.Exit(1)
	}

	srv := httpx.NewServer(cfg, logger, st)
	httpServer := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      srv.Router(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Meter hourly compute cost for all orgs at the live admin price list.
	srv.StartMetering(ctx, time.Hour)

	go func() {
		logger.Info("vortex-api starting",
			"addr", cfg.HTTPAddr, "env", cfg.Env, "version", version.Version)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	logger.Info("vortex-api shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
	}
}

// buildStore selects the control-plane store: Postgres when VORTEX_DATABASE_URL is
// set (migrated + seeded on boot), otherwise the in-memory store for local dev/tests.
func buildStore(ctx context.Context, cfg *config.Config, logger *slog.Logger) (store.Store, error) {
	if cfg.DatabaseURL == "" {
		logger.Info("store: in-memory (set VORTEX_DATABASE_URL for Postgres persistence)")
		return store.NewMemoryStore(), nil
	}
	logger.Info("store: postgres")
	pg, err := store.NewPostgresStore(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := pg.Migrate(ctx); err != nil {
		return nil, err
	}
	if err := pg.Seed(ctx); err != nil {
		return nil, err
	}
	return pg, nil
}
