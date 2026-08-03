package financial

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type WalletRepayment struct {
	LegalEntityID, TenantID, WalletID, FacilityID                 string
	Amount, Currency, IdempotencyKey, CorrelationID, SourceSystem string
}

type SettledExternalRepayment struct {
	LegalEntityID, TenantID, WalletID, FacilityID, SettlementReference, EvidenceID string
	Amount, Currency, IdempotencyKey, CorrelationID, SourceSystem                  string
}

// RepayLoanFromWallet posts only the financial effect. Allocation to contractual
// principal, interest and fees remains a lending-domain decision supplied later
// through approved posting rules.
func (s *Service) RepayLoanFromWallet(ctx context.Context, in WalletRepayment) (Transaction, error) {
	amount, err := parsePostingAmount(in.Amount)
	if err != nil {
		return Transaction{}, fmt.Errorf("invalid amount: %w", ErrConflict)
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.LegalEntityID == "" || in.TenantID == "" || in.WalletID == "" || in.FacilityID == "" || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128 || len(in.Currency) != 3 {
		return Transaction{}, ErrConflict
	}
	if in.CorrelationID == "" {
		in.CorrelationID = newID("cor")
	}
	if in.SourceSystem == "" {
		in.SourceSystem = "lending-service"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)
	requestHash := hashRequest(in.WalletID, in.FacilityID, amount.StringFixed(2), in.Currency)
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
	var entityUUID, walletUUID, walletAccount, receivableAccount, walletCurrency string
	err = tx.QueryRow(ctx, `SELECT le.id::text,w.id::text,wa.id::text,ra.id::text,w.currency FROM legal_entities le JOIN wallets w ON w.legal_entity_id=le.id JOIN ledger_accounts wa ON wa.wallet_id=w.id AND wa.account_purpose='wallet_available' JOIN ledger_accounts ra ON ra.legal_entity_id=le.id AND ra.account_purpose='loan_receivable' AND ra.currency=w.currency WHERE le.public_id=$1 AND w.tenant_id=$2 AND w.public_id=$3 AND le.status='active' AND w.status='active' FOR UPDATE OF w,wa,ra`, in.LegalEntityID, in.TenantID, in.WalletID).Scan(&entityUUID, &walletUUID, &walletAccount, &receivableAccount, &walletCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	if walletCurrency != in.Currency {
		return Transaction{}, ErrConflict
	}
	var availableText string
	err = tx.QueryRow(ctx, `SELECT (COALESCE(SUM(CASE WHEN e.side=a.normal_side THEN e.amount ELSE -e.amount END),0)-COALESCE((SELECT SUM(h.amount) FROM balance_holds h WHERE h.wallet_id=$1 AND h.status='active' AND (h.expires_at IS NULL OR h.expires_at>now())),0))::text FROM ledger_accounts a LEFT JOIN journal_entries e ON e.account_id=a.id WHERE a.id=$2 GROUP BY a.id`, walletUUID, walletAccount).Scan(&availableText)
	if err != nil {
		return Transaction{}, err
	}
	available, _ := decimal.NewFromString(availableText)
	if available.LessThan(amount) {
		return Transaction{}, ErrInsufficientBalance
	}
	result := Transaction{ID: newID("txn"), Status: "posted", Currency: in.Currency, IdempotencyKey: in.IdempotencyKey, CorrelationID: in.CorrelationID, Amount: amount}
	var transactionUUID string
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,external_reference,idempotency_key,request_hash,correlation_id,source_system,effective_at,metadata) VALUES($1,$2,$3,'loan_repayment','loan.repayment.wallet','posted',$4,NULL,$5,$6,$7,$8,$9,jsonb_build_object('facility_id',$10::varchar)) RETURNING id::text`, result.ID, entityUUID, in.TenantID, in.Currency, in.IdempotencyKey, requestHash, in.CorrelationID, in.SourceSystem, time.Now().UTC(), in.FacilityID).Scan(&transactionUUID)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) VALUES($1,$2,$3,'debit',$4,$5),($6,$2,$7,'credit',$4,$5)`, newID("ent"), transactionUUID, walletAccount, amount.StringFixed(2), in.Currency, newID("ent"), receivableAccount)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'loan.repayment.posted','journal_transaction',$2::varchar,jsonb_build_object('transaction_id',$2::varchar,'facility_id',$3::text,'wallet_id',$4::text,'amount',$5::text,'currency',$6::text,'method','wallet'))`, newID("evt"), result.ID, in.FacilityID, in.WalletID, amount.StringFixed(2), in.Currency)
	if err != nil {
		return Transaction{}, err
	}
	return result, tx.Commit(ctx)
}

// RecordSettledExternalRepayment records only externally confirmed bank,
// payroll/MOU or mobile-money receipts supplied by reconciliation.
func (s *Service) RecordSettledExternalRepayment(ctx context.Context, in SettledExternalRepayment) (Transaction, error) {
	amount, err := parsePostingAmount(in.Amount)
	if err != nil {
		return Transaction{}, ErrConflict
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.LegalEntityID == "" || in.TenantID == "" || in.WalletID == "" || in.FacilityID == "" || in.SettlementReference == "" || in.EvidenceID == "" || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128 || len(in.Currency) != 3 {
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
	requestHash := hashRequest(in.WalletID, in.FacilityID, in.SettlementReference, in.EvidenceID, amount.StringFixed(2), in.Currency)
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
	var entityUUID, bankAccount, receivableAccount, walletCurrency string
	err = tx.QueryRow(ctx, `SELECT le.id::text,ba.id::text,ra.id::text,w.currency FROM legal_entities le JOIN wallets w ON w.legal_entity_id=le.id JOIN ledger_accounts ba ON ba.legal_entity_id=le.id AND ba.account_purpose='bank_clearing' AND ba.currency=w.currency JOIN ledger_accounts ra ON ra.legal_entity_id=le.id AND ra.account_purpose='loan_receivable' AND ra.currency=w.currency WHERE le.public_id=$1 AND w.tenant_id=$2 AND w.public_id=$3 AND le.status='active' FOR UPDATE OF ba,ra`, in.LegalEntityID, in.TenantID, in.WalletID).Scan(&entityUUID, &bankAccount, &receivableAccount, &walletCurrency)
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
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,external_reference,idempotency_key,request_hash,correlation_id,source_system,effective_at,metadata) VALUES($1,$2,$3,'loan_repayment','loan.repayment.external_settled','posted',$4,$5,$6,$7,$8,$9,$10,jsonb_build_object('facility_id',$11::text,'wallet_id',$12::text,'evidence_id',$13::text)) RETURNING id::text`, result.ID, entityUUID, in.TenantID, in.Currency, in.SettlementReference, in.IdempotencyKey, requestHash, in.CorrelationID, in.SourceSystem, time.Now().UTC(), in.FacilityID, in.WalletID, in.EvidenceID).Scan(&transactionUUID)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) VALUES($1,$2,$3,'debit',$4,$5),($6,$2,$7,'credit',$4,$5)`, newID("ent"), transactionUUID, bankAccount, amount.StringFixed(2), in.Currency, newID("ent"), receivableAccount)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'loan.repayment.posted','journal_transaction',$2::varchar,jsonb_build_object('transaction_id',$2::varchar,'facility_id',$3::text,'wallet_id',$4::text,'amount',$5::text,'currency',$6::text,'method','external_settled','settlement_reference',$7::text,'evidence_id',$8::text))`, newID("evt"), result.ID, in.FacilityID, in.WalletID, amount.StringFixed(2), in.Currency, in.SettlementReference, in.EvidenceID)
	if err != nil {
		return Transaction{}, err
	}
	return result, tx.Commit(ctx)
}
