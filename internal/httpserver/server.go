package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
	"github.com/Mightyfin/wallet-ledger/internal/config"
	"github.com/Mightyfin/wallet-ledger/internal/financial"
)

type readiness interface{ Ping(context.Context) error }

func New(cfg config.Config, logger *slog.Logger, database readiness, service *financial.Service, verifier auth.Verifier) *http.Server {
	mux := http.NewServeMux()
	(&api{financial: service, environment: cfg.Environment}).routes(mux)
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "wallet-ledger-api", "environment": cfg.Environment})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "service": "wallet-ledger-api", "environment": cfg.Environment})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	})
	baseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get("X-Correlation-Id")
		if correlationID == "" {
			correlationID = newID("cor")
		}
		w.Header().Set("X-Correlation-Id", correlationID)
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		started := time.Now()
		mux.ServeHTTP(w, r)
		logger.Info("request completed", "correlation_id", correlationID, "method", r.Method,
			"path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
	handler := authenticate(verifier, cfg.AuthMode == "disabled", baseHandler)
	return &http.Server{Addr: cfg.HTTPAddress, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func newID(prefix string) string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	return prefix + "_" + hex.EncodeToString(value[:])
}
