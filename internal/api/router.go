package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/OpenCHAMI/kea-sync/internal/auth"
	"github.com/OpenCHAMI/kea-sync/internal/config"
	"github.com/OpenCHAMI/kea-sync/internal/service"
	"github.com/OpenCHAMI/kea-sync/internal/version"
)

func NewRouter(cfg config.Config, controller *service.Controller, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	readAuth, _ := auth.NewJWKSAuth(cfg.Auth.JWKSURL, cfg.Auth.JWKSRefreshInterval, cfg.Auth.ReadScopes)
	writeAuth, _ := auth.NewJWKSAuth(cfg.Auth.JWKSURL, cfg.Auth.JWKSRefreshInterval, cfg.Auth.WriteScopes)

	mux.Handle("/", logRequests(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "kea-sync",
			"version": version.Version,
		})
	})))
	mux.Handle("/liveness", logRequests(logger, method(http.MethodGet, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))))
	mux.Handle("/readiness", logRequests(logger, method(http.MethodGet, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := controller.Ready(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))))
	mux.Handle("/health", logRequests(logger, method(http.MethodGet, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		health := controller.Health(ctx)
		statusCode := http.StatusOK
		if health.Status != "ok" {
			statusCode = http.StatusServiceUnavailable
		}
		writeJSON(w, statusCode, health)
	}))))
	mux.Handle("/metrics", logRequests(logger, method(http.MethodGet, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(controller.Metrics().Text()))
	}))))
	mux.Handle("/v1/diff", logRequests(logger, readAuth.Wrap(method(http.MethodGet, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plan, err := controller.Preview(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": err.Error(),
				"plan":  plan,
			})
			return
		}
		writeJSON(w, http.StatusOK, plan)
	})))))
	mux.Handle("/v1/sync", logRequests(logger, writeAuth.Wrap(method(http.MethodPost, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plan, err := controller.Sync(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": err.Error(),
				"plan":  plan,
			})
			return
		}
		writeJSON(w, http.StatusOK, plan)
	})))))

	logger.Debug("router initialized", "auth_enabled", cfg.Auth.Enabled())
	return mux
}

func method(want string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != want {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("request complete", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}
