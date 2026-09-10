package financial

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/Mightyfin/wallet-ledger/internal/database"
)

func TestLenderLiquidityReservation(t *testing.T) {
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
	entity, err := s.CreateLegalEntity(ctx, "Synthetic lender", "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	var entityID, accountID, offsetID string
	accountPublic := newID("acc")
	if err = pool.QueryRow(ctx, `SELECT id::text FROM legal_entities WHERE public_id=$1`, entity.ID).Scan(&entityID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO ledger_accounts(public_id,legal_entity_id,account_code,account_class,account_purpose,normal_side,currency,status) VALUES($1,$2,'test-lender','asset','lender_liquidity','debit','ZMW','active') RETURNING id::text`, accountPublic, entityID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO ledger_accounts(public_id,legal_entity_id,account_code,account_class,account_purpose,normal_side,currency,status) VALUES($1,$2,'test-offset','equity','test_capital','credit','ZMW','active') RETURNING id::text`, newID("acc"), entityID).Scan(&offsetID); err != nil {
		t.Fatal(err)
	}
	in := LiquidityRequest{RequestedBy: "test-service", LegalEntityID: entity.ID, AccountID: accountPublic, TenantID: "lender-test", FacilityID: newID("fac"), AuthorizationID: newID("auth"), Amount: "70.00", Currency: "ZMW"}
	if _, err = s.ReserveLenderLiquidity(ctx, in); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatal("unfunded account", err)
	}
	// Test-only posted capital. There is no production balance seeding endpoint.
	post := func(side string, amount string) error {
		tx, e := pool.Begin(ctx)
		if e != nil {
			return e
		}
		defer tx.Rollback(ctx)
		var txn string
		e = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,idempotency_key,request_hash,correlation_id,source_system,effective_at) VALUES($1,$2,'lender-test','test','test','posted','ZMW',$3,$4,$5,'test',now()) RETURNING id::text`, newID("txn"), entityID, newID("key"), hashRequest(newID("hash")), newID("cor")).Scan(&txn)
		if e != nil {
			return e
		}
		other := "credit"
		if side == "credit" {
			other = "debit"
		}
		if _, e = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) VALUES($1,$2,$3,$4,$5,'ZMW'),($6,$2,$7,$8,$5,'ZMW')`, newID("ent"), txn, accountID, side, amount, newID("ent"), offsetID, other); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if err = post("debit", "100.00"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wins := make(chan LiquidityRequest, 2)
	errs := make(chan error, 2)
	for range 2 {
		request := in
		request.AuthorizationID = newID("auth")
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.ReserveLenderLiquidity(ctx, request)
			if e != nil {
				errs <- e
			} else {
				wins <- request
			}
		}()
	}
	wg.Wait()
	close(wins)
	close(errs)
	if len(wins) != 1 || len(errs) != 1 {
		for e := range errs {
			t.Log("reservation error", e)
		}
		t.Fatal("oversubscribed", len(wins), len(errs))
	}
	for e := range errs {
		if !errors.Is(e, ErrInsufficientBalance) {
			t.Fatal(e)
		}
	}
	winner := <-wins
	first, err := s.ReserveLenderLiquidity(ctx, winner)
	if err != nil {
		t.Fatal(err)
	}
	testLiquidityRead(t, ctx, s, winner, first)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.ReserveLenderLiquidity(ctx, winner)
			if e != nil || r.ID != first.ID {
				t.Error("retry", r, e)
			}
		}()
	}
	wg.Wait()
	changed := winner
	changed.Amount = "69.00"
	if _, err = s.ReserveLenderLiquidity(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed retry", err)
	}
	if err = post("credit", "31.00"); err == nil {
		t.Fatal("spent reserved liquidity")
	}
	if err = post("credit", "30.00"); err != nil {
		t.Fatal("unreserved liquidity blocked", err)
	}
	var count int
	var sum string
	if err = pool.QueryRow(ctx, `SELECT count(*),sum(amount)::text FROM lender_liquidity_reservations WHERE account_id=$1`, accountID).Scan(&count, &sum); err != nil || count != 1 || sum != "70.00" {
		t.Fatal(count, sum, err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM lender_liquidity_reservations WHERE account_id=$1`, accountID); err == nil {
		t.Fatal("history removed")
	}
	if _, err = pool.Exec(ctx, `UPDATE ledger_accounts SET account_purpose='other' WHERE id=$1`, accountID); err == nil {
		t.Fatal("reserved account relabelled")
	}
	var clearing string
	if err = pool.QueryRow(ctx, `SELECT public_id FROM ledger_accounts WHERE legal_entity_id=$1 AND account_purpose='bank_clearing'`, entityID).Scan(&clearing); err != nil {
		t.Fatal(err)
	}
	changed = in
	changed.AccountID = clearing
	changed.AuthorizationID = newID("auth")
	if _, err = s.ReserveLenderLiquidity(ctx, changed); !errors.Is(err, ErrNotFound) {
		t.Fatal("shared clearing accepted", err)
	}
	testGoodsCredit(t, ctx, s, winner, first)
}
