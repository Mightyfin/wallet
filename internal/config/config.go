package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Environment  string
	HTTPAddress  string
	DatabaseURL  string
	OIDCIssuer   string
	OIDCAudience string
	AuthMode     string
}

func Load() (Config, error) {
	databaseURL, err := secretValue("WALLET_LEDGER_DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Environment:  strings.TrimSpace(os.Getenv("WALLET_LEDGER_ENVIRONMENT")),
		HTTPAddress:  envOrDefault("WALLET_LEDGER_HTTP_ADDRESS", ":8080"),
		DatabaseURL:  databaseURL,
		OIDCIssuer:   strings.TrimRight(strings.TrimSpace(os.Getenv("WALLET_LEDGER_OIDC_ISSUER")), "/"),
		OIDCAudience: strings.TrimSpace(os.Getenv("WALLET_LEDGER_OIDC_AUDIENCE")),
		AuthMode:     envOrDefault("WALLET_LEDGER_AUTH_MODE", "oidc"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("database configuration is required")
	}
	if cfg.Environment == "" {
		return Config{}, fmt.Errorf("WALLET_LEDGER_ENVIRONMENT is required")
	}
	if !strings.HasPrefix(cfg.HTTPAddress, ":") {
		return Config{}, fmt.Errorf("WALLET_LEDGER_HTTP_ADDRESS must use :port form")
	}
	switch cfg.Environment {
	case "local", "dev", "sandbox", "staging", "production":
	default:
		return Config{}, fmt.Errorf("unsupported environment %q", cfg.Environment)
	}
	if cfg.AuthMode == "disabled" {
		if cfg.Environment != "local" {
			return Config{}, fmt.Errorf("authentication may only be disabled locally")
		}
	} else if cfg.AuthMode != "oidc" {
		return Config{}, fmt.Errorf("unsupported authentication mode %q", cfg.AuthMode)
	} else if cfg.OIDCIssuer == "" || cfg.OIDCAudience == "" {
		return Config{}, fmt.Errorf("OIDC configuration is required")
	}
	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func secretValue(name string) (string, error) {
	direct := strings.TrimSpace(os.Getenv(name))
	fileName := strings.TrimSpace(os.Getenv(name + "_FILE"))
	if direct != "" && fileName != "" {
		return "", fmt.Errorf("%s and %s_FILE cannot both be set", name, name)
	}
	if fileName == "" {
		return direct, nil
	}
	value, err := os.ReadFile(fileName)
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", name, err)
	}
	return strings.TrimSpace(string(value)), nil
}
