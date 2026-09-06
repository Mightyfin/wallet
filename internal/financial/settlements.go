package financial

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type SettledDeposit struct {
	LegalEntityID, TenantID, WalletID, SettlementReference, EvidenceID string
	Amount, Currency, IdempotencyKey, CorrelationID, SourceSystem      string
}

type SettledWithdrawal struct {
	LegalEntityID, TenantID, WalletID, HoldID, SettlementReference, EvidenceID string
	Amount, Currency, IdempotencyKey, CorrelationID, SourceSystem              string
}

// RecordSettledWithdrawal captures a previously reserved wallet hold after
// Payment Rails reconciliation confirms that money left the external rail.
// The hold is mandatory: provider dispatch must never race an unreserved
// customer balance.
func (s *Service) RecordSettledWithdrawal(ctx context.Context, in SettledWithdrawal) (Transaction, error) {
	amount, err := decimal.NewFromString(in.Amount)
	if err != nil || !amount.IsPositive() || amount.Exponent() < -2 {
		return Transaction{}, ErrConflict
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.LegalEntityID == "" || in.TenantID == "" || in.WalletID == "" || in.HoldID == "" || in.SettlementReference == "" || in.EvidenceID == "" || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128 || len(in.Currency) != 3 {
		return Transaction{}, ErrConflict
	}
	if in.CorrelationID == "" {
		in.CorrelationID = newID("cor")
	}
	if in.SourceSystem == "" {
		in.SourceSystem = "reconciliation-service"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)
	requestHash := hashRequest(in.WalletID, in.HoldID, in.SettlementReference, in.EvidenceID, amount.StringFixed(2), in.Currency)
	var existing Transaction
	var existingHash, existingAmount string
	err = tx.QueryRow(ctx, `SELECT jt.public_id,jt.status,jt.currency,jt.idempotency_key,jt.correlation_id,jt.request_hash,(SELECT COALESCE(SUM(amount),0)::text FROM journal_entries WHERE transaction_id=jt.id AND side='debit') FROM journal_transactions jt JOIN legal_entities le ON le.id=jt.legal_entity_id WHERE le.public_id=$1 AND jt.tenant_id=$2 AND jt.source_system=$3 AND jt.idempotency_key=$4`, in.LegalEntityID, in.TenantID, in.SourceSystem, in.IdempotencyKey).Scan(&existing.ID, &existing.Status, &existing.Currency, &existing.IdempotencyKey, &existing.CorrelationID, &existingHash, &existingAmount)
	if err == nil {
		if existingHash != requestHash {
			return Transaction{}, ErrConflict
		}
		existing.Amount, _ = decimal.NewFromString(existingAmount)
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, err
	}
	var entityUUID, walletUUID, bankAccount, walletAccount, walletCurrency string
	err = tx.QueryRow(ctx, `SELECT le.id::text,w.id::text,ba.id::text,wa.id::text,w.currency FROM legal_entities le JOIN wallets w ON w.legal_entity_id=le.id JOIN ledger_accounts ba ON ba.legal_entity_id=le.id AND ba.account_purpose='bank_clearing' AND ba.currency=w.currency JOIN ledger_accounts wa ON wa.wallet_id=w.id AND wa.account_purpose='wallet_available' WHERE le.public_id=$1 AND w.tenant_id=$2 AND w.public_id=$3 AND le.status='active' AND w.status='active' FOR UPDATE OF w,ba,wa`, in.LegalEntityID, in.TenantID, in.WalletID).Scan(&entityUUID, &walletUUID, &bankAccount, &walletAccount, &walletCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	if walletCurrency != in.Currency {
		return Transaction{}, ErrConflict
	}
	var holdAmount, holdCurrency, holdStatus string
	var holdExpired bool
	err = tx.QueryRow(ctx, `SELECT amount::text,currency,status,(expires_at IS NOT NULL AND expires_at<=now()) FROM balance_holds WHERE wallet_id=$1 AND public_id=$2 FOR UPDATE`, walletUUID, in.HoldID).Scan(&holdAmount, &holdCurrency, &holdStatus, &holdExpired)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	if holdStatus != "active" || holdExpired || holdAmount != amount.StringFixed(2) || holdCurrency != in.Currency {
		return Transaction{}, ErrConflict
	}
	result := Transaction{ID: newID("txn"), Status: "posted", Currency: in.Currency, IdempotencyKey: in.IdempotencyKey, CorrelationID: in.CorrelationID, Amount: amount}
	var transactionUUID string
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,external_reference,idempotency_key,request_hash,correlation_id,source_system,effective_at,metadata) VALUES($1,$2,$3,'external_withdrawal','external.withdrawal.settled','posted',$4,$5,$6,$7,$8,$9,$10,jsonb_build_object('wallet_id',$11::text,'hold_id',$12::text,'evidence_id',$13::text)) RETURNING id::text`, result.ID, entityUUID, in.TenantID, in.Currency, in.SettlementReference, in.IdempotencyKey, requestHash, in.CorrelationID, in.SourceSystem, time.Now().UTC(), in.WalletID, in.HoldID, in.EvidenceID).Scan(&transactionUUID)
	if err != nil {
		return Transaction{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) VALUES($1,$2,$3,'debit',$4,$5),($6,$2,$7,'credit',$4,$5)`, newID("ent"), transactionUUID, walletAccount, amount.StringFixed(2), in.Currency, newID("ent"), bankAccount); err != nil {
		return Transaction{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE balance_holds SET status='captured',updated_at=now() WHERE wallet_id=$1 AND public_id=$2 AND status='active' AND (expires_at IS NULL OR expires_at>now())`, walletUUID, in.HoldID)
	if err != nil {
		return Transaction{}, err
	}
	if tag.RowsAffected() != 1 {
		return Transaction{}, ErrConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'wallet.withdrawal.settled','journal_transaction',$2::varchar,jsonb_build_object('transaction_id',$2::varchar,'wallet_id',$3::text,'hold_id',$4::text,'amount',$5::text,'currency',$6::text,'settlement_reference',$7::text,'evidence_id',$8::text))`, newID("evt"), result.ID, in.WalletID, in.HoldID, amount.StringFixed(2), in.Currency, in.SettlementReference, in.EvidenceID); err != nil {
		return Transaction{}, err
	}
	return result, tx.Commit(ctx)
}

// RecordSettledDeposit credits spendable wallet value only after external
// settlement evidence has been confirmed by reconciliation.
func (s *Service) RecordSettledDeposit(ctx context.Context, in SettledDeposit) (Transaction, error) {
	amount, err := decimal.NewFromString(in.Amount)
	if err != nil || !amount.IsPositive() || amount.Exponent() < -2 {
		return Transaction{}, ErrConflict
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.LegalEntityID == "" || in.TenantID == "" || in.WalletID == "" || in.SettlementReference == "" || in.EvidenceID == "" || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128 || len(in.Currency) != 3 {
		return Transaction{}, ErrConflict
	}
	if in.CorrelationID == "" {
		in.CorrelationID = newID("cor")
	}
	if in.SourceSystem == "" {
		in.SourceSystem = "reconciliation-service"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)
	requestHash := hashRequest(in.WalletID, in.SettlementReference, in.EvidenceID, amount.StringFixed(2), in.Currency)
	var existing Transaction
	var existingHash, existingAmount string
	err = tx.QueryRow(ctx, `SELECT jt.public_id,jt.status,jt.currency,jt.idempotency_key,jt.correlation_id,jt.request_hash,(SELECT COALESCE(SUM(amount),0)::text FROM journal_entries WHERE transaction_id=jt.id AND side='debit') FROM journal_transactions jt JOIN legal_entities le ON le.id=jt.legal_entity_id WHERE le.public_id=$1 AND jt.tenant_id=$2 AND jt.source_system=$3 AND jt.idempotency_key=$4`, in.LegalEntityID, in.TenantID, in.SourceSystem, in.IdempotencyKey).Scan(&existing.ID, &existing.Status, &existing.Currency, &existing.IdempotencyKey, &existing.CorrelationID, &existingHash, &existingAmount)
	if err == nil {
		if existingHash != requestHash {
			return Transaction{}, ErrConflict
		}
		existing.Amount, _ = decimal.NewFromString(existingAmount)
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, err
	}
	var entityUUID, bankAccount, walletAccount, walletCurrency string
	err = tx.QueryRow(ctx, `SELECT le.id::text,ba.id::text,wa.id::text,w.currency FROM legal_entities le JOIN wallets w ON w.legal_entity_id=le.id JOIN ledger_accounts ba ON ba.legal_entity_id=le.id AND ba.account_purpose='bank_clearing' AND ba.currency=w.currency JOIN ledger_accounts wa ON wa.wallet_id=w.id AND wa.account_purpose='wallet_available' WHERE le.public_id=$1 AND w.tenant_id=$2 AND w.public_id=$3 AND le.status='active' AND w.status='active' FOR UPDATE OF ba,wa,w`, in.LegalEntityID, in.TenantID, in.WalletID).Scan(&entityUUID, &bankAccount, &walletAccount, &walletCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	if walletCurrency != in.Currency {
		return Transaction{}, ErrConflict
	}
	result := Transaction{ID: newID("txn"), Status: "posted", Currency: in.Currency, IdempotencyKey: in.IdempotencyKey, CorrelationID: in.CorrelationID, Amount: amount}
	var transactionUUID string
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,external_reference,idempotency_key,request_hash,correlation_id,source_system,effective_at,metadata) VALUES($1,$2,$3,'external_deposit','external.deposit.settled','posted',$4,$5,$6,$7,$8,$9,$10,jsonb_build_object('wallet_id',$11::text,'evidence_id',$12::text)) RETURNING id::text`, result.ID, entityUUID, in.TenantID, in.Currency, in.SettlementReference, in.IdempotencyKey, requestHash, in.CorrelationID, in.SourceSystem, time.Now().UTC(), in.WalletID, in.EvidenceID).Scan(&transactionUUID)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) VALUES($1,$2,$3,'debit',$4,$5),($6,$2,$7,'credit',$4,$5)`, newID("ent"), transactionUUID, bankAccount, amount.StringFixed(2), in.Currency, newID("ent"), walletAccount)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'wallet.deposit.settled','journal_transaction',$2::varchar,jsonb_build_object('transaction_id',$2::varchar,'wallet_id',$3::text,'amount',$4::text,'currency',$5::text,'settlement_reference',$6::text,'evidence_id',$7::text))`, newID("evt"), result.ID, in.WalletID, amount.StringFixed(2), in.Currency, in.SettlementReference, in.EvidenceID)
	if err != nil {
		return Transaction{}, err
	}
	return result, tx.Commit(ctx)
}
