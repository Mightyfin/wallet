package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
	"github.com/Mightyfin/wallet-ledger/internal/config"
	"github.com/Mightyfin/wallet-ledger/internal/database"
	"github.com/Mightyfin/wallet-ledger/internal/financial"
	"github.com/Mightyfin/wallet-ledger/internal/httpserver"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		runHealthcheck()
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration rejected", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	var verifier auth.Verifier
	if cfg.AuthMode == "oidc" {
		verifier, err = auth.NewOIDCVerifier(ctx, cfg.OIDCIssuer, cfg.OIDCAudience, cfg.Environment)
		if err != nil {
			logger.Error("OIDC unavailable", "error", err)
			os.Exit(1)
		}
	}
	server := httpserver.New(cfg, logger, pool, financial.New(pool), verifier)
	go func() {
		logger.Info("wallet-ledger API starting", "environment", cfg.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

func runHealthcheck() {
	address := os.Getenv("WALLET_LEDGER_HTTP_ADDRESS")
	port := strings.TrimPrefix(address, ":")
	if port == "" {
		port = "8080"
	}
	client := http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + port + "/health/ready")
	if err != nil || response.StatusCode != http.StatusOK {
		os.Exit(1)
	}
	_ = response.Body.Close()
}
