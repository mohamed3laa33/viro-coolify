// Command api is the Viro control-plane HTTP server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
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
		WriteTimeout: writeTimeout(cfg),
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Track the background loops so we can wait for them to drain before closing
	// the store (otherwise a mid-pass query could race the pool's Close()).
	var bg sync.WaitGroup

	// Meter hourly compute cost for all orgs at the live admin price list.
	srv.StartMetering(ctx, time.Hour, &bg)

	// Reconcile stored workload status against the live deploy backend.
	srv.StartReconciler(ctx, time.Duration(cfg.ReconcileSec)*time.Second, &bg)

	// GC expired/revoked refresh tokens so the table does not grow without bound.
	srv.StartTokenCleanup(ctx, time.Hour, &bg)

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
	// Stop the background loops (ctx was cancelled by signal) and WAIT for them to
	// exit before releasing store resources, so no metering/reconcile pass is still
	// querying the pool when it closes.
	stop()
	bg.Wait()
	// Drain any in-flight async git builds before releasing store resources, so a
	// build worker never writes to a pool that is about to close.
	srv.WaitBuilds()
	// Release store resources (e.g. the pgx connection pool) after the HTTP
	// server has drained and the background loops have exited.
	st.Close()
}

// writeTimeoutBuffer is added on top of the helm Apply deadline so a synchronous
// deploy handler can finish writing its response after helm returns.
const writeTimeoutBuffer = 60 * time.Second

// minWriteTimeout floors the server WriteTimeout so it is never shorter than the
// previous fixed value even with a tiny configured helm timeout.
const minWriteTimeout = 30 * time.Second

// writeTimeout derives the http.Server WriteTimeout. Deploys currently run
// `helm upgrade --install --wait --atomic --timeout <HelmTimeout>` SYNCHRONOUSLY
// inside the HTTP handler, so the response is only written after helm returns;
// the write deadline must therefore exceed the helm timeout (plus a buffer) or a
// real deploy is cut off mid-flight. The eventual design is a fully-async deploy
// (return 202 and run the apply in the background), which would let this drop
// back to a short, fixed write timeout.
func writeTimeout(cfg *config.Config) time.Duration {
	helmTimeout := time.Duration(cfg.HelmTimeoutSec) * time.Second
	if helmTimeout < 0 {
		helmTimeout = 0
	}
	wt := helmTimeout + writeTimeoutBuffer
	if wt < minWriteTimeout {
		wt = minWriteTimeout
	}
	return wt
}

// buildStore selects the control-plane store: Postgres when VORTEX_DATABASE_URL is
// set (migrated + seeded on boot), otherwise the in-memory store for local dev/tests.
func buildStore(ctx context.Context, cfg *config.Config, logger *slog.Logger) (store.Store, error) {
	if cfg.DatabaseURL == "" {
		logger.Info("store: in-memory (set VORTEX_DATABASE_URL for Postgres persistence)")
		return store.NewMemoryStore(), nil
	}
	logger.Info("store: postgres")
	pg, err := store.NewPostgresStore(ctx, cfg.DatabaseURL, cfg.DBMaxConns, cfg.DBMinConns)
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
