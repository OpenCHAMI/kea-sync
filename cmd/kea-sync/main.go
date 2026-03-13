package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/OpenCHAMI/kea-sync/internal/api"
	"github.com/OpenCHAMI/kea-sync/internal/auth"
	"github.com/OpenCHAMI/kea-sync/internal/config"
	"github.com/OpenCHAMI/kea-sync/internal/httpjson"
	"github.com/OpenCHAMI/kea-sync/internal/kea"
	"github.com/OpenCHAMI/kea-sync/internal/metrics"
	"github.com/OpenCHAMI/kea-sync/internal/service"
	"github.com/OpenCHAMI/kea-sync/internal/smd"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.LogLevel)

	smdClient := smd.NewClient(httpjson.NewClient(httpjson.Config{
		BaseURL:      cfg.SMD.BaseURL,
		Timeout:      cfg.SMD.Timeout,
		Retries:      cfg.SMD.Retries,
		AuthProvider: auth.NewAuthorizationProvider(cfg.Tokensmith, cfg.SMD),
	}))
	keaClient := kea.NewClient(httpjson.NewClient(httpjson.Config{
		BaseURL:      cfg.Kea.BaseURL,
		Timeout:      cfg.Kea.Timeout,
		Retries:      cfg.Kea.Retries,
		AuthProvider: auth.NewAuthorizationProvider(cfg.Tokensmith, cfg.Kea.HTTPService),
	}), firstService(cfg.Kea.ControlAgentServices))

	controller := service.NewController(cfg, logger, smdClient, keaClient, metrics.NewCollector())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	controller.StartBackground(ctx)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.NewRouter(cfg, controller, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("starting kea-sync", "addr", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server exited with error", "error", err)
			stop()
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTPShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}
}

func firstService(services []string) string {
	if len(services) == 0 {
		return "dhcp4"
	}
	return services[0]
}

func newLogger(level string) *slog.Logger {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slogLevel,
	}))
}
