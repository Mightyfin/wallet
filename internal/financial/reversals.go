package financial

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type ReversalRequest struct {
	LegalEntityID, TenantID, TransactionID, Reason, ApprovalReference string
	IdempotencyKey, CorrelationID, SourceSystem                       string
}

// ReverseTransaction creates linked opposite entries. It is intentionally not
// exposed by the HTTP API until the operations maker-checker approval workflow exists.
func (s *Service) ReverseTransaction(ctx context.Context, in ReversalRequest) (Transaction, error) {
	if in.LegalEntityID == "" || in.TenantID == "" || in.TransactionID == "" || strings.TrimSpace(in.Reason) == "" || in.ApprovalReference == "" || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128 {
		return Transaction{}, ErrConflict
	}
	if in.CorrelationID == "" {
		in.CorrelationID = newID("cor")
	}
	if in.SourceSystem == "" {
		in.SourceSystem = "operations-service"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)
	requestHash := hashRequest(in.TransactionID, in.Reason, in.ApprovalReference)
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
	var originalUUID, entityUUID, currency, postingRule, amountText string
	err = tx.QueryRow(ctx, `SELECT original.id::text,original.legal_entity_id::text,original.currency,original.posting_rule FROM journal_transactions original JOIN legal_entities le ON le.id=original.legal_entity_id WHERE le.public_id=$1 AND original.tenant_id=$2 AND original.public_id=$3 AND original.reversal_of_id IS NULL AND NOT EXISTS(SELECT 1 FROM journal_transactions reversal WHERE reversal.reversal_of_id=original.id) FOR UPDATE OF original`, in.LegalEntityID, in.TenantID, in.TransactionID).Scan(&originalUUID, &entityUUID, &currency, &postingRule)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount) FILTER(WHERE side='debit'),0)::text FROM journal_entries WHERE transaction_id=$1`, originalUUID).Scan(&amountText); err != nil {
		return Transaction{}, err
	}
	amount, _ := decimal.NewFromString(amountText)
	result := Transaction{ID: newID("txn"), Status: "posted", Currency: currency, IdempotencyKey: in.IdempotencyKey, CorrelationID: in.CorrelationID, Amount: amount}
	var reversalUUID string
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,idempotency_key,request_hash,correlation_id,source_system,effective_at,reversal_of_id,metadata) VALUES($1,$2,$3,'reversal',$4,'posted',$5,$6,$7,$8,$9,$10,$11,jsonb_build_object('reason',$12::text,'approval_reference',$13::text,'original_transaction_id',$14::text)) RETURNING id::text`, result.ID, entityUUID, in.TenantID, "reversal."+postingRule, currency, in.IdempotencyKey, requestHash, in.CorrelationID, in.SourceSystem, time.Now().UTC(), originalUUID, in.Reason, in.ApprovalReference, in.TransactionID).Scan(&reversalUUID)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) SELECT 'ent_'||encode(gen_random_bytes(16),'hex'),$1::uuid,account_id,CASE side WHEN 'debit' THEN 'credit' ELSE 'debit' END,amount,currency FROM journal_entries WHERE transaction_id=$2`, reversalUUID, originalUUID)
	if err != nil {
		return Transaction{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'ledger.transaction.reversed','journal_transaction',$2::varchar,jsonb_build_object('transaction_id',$2::varchar,'original_transaction_id',$3::text,'approval_reference',$4::text))`, newID("evt"), result.ID, in.TransactionID, in.ApprovalReference)
	if err != nil {
		return Transaction{}, err
	}
	return result, tx.Commit(ctx)
}
