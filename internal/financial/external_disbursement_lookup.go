package financial

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// FindConfirmedBankDisbursement recovers an existing journal only. It never
// creates a posting, reserves funds or requires currently active bank accounts.
// The exact original command hash must match, including its evidence digest.
func (s *Service) FindConfirmedBankDisbursement(ctx context.Context, in ConfirmedBankDisbursement) (Transaction, error) {
	if err := in.validate(time.Now().UTC()); err != nil {
		return Transaction{}, err
	}
	in.PaidAt = in.PaidAt.UTC()
	encoded, err := json.Marshal(in)
	if err != nil {
		return Transaction{}, err
	}
	var out Transaction
	var storedHash, amount string
	err = s.pool.QueryRow(ctx, `SELECT jt.public_id,jt.status,jt.currency,jt.idempotency_key,jt.correlation_id,jt.request_hash,
 (SELECT SUM(amount)::text FROM journal_entries WHERE transaction_id=jt.id AND side='debit')
 FROM journal_transactions jt JOIN legal_entities le ON le.id=jt.legal_entity_id
 WHERE jt.source_system='manual-bank-reconciliation' AND jt.external_reference=$1
 AND le.public_id=$2 AND jt.tenant_id=$3 AND jt.metadata->>'environment'=$4`, in.PaymentID, in.LegalEntityID, in.TenantID, in.Environment).Scan(&out.ID, &out.Status, &out.Currency, &out.IdempotencyKey, &out.CorrelationID, &storedHash, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transaction{}, ErrNotFound
	}
	if err != nil {
		return Transaction{}, err
	}
	if storedHash != hashRequest(string(encoded)) || out.Status != "posted" || out.Currency != in.Currency || out.IdempotencyKey != in.PaymentID {
		return Transaction{}, ErrConflict
	}
	out.Amount, err = decimal.NewFromString(amount)
	if err != nil || !out.Amount.Equal(decimal.NewFromInt(in.AmountMinor).Shift(-2)) {
		return Transaction{}, ErrConflict
	}
	return out, nil
}
