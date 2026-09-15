package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/config"
	"github.com/Mightyfin/wallet-ledger/internal/database"
	"github.com/Mightyfin/wallet-ledger/internal/eventbus"
	"github.com/Mightyfin/wallet-ledger/internal/outbox"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration rejected", "error", err)
		os.Exit(1)
	}
	natsURL := strings.TrimSpace(os.Getenv("WALLET_LEDGER_NATS_URL"))
	if natsURL == "" {
		logger.Error("NATS configuration is required")
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
	var publisher *eventbus.Publisher
	var closeConnection func()
	user := strings.TrimSpace(os.Getenv("WALLET_LEDGER_NATS_USER"))
	password := os.Getenv("WALLET_LEDGER_NATS_PASSWORD")
	token := strings.TrimSpace(os.Getenv("WALLET_LEDGER_NATS_TOKEN"))
	if user != "" || password != "" {
		if token != "" {
			logger.Error("event credentials are ambiguous; shared token must be unset")
			os.Exit(1)
		}
		publisher, closeConnection, err = eventbus.NewPublisherWithCredentials(natsURL, user, password, cfg.Environment)
	} else {
		// Compatibility during the staged broker migration only.
		publisher, closeConnection, err = eventbus.NewPublisher(natsURL, token, cfg.Environment)
	}
	if err != nil {
		logger.Error("NATS unavailable", "error", err)
		os.Exit(1)
	}
	defer closeConnection()
	if err = publisher.EnsureStream(ctx); err != nil {
		logger.Error("Wallet stream unavailable", "error", err)
		os.Exit(1)
	}
	worker := outbox.Worker{Store: outbox.NewStore(pool), Publisher: publisher, BatchSize: 100, Lease: 5 * time.Minute}
	for ticker := time.NewTicker(time.Second); ; {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err = worker.RunOnce(ctx); err != nil {
				logger.Error("outbox delivery failed", "error", err)
			}
		}
	}
}
