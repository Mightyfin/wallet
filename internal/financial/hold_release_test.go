package financial

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Mightyfin/wallet-ledger/internal/database"
)

func TestHoldReleaseRetryIsScopedAndEmitsOnce(t *testing.T) {
	dsn := os.Getenv("WALLET_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated disposable database required")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	entity, err := s.CreateLegalEntity(ctx, "Synthetic release", "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateWallet(ctx, Wallet{LegalEntityID: entity.ID, TenantID: "release-tenant", OwnerType: "customer", OwnerID: "release-owner", Currency: "ZMW"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DisburseLoan(ctx, LoanDisbursement{TenantID: w.TenantID, DestinationWalletID: w.ID, FacilityID: newID("fac"), DisbursementID: newID("draw"), Amount: "100.00", Currency: "ZMW", IdempotencyKey: newID("fund")})
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CreateHold(ctx, HoldRequest{TenantID: w.TenantID, WalletID: w.ID, Amount: "20.00", Currency: "ZMW", Reason: "external_payment", IdempotencyKey: newID("hold")})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		released, err := s.ReleaseHold(ctx, "", w.TenantID, w.ID, h.ID)
		if err != nil || released.ID != h.ID || released.Status != "released" {
			t.Fatalf("retry %d: %+v %v", i, released, err)
		}
	}
	if _, err = s.ReleaseHold(ctx, entity.ID, "other-tenant", w.ID, h.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign tenant: %v", err)
	}
	var events int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='wallet.hold.released' AND aggregate_id=$1`, h.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("events=%d err=%v", events, err)
	}
}
