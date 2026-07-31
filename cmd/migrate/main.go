package main

import (
	"log"

	"github.com/Mightyfin/wallet-ledger/internal/config"
	"github.com/Mightyfin/wallet-ledger/internal/database"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	if err := database.Migrate(cfg.DatabaseURL); err != nil {
		log.Fatal(err)
	}
}
