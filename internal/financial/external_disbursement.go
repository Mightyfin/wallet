package financial

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// ConfirmedBankDisbursement is populated by the trusted reconciliation adapter
// from a verified Payment Rails record, never directly from tenant request JSON.
// WalletID identifies the borrower; it is not a destination for a cash credit.
type ConfirmedBankDisbursement struct {
	LegalEntityID, TenantID, Environment, WalletID, PartyID           string
	FacilityID, PaymentID, AuthorizationID                            string
	SourceAccountID, DestinationAccountID, EvidenceDigest, ReviewedBy string
	AmountMinor                                                       int64
	Currency                                                          string
	PaidAt                                                            time.Time
}

func (in ConfirmedBankDisbursement) validate(now time.Time) error {
	for _, id := range []string{in.LegalEntityID, in.TenantID, in.WalletID, in.PartyID, in.FacilityID, in.PaymentID, in.AuthorizationID, in.SourceAccountID, in.DestinationAccountID, in.ReviewedBy} {
		if strings.TrimSpace(id) == "" || len(id) > 128 {
			return ErrConflict
		}
	}
	if (in.Environment != "sandbox" && in.Environment != "production") || in.AmountMinor <= 0 || len(in.Currency) != 3 || strings.Trim(in.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" || len(in.EvidenceDigest) != 64 || strings.Trim(in.EvidenceDigest, "0123456789abcdef") != "" || in.PaidAt.IsZero() || in.PaidAt.After(now) {
		return ErrConflict
	}
	return nil
}

// RecordConfirmedBankDisbursement records an already executed bank payment:
// debit loan receivable, credit bank clearing. It neither moves bank funds nor
// increases wallet_available. Bank clearing is reconciled with external bank
// statements; this posting is not evidence of live provider certification.
func (s *Service) RecordConfirmedBankDisbursement(ctx context.Context, in ConfirmedBankDisbursement) (Transaction, error) {
	if err := in.validate(time.Now().UTC()); err != nil {
		return Transaction{}, err
	}
	in.PaidAt = in.PaidAt.UTC()
	encoded, err := json.Marshal(in)
	if err != nil {
		return Transaction{}, err
	}
	hash := hashRequest(string(encoded))
	const source = "manual-bank-reconciliation"
	amount := decimal.NewFromInt(in.AmountMinor).Shift(-2)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)
	// One verified payment is one financial effect, even with simultaneous
	// requests, different tenants, or a caller attempting a different retry key.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, source+"/"+in.PaymentID); err != nil {
		return Transaction{}, err
	}
	var existing Transaction
	var existingHash, entity, tenant, existingAmount string
	err = tx.QueryRow(ctx, `SELECT jt.public_id,jt.status,jt.currency,jt.idempotency_key,jt.correlation_id,jt.request_hash,le.public_id,jt.tenant_id,
 (SELECT SUM(amount)::text FROM journal_entries WHERE transaction_id=jt.id AND side='debit')
 FROM journal_transactions jt JOIN legal_entities le ON le.id=jt.legal_entity_id WHERE jt.source_system=$1 AND jt.external_reference=$2`, source, in.PaymentID).Scan(&existing.ID, &existing.Status, &existing.Currency, &existing.IdempotencyKey, &existing.CorrelationID, &existingHash, &entity, &tenant, &existingAmount)
	if err == nil {
		if existingHash != hash || entity != in.LegalEntityID || tenant != in.TenantID {
			return Transaction{}, ErrConflict
		}
		existing.Amount, err = decimal.NewFromString(existingAmount)
		return existing, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, err
	}
	var entityUUID, receivable, bank string
	err = tx.QueryRow(ctx, `SELECT le.id::text,ra.id::text,ba.id::text FROM legal_entities le
 JOIN wallets w ON w.legal_entity_id=le.id
 JOIN ledger_accounts ra ON ra.legal_entity_id=le.id AND ra.currency=w.currency AND ra.account_purpose='loan_receivable' AND ra.account_class='asset' AND ra.normal_side='debit' AND ra.status='active'
 JOIN ledger_accounts ba ON ba.legal_entity_id=le.id AND ba.currency=w.currency AND ba.account_purpose='bank_clearing' AND ba.account_class='asset' AND ba.normal_side='debit' AND ba.status='active'
 WHERE le.public_id=$1 AND le.status='active' AND w.public_id=$2 AND w.tenant_id=$3 AND w.owner_id=$4 AND w.currency=$5
 FOR UPDATE OF ra,ba`, in.LegalEntityID, in.WalletID, in.TenantID, in.PartyID, in.Currency).Scan(&entityUUID, &receivable, &bank)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	result := Transaction{ID: newID("txn"), Status: "posted", Currency: in.Currency, IdempotencyKey: in.PaymentID, CorrelationID: in.PaymentID, Amount: amount}
	metadata, err := json.Marshal(map[string]any{"facility_id": in.FacilityID, "payment_id": in.PaymentID, "authorization_id": in.AuthorizationID, "wallet_id": in.WalletID, "party_id": in.PartyID, "source_account_id": in.SourceAccountID, "destination_account_id": in.DestinationAccountID, "evidence_digest": in.EvidenceDigest, "reviewed_by": in.ReviewedBy, "environment": in.Environment, "method": "manual_bank"})
	if err != nil {
		return Transaction{}, err
	}
	var transactionUUID string
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,external_reference,idempotency_key,request_hash,correlation_id,source_system,effective_at,metadata)
 VALUES($1,$2,$3,'loan_disbursement','loan.disbursement.external_bank','posted',$4,$5,$5,$6,$5,$7,$8,$9) RETURNING id::text`, result.ID, entityUUID, in.TenantID, in.Currency, in.PaymentID, hash, source, in.PaidAt, metadata).Scan(&transactionUUID)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) VALUES($1,$2,$3,'debit',$4,$5),($6,$2,$7,'credit',$4,$5)`, newID("ent"), transactionUUID, receivable, amount.StringFixed(2), in.Currency, newID("ent"), bank)
	if err != nil {
		return Transaction{}, err
	}
	payload, err := json.Marshal(map[string]any{"transaction_id": result.ID, "tenant_id": in.TenantID, "environment": in.Environment, "facility_id": in.FacilityID, "payment_id": in.PaymentID, "authorization_id": in.AuthorizationID, "wallet_id": in.WalletID, "amount": amount.StringFixed(2), "currency": in.Currency, "method": "manual_bank", "paid_at": in.PaidAt})
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'loan.external_disbursement.posted','journal_transaction',$2,$3)`, newID("evt"), result.ID, payload)
	if err != nil {
		return Transaction{}, err
	}
	return result, tx.Commit(ctx)
}
