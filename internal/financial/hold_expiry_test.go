package financial

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/database"
)

func TestHoldExpiryRetryAndAdmission(t *testing.T) {
	dsn := os.Getenv("WALLET_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated disposable database required")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := New(pool)
	entity, err := s.CreateLegalEntity(ctx, "Synthetic hold expiry", "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateWallet(ctx, Wallet{LegalEntityID: entity.ID, TenantID: "hold-tenant", OwnerType: "customer", OwnerID: "hold-owner", Currency: "ZMW"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DisburseLoan(ctx, LoanDisbursement{LegalEntityID: entity.ID, TenantID: w.TenantID, DestinationWalletID: w.ID, FacilityID: newID("fac"), DisbursementID: newID("draw"), Amount: "100.00", Currency: "ZMW", IdempotencyKey: newID("fund")})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	in := HoldRequest{LegalEntityID: entity.ID, TenantID: w.TenantID, WalletID: w.ID, Amount: "20.00", Currency: "ZMW", Reason: "purchase", IdempotencyKey: newID("hold"), ExpiresAt: &expires}
	first, err := s.CreateHold(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateHold(ctx, in)
	if err != nil || replay.ID != first.ID {
		t.Fatal(replay, err)
	}
	zone := expires.In(time.FixedZone("test", 7200))
	in.ExpiresAt = &zone
	if replay, err = s.CreateHold(ctx, in); err != nil || replay.ID != first.ID {
		t.Fatal("same instant", replay, err)
	}
	later := expires.Add(time.Minute)
	for _, deadline := range []*time.Time{nil, &later} {
		in.ExpiresAt = deadline
		if _, err = s.CreateHold(ctx, in); !errors.Is(err, ErrConflict) {
			t.Fatal("changed expiry accepted", err)
		}
	}
	past := time.Now().Add(-time.Minute)
	in.ExpiresAt = &past
	in.IdempotencyKey = newID("expired")
	if _, err = s.CreateHold(ctx, in); !errors.Is(err, ErrConflict) {
		t.Fatal("expired admission", err)
	}
	balance, err := s.GetBalance(ctx, entity.ID, w.TenantID, w.ID)
	if err != nil || balance.Held.StringFixed(2) != "20.00" {
		t.Fatal(balance, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='wallet.hold.created' AND payload->>'wallet_id'=$1`, w.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("extra hold event", count, err)
	}
	in.TenantID = "other"
	in.ExpiresAt = &expires
	if _, err = s.CreateHold(ctx, in); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign tenant", err)
	}
}
