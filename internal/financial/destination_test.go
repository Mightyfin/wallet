package financial

import (
	"context"
	"errors"
	"github.com/Mightyfin/wallet-ledger/internal/database"
	"os"
	"testing"
)

func TestDestinationVerificationScopeAndStatus(t *testing.T) {
	dsn := os.Getenv("WALLET_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable database required")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := New(pool)
	entity, err := s.CreateLegalEntity(ctx, "Synthetic destination", "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := s.CreateWallet(ctx, Wallet{LegalEntityID: entity.ID, TenantID: "destination-tenant", OwnerType: "organization", OwnerID: "supplier", Currency: "ZMW"})
	if err != nil {
		t.Fatal(err)
	}
	in := DestinationRequest{LegalEntityID: entity.ID, TenantID: wallet.TenantID, WalletID: wallet.ID, PartyID: wallet.OwnerID, Currency: wallet.Currency}
	got, err := s.VerifyDestination(ctx, in)
	if err != nil || got.WalletID != wallet.ID || got.PartyID != "supplier" || got.VerifiedAt.IsZero() {
		t.Fatal(got, err)
	}
	for _, field := range []string{"tenant", "party", "currency", "entity", "wallet"} {
		wrong := in
		switch field {
		case "tenant":
			wrong.TenantID = "foreign"
		case "party":
			wrong.PartyID = "other"
		case "currency":
			wrong.Currency = "USD"
		case "entity":
			wrong.LegalEntityID = "other"
		case "wallet":
			wrong.WalletID = "other"
		}
		if _, err = s.VerifyDestination(ctx, wrong); !errors.Is(err, ErrNotFound) {
			t.Fatal(field, err)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE wallets SET status='suspended' WHERE public_id=$1`, wallet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyDestination(ctx, in); !errors.Is(err, ErrNotFound) {
		t.Fatal("suspended destination verified", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE wallets SET status='active' WHERE public_id=$1`, wallet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE ledger_accounts SET status='blocked' WHERE wallet_id=(SELECT id FROM wallets WHERE public_id=$1)`, wallet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyDestination(ctx, in); !errors.Is(err, ErrNotFound) {
		t.Fatal("blocked receiving account verified", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE ledger_accounts SET status='active' WHERE wallet_id=(SELECT id FROM wallets WHERE public_id=$1)`, wallet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE legal_entities SET status='suspended' WHERE public_id=$1`, entity.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyDestination(ctx, in); !errors.Is(err, ErrNotFound) {
		t.Fatal("inactive entity verified", err)
	}
}
