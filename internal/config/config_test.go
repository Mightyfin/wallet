package config

import "testing"

func TestLoadRejectsMissingFinancialDependencies(t *testing.T) {
	t.Setenv("WALLET_LEDGER_DATABASE_URL", "")
	t.Setenv("WALLET_LEDGER_OIDC_ISSUER", "")
	t.Setenv("WALLET_LEDGER_OIDC_AUDIENCE", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want required configuration error")
	}
}

func TestLoadAcceptsProduction(t *testing.T) {
	t.Setenv("WALLET_LEDGER_ENVIRONMENT", "production")
	t.Setenv("WALLET_LEDGER_AUTH_MODE", "oidc")
	t.Setenv("WALLET_LEDGER_DATABASE_URL", "postgres://test:test@localhost/wallet")
	t.Setenv("WALLET_LEDGER_OIDC_ISSUER", "https://identity.example.test/realms/production")
	t.Setenv("WALLET_LEDGER_OIDC_AUDIENCE", "wallet-ledger")
	if _, err := Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadRejectsDisabledAuthenticationOutsideLocal(t *testing.T) {
	t.Setenv("WALLET_LEDGER_ENVIRONMENT", "production")
	t.Setenv("WALLET_LEDGER_DATABASE_URL", "postgres://test:test@localhost/wallet")
	t.Setenv("WALLET_LEDGER_AUTH_MODE", "disabled")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted disabled production authentication")
	}
}
