package financial

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/database"
)

func TestConfirmedBankDisbursementNoWalletCashAndSinglePosting(t *testing.T) {
	dsn := os.Getenv("WALLET_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable wallet database required")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := New(pool)
	le, err := s.CreateLegalEntity(ctx, "Synthetic Manual Bank UAT", "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateWallet(ctx, Wallet{LegalEntityID: le.ID, TenantID: newID("tenant"), OwnerType: "customer", OwnerID: newID("party"), Currency: "ZMW"})
	if err != nil {
		t.Fatal(err)
	}
	in := ConfirmedBankDisbursement{LegalEntityID: le.ID, TenantID: w.TenantID, Environment: "sandbox", WalletID: w.ID, PartyID: w.OwnerID, FacilityID: newID("fac"), PaymentID: newID("mpay"), AuthorizationID: newID("mba"), SourceAccountID: "synthetic-bank", DestinationAccountID: "synthetic-payee", EvidenceDigest: strings.Repeat("a", 64), ReviewedBy: "independent-synthetic-reviewer", AmountMinor: 500000, Currency: "ZMW", PaidAt: time.Now().UTC().Add(-time.Minute)}
	var wg sync.WaitGroup
	if _, err = s.FindConfirmedBankDisbursement(ctx, in); !errors.Is(err, ErrNotFound) {
		t.Fatal("lookup invented a posting", err)
	}
	// An uncommitted suspension must serialize with a new posting, rather than
	// letting the posting use an older active snapshot of the lender row.
	blocking, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocking.Rollback(ctx)
	if _, err = blocking.Exec(ctx, `UPDATE legal_entities SET status='suspended' WHERE public_id=$1`, le.ID); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	_, blockedErr := s.RecordConfirmedBankDisbursement(short, in)
	cancel()
	if !errors.Is(blockedErr, context.DeadlineExceeded) {
		t.Fatalf("posting did not wait for lender state change: %v", blockedErr)
	}
	if err = blocking.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FindConfirmedBankDisbursement(ctx, in); !errors.Is(err, ErrNotFound) {
		t.Fatal("timed-out lender check left a journal", err)
	}
	results := make(chan Transaction, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := s.RecordConfirmedBankDisbursement(ctx, in); results <- r; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var id string
	for r := range results {
		if id != "" && id != r.ID {
			t.Fatal("duplicate journal")
		}
		id = r.ID
	}
	balance, err := s.GetBalance(ctx, le.ID, w.TenantID, w.ID)
	for i := 0; i < 3; i++ {
		found, e := s.FindConfirmedBankDisbursement(ctx, in)
		if e != nil || found.ID != id || found.Amount.StringFixed(2) != "5000.00" {
			t.Fatal("existing posting recovery failed", found, e)
		}
	}
	if err != nil || !balance.Available.IsZero() || !balance.Ledger.IsZero() {
		t.Fatal("bank payment created wallet cash", balance, err)
	}
	var debit, credit string
	var count int
	err = pool.QueryRow(ctx, `SELECT count(*),sum(CASE WHEN e.side='debit' AND a.account_purpose='loan_receivable' THEN e.amount ELSE 0 END)::text,sum(CASE WHEN e.side='credit' AND a.account_purpose='bank_clearing' THEN e.amount ELSE 0 END)::text FROM journal_entries e JOIN ledger_accounts a ON a.id=e.account_id JOIN journal_transactions j ON j.id=e.transaction_id WHERE j.public_id=$1`, id).Scan(&count, &debit, &credit)
	if err != nil || count != 2 || debit != "5000.00" || credit != "5000.00" {
		t.Fatal("incorrect bank journal", count, debit, credit, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='loan.external_disbursement.posted'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("event count", count, err)
	}
	for _, change := range []string{"amount", "tenant", "environment", "payee", "evidence", "party"} {
		bad := in
		switch change {
		case "amount":
			bad.AmountMinor++
		case "tenant":
			bad.TenantID = "other"
		case "environment":
			bad.Environment = "production"
		case "payee":
			bad.DestinationAccountID = "other"
		case "evidence":
			bad.EvidenceDigest = strings.Repeat("b", 64)
		case "party":
			bad.PartyID = "other"
		}
		if _, err = s.RecordConfirmedBankDisbursement(ctx, bad); !errors.Is(err, ErrConflict) {
			t.Fatal("changed instruction replay", change, err)
		}
		_, lookupErr := s.FindConfirmedBankDisbursement(ctx, bad)
		if change == "tenant" || change == "environment" {
			if !errors.Is(lookupErr, ErrNotFound) {
				t.Fatal("lookup leaked another scope", change, lookupErr)
			}
		} else if !errors.Is(lookupErr, ErrConflict) {
			t.Fatal("lookup accepted changed instruction", change, lookupErr)
		}
	}
	bad := in
	bad.PaymentID = newID("mpay")
	bad.PartyID = "wrong-party"
	if _, err = s.RecordConfirmedBankDisbursement(ctx, bad); !errors.Is(err, ErrNotFound) {
		t.Fatal("wrong borrower accepted", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE journal_transactions SET status='reversed' WHERE public_id=$1`, id); err == nil {
		t.Fatal("posted bank journal mutable")
	}
	// Fail the event insert deliberately; the bank journal must roll back too.
	fn := newID("reject_bank_event")
	if _, err = pool.Exec(ctx, `CREATE FUNCTION `+fn+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='loan.external_disbursement.posted' THEN RAISE EXCEPTION 'injected manual event failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER `+fn+` BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION `+fn+`() `); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DROP TRIGGER `+fn+` ON outbox_events; DROP FUNCTION `+fn+`() `)
	failed := in
	failed.PaymentID = newID("mpay")
	if _, err = s.RecordConfirmedBankDisbursement(ctx, failed); err == nil {
		t.Fatal("event failure ignored")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM journal_transactions WHERE external_reference=$1`, failed.PaymentID).Scan(&count); err != nil || count != 0 {
		t.Fatal("journal survived failed event", count, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE legal_entities SET status='suspended' WHERE public_id=$1`, le.ID); err != nil {
		t.Fatal(err)
	}
	if found, e := s.FindConfirmedBankDisbursement(ctx, in); e != nil || found.ID != id {
		t.Fatal("historical recovery lost after suspension", e)
	}
}

func TestConfirmedBankDisbursementRejectsUnusableInstruction(t *testing.T) {
	if err := (ConfirmedBankDisbursement{}).validate(time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
