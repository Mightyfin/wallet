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

	t.Run("capture expires while waiting for wallet lock", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		deadline := time.Now().Add(2 * time.Second)
		reserved, err := s.CreateHold(ctx, HoldRequest{LegalEntityID: entity.ID, TenantID: w.TenantID, WalletID: w.ID, Amount: "10.00", Currency: "ZMW", Reason: "capture-test", IdempotencyKey: newID("hold"), ExpiresAt: &deadline})
		if err != nil {
			t.Fatal(err)
		}
		lock, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Rollback(context.Background())
		var id string
		if err = lock.QueryRow(ctx, `SELECT id::text FROM wallets WHERE public_id=$1 FOR UPDATE`, w.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		pid := lock.Conn().PgConn().PID()
		result := make(chan error, 1)
		go func() {
			_, err := s.RecordSettledWithdrawal(ctx, SettledWithdrawal{LegalEntityID: entity.ID, TenantID: w.TenantID, WalletID: w.ID, HoldID: reserved.ID, Amount: "10.00", Currency: "ZMW", SettlementReference: newID("settle"), EvidenceID: newID("evidence"), IdempotencyKey: newID("capture")})
			result <- err
		}()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case err := <-result:
				t.Fatal("capture did not wait", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
		// Keep the lock until the database clock is beyond the saved deadline.
		if _, err = lock.Exec(ctx, `SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM ($1::timestamptz-clock_timestamp())))+0.02)`, reserved.ExpiresAt); err != nil {
			t.Fatal(err)
		}
		if err = lock.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, ErrConflict) {
				t.Fatal("expired capture accepted", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		var postings int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM journal_transactions WHERE metadata->>'hold_id'=$1`, reserved.ID).Scan(&postings); err != nil || postings != 0 {
			t.Fatal("expired capture posted", postings, err)
		}
		var status string
		if err = pool.QueryRow(ctx, `SELECT status FROM balance_holds WHERE public_id=$1`, reserved.ID).Scan(&status); err != nil || status != "active" {
			t.Fatal("failed capture mutated hold", status, err)
		}
	})
}
