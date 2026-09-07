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

	_ "github.com/reijiokito/jigsaw-backend/docs" // generated swagger docs
	"github.com/reijiokito/jigsaw-backend/internal/api"
	"github.com/reijiokito/jigsaw-backend/internal/auth"
	"github.com/reijiokito/jigsaw-backend/internal/buildinfo"
	"github.com/reijiokito/jigsaw-backend/internal/config"
	"github.com/reijiokito/jigsaw-backend/internal/database"
	"github.com/reijiokito/jigsaw-backend/internal/logx"
	"github.com/reijiokito/jigsaw-backend/internal/metrics"
	"github.com/reijiokito/jigsaw-backend/internal/purchases"
	"github.com/reijiokito/jigsaw-backend/internal/repository"
	"github.com/reijiokito/jigsaw-backend/internal/storage"
)

// serviceName labels every log record and is the Prometheus job name.
const serviceName = "jigsaw-backend"

// @title           Jigsaw Puzzle API
// @version         1.0
// @description     Backend for the Jigsaw Puzzle app: email/password auth, image
// @description     collection, admin daily challenges (Cloudinary) and per-day
// @description     reputation ("Danh vọng").
// @BasePath        /
//
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Type "Bearer" followed by a space and the session token issued by /api/v1/auth/login or /register.
func main() {
	// Logging is configured before anything else so even config errors are
	// emitted in the same structured format as the rest of the run.
	config.LoadDotEnv(".env")
	level, format := config.LogSettings()
	logx.Init(level, format, serviceName, buildinfo.Version)

	slog.Info("starting server",
		slog.String("version", buildinfo.Version),
		slog.String("commit", buildinfo.Commit),
		slog.String("go_version", buildinfo.GoVersion()),
	)
	if err := run(); err != nil {
		slog.Error("fatal startup error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", slog.Any("error", err))
		return err
	}
	slog.Info("config loaded", slog.String("env", cfg.AppEnv))

	metrics.SetBuildInfo(buildinfo.Version, buildinfo.Commit, buildinfo.GoVersion(), cfg.AppEnv)

	ctx := context.Background()

	pool, err := database.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		slog.Error("failed to connect to database", slog.Any("error", err))
		return err
	}
	defer pool.Close()
	slog.Info("database connected")

	// Pool saturation is the usual root cause of latency spikes here, so the
	// pool is scraped alongside the HTTP metrics.
	if err := metrics.Register(metrics.NewPoolCollector(pool)); err != nil {
		slog.Warn("failed to register database pool metrics", slog.Any("error", err))
	}

	// if err := database.Migrate(ctx, pool); err != nil {
	// 	return err
	// }
	// slog.Info("migrations applied")

	store := repository.New(pool)

	cloud, err := storage.New(cfg)
	if err != nil {
		slog.Error("failed to initialize cloudinary client", slog.Any("error", err))
		return err
	}
	slog.Info("cloudinary client initialized")

	jwtManager := auth.NewJWTManager(cfg.JWTSecret, cfg.JWTTTL)

	// Store credentials are optional: platforms without them are simply not
	// verifiable, so their purchases are rejected instead of blocking startup.
	verifiers := purchases.NewVerifiers(ctx, cfg)
	slog.Info("in-app-purchase verifiers configured", slog.Int("count", len(verifiers)))

	server := api.NewServer(cfg, store, cloud, jwtManager, verifiers)
	slog.Info("router built",
		slog.Bool("metrics_enabled", cfg.MetricsEnabled),
		slog.String("metrics_path", cfg.MetricsPath),
	)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown.
	errCh := make(chan error, 1)
	go func() {
		slog.Info("server listening", slog.String("addr", httpServer.Addr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", slog.Any("error", err))
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		slog.Info("shutdown signal received", slog.String("signal", sig.String()))
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("error during graceful shutdown", slog.Any("error", err))
			return err
		}
		slog.Info("server stopped cleanly")
		return nil
	}
}
