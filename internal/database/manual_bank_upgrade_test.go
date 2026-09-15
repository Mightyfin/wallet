package database

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestManualBankUpgradeRepairsHistoricalVersionFive(t *testing.T) {
	dsn := os.Getenv("WALLET_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable wallet database required")
	}
	ctx := context.Background()
	admin, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("manual_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	if err = Migrate(u.String()); err != nil {
		t.Fatal(err)
	}
	p, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Simulate the observed legacy database only inside this isolated schema.
	// Never edit the deployed migration history to repair an installation.
	_, err = p.Exec(ctx, `DROP TRIGGER manual_bank_posting_checked ON journal_transactions;
	 DROP FUNCTION enforce_manual_bank_posting(); DROP INDEX journal_manual_bank_payment_unique;
	 DELETE FROM goose_db_version WHERE version_id=6;`)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(u.String()); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(u.String()); err != nil {
		t.Fatalf("repeat upgrade: %v", err)
	}
	var trigger, idx bool
	err = p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='journal_transactions'::regclass AND tgname='manual_bank_posting_checked' AND tgdeferrable AND tginitdeferred),to_regclass('journal_manual_bank_payment_unique') IS NOT NULL`).Scan(&trigger, &idx)
	if err != nil || !trigger || !idx {
		t.Fatalf("manual safeguards missing: trigger=%v index=%v err=%v", trigger, idx, err)
	}
}
