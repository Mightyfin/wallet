package financial

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func testGoodsCredit(t *testing.T, ctx context.Context, s *Service, in LiquidityRequest, r LiquidityReservation) {
	t.Helper()
	supplier, err := s.CreateWallet(ctx, Wallet{LegalEntityID: in.LegalEntityID, TenantID: in.TenantID, OwnerType: "organization", OwnerID: "goods-supplier", Currency: in.Currency})
	if err != nil {
		t.Fatal(err)
	}
	borrower, err := s.CreateWallet(ctx, Wallet{LegalEntityID: in.LegalEntityID, TenantID: in.TenantID, OwnerType: "customer", OwnerID: "goods-borrower", Currency: in.Currency})
	if err != nil {
		t.Fatal(err)
	}
	a := GoodsAuthorization{ID: newID("gca"), TenantID: in.TenantID, ReservationID: r.ID, BorrowerPartyID: borrower.OwnerID, SupplierPartyID: supplier.OwnerID, DestinationWalletID: supplier.ID, OrderReference: "SYNTHETIC-ORDER", Amount: "70.00", Currency: in.Currency, AuthorizedBy: "synthetic-facility-approval", AllowPartialUse: true, ExpiresAt: time.Now().Add(time.Hour)}
	wrong := a
	wrong.SupplierPartyID = borrower.OwnerID
	if err = s.RegisterGoodsAuthorization(ctx, wrong); err == nil {
		t.Fatal("self financing admitted")
	}
	wrong = a
	wrong.ExpiresAt = time.Now().Add(-time.Hour)
	if err = s.RegisterGoodsAuthorization(ctx, wrong); err == nil {
		t.Fatal("expired authority admitted")
	}
	if err = s.RegisterGoodsAuthorization(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterGoodsAuthorization(ctx, a); err != nil {
		t.Fatal("authorization retry", err)
	}
	wrong = a
	wrong.OrderReference = "changed"
	if err = s.RegisterGoodsAuthorization(ctx, wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("changed authority", err)
	}
	if _, err = s.PostGoodsUse(ctx, "other", a.ID, "use", "20", "test"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign draw", err)
	}
	if _, err = s.PostGoodsUse(ctx, a.TenantID, a.ID, "too-much", "71", "test"); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatal("over limit", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE wallets SET status='suspended' WHERE public_id=$1`, supplier.ID); err != nil {
		t.Fatal(err)
	}
	_, blockedErr := s.PostGoodsUse(ctx, a.TenantID, a.ID, "inactive-supplier", "20", "test")
	if _, err = s.pool.Exec(ctx, `UPDATE wallets SET status='active' WHERE public_id=$1`, supplier.ID); err != nil {
		t.Fatal(err)
	}
	if blockedErr == nil {
		t.Fatal("inactive supplier received a draw")
	}
	// Force the atomic event append to fail: neither debt nor supplier cash may remain.
	if _, err = s.pool.Exec(ctx, `ALTER TABLE outbox_events ADD CONSTRAINT test_goods_outbox_failure CHECK(event_type<>'goods.credit.use.posted') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	_, postErr := s.PostGoodsUse(ctx, a.TenantID, a.ID, "retry-after-rollback", "20", "test")
	if _, err = s.pool.Exec(ctx, `ALTER TABLE outbox_events DROP CONSTRAINT test_goods_outbox_failure`); err != nil {
		t.Fatal(err)
	}
	if postErr == nil {
		t.Fatal("missing event allowed a posting")
	}
	balance, err := s.GetBalance(ctx, in.LegalEntityID, in.TenantID, supplier.ID)
	if err != nil || !balance.Ledger.IsZero() {
		t.Fatal("rollback left money", balance, err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, e := s.PostGoodsUse(ctx, a.TenantID, a.ID, "retry-after-rollback", "20", "test")
			if e != nil {
				t.Error(e)
				return
			}
			ids <- result.ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for value := range ids {
		if first != "" && first != value {
			t.Fatal("duplicate posting")
		}
		first = value
	}
	if first == "" {
		t.Fatal("no successful draw")
	}
	if _, err = s.PostGoodsUse(ctx, a.TenantID, a.ID, "retry-after-rollback", "21", "test"); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay", err)
	}
	wins := make(chan bool, 2)
	for _, id := range []string{"competing-a", "competing-b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, e := s.PostGoodsUse(ctx, a.TenantID, a.ID, id, "40", "test")
			if e != nil && !errors.Is(e, ErrInsufficientBalance) {
				t.Error(e)
			}
			wins <- e == nil
		}(id)
	}
	wg.Wait()
	close(wins)
	n := 0
	for won := range wins {
		if won {
			n++
		}
	}
	if n != 1 {
		t.Fatal("concurrent draws exceeded capacity", n)
	}
	if _, err = s.PostGoodsUse(ctx, a.TenantID, a.ID, "final", "10", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PostGoodsUse(ctx, a.TenantID, a.ID, "excess", "0.01", "test"); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatal("exhausted capacity", err)
	}
	balance, err = s.GetBalance(ctx, in.LegalEntityID, in.TenantID, supplier.ID)
	if err != nil || balance.Ledger.StringFixed(2) != "70.00" {
		t.Fatal("supplier balance", balance, err)
	}
	balance, err = s.GetBalance(ctx, in.LegalEntityID, in.TenantID, borrower.ID)
	if err != nil || !balance.Ledger.IsZero() {
		t.Fatal("borrower received cash", balance, err)
	}
	var uses, events int
	var total string
	if err = s.pool.QueryRow(ctx, `SELECT count(*),sum(amount)::text FROM goods_credit_uses WHERE authorization_id=$1`, a.ID).Scan(&uses, &total); err != nil || uses != 3 || total != "70.00" {
		t.Fatal(uses, total, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='goods.credit.use.posted' AND payload->>'authorization_id'=$1`, a.ID).Scan(&events); err != nil || events != uses {
		t.Fatal("missing or repeated event", events, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE goods_credit_authorizations SET destination_wallet_id=(SELECT id FROM wallets WHERE public_id=$2) WHERE id=$1`, a.ID, borrower.ID); err == nil {
		t.Fatal("authority changed")
	}
	if _, err = s.pool.Exec(ctx, `DELETE FROM goods_credit_uses WHERE authorization_id=$1`, a.ID); err == nil {
		t.Fatal("use history deleted")
	}
	// Even a balanced copied journal cannot bypass the separate draw record.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var forgedID string
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,idempotency_key,request_hash,correlation_id,source_system,effective_at,metadata)
 SELECT $1,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,$2,request_hash,correlation_id,source_system,effective_at,metadata FROM journal_transactions WHERE public_id=$3 RETURNING id::text`, newID("txn"), newID("forged"), first).Scan(&forgedID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency)
 SELECT 'ent_'||gen_random_uuid()::text,$1,account_id,side,amount,currency FROM journal_entries WHERE transaction_id=(SELECT id FROM journal_transactions WHERE public_id=$2)`, forgedID, first)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil {
		t.Fatal("unattributed goods posting committed")
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency)
 SELECT 'ent_'||gen_random_uuid()::text,transaction_id,account_id,side,amount,currency FROM journal_entries WHERE transaction_id=(SELECT id FROM journal_transactions WHERE public_id=$1)`, first); err == nil {
		t.Fatal("extra balanced entries changed an existing goods draw")
	}
	verified, err := s.ReadLenderLiquidity(ctx, in.TenantID, in.LegalEntityID, r.ID)
	if err != nil || verified.Reservation.Amount != "70.00" || verified.Reservation.Status != "reserved" {
		t.Fatal("supplier backing released", verified, err)
	}
}

func TestGoodsAmountValidation(t *testing.T) {
	for _, raw := range []string{"0", "-1", "1.001", "NaN", "1000000000000000000", "1e-20"} {
		if _, err := goodsAmount(raw); !errors.Is(err, ErrConflict) {
			t.Errorf("accepted invalid amount %q", raw)
		}
	}
	for _, raw := range []string{"0.01", "5000", "999999999999999999.99"} {
		if _, err := goodsAmount(raw); err != nil {
			t.Errorf("rejected valid amount %q: %v", raw, err)
		}
	}
}
