package financial

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// SandboxFunding is an explicitly synthetic credit used only by the sandbox
// API. It is kept separate from settled deposits so test money can never be
// mistaken for externally reconciled funds.
type SandboxFunding struct {
	LegalEntityID, TenantID, WalletID, PartnerReference           string
	Amount, Currency, IdempotencyKey, CorrelationID, SourceSystem string
}

type WalletTransaction struct {
	ID                   string    `json:"id"`
	Type                 string    `json:"type"`
	Status               string    `json:"status"`
	Amount               string    `json:"amount"`
	Currency             string    `json:"currency"`
	WalletID             string    `json:"wallet_id"`
	CounterpartyWalletID *string   `json:"counterparty_wallet_id,omitempty"`
	PartnerReference     string    `json:"partner_reference"`
	CreatedAt            time.Time `json:"created_at"`
}

func (s *Service) FundSandboxWallet(ctx context.Context, in SandboxFunding) (Transaction, error) {
	amount, err := decimal.NewFromString(in.Amount)
	if err != nil || !amount.IsPositive() || amount.Exponent() < -2 {
		return Transaction{}, ErrConflict
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.LegalEntityID == "" || in.TenantID == "" || in.WalletID == "" || in.PartnerReference == "" || in.IdempotencyKey == "" || len(in.Currency) != 3 {
		return Transaction{}, ErrConflict
	}
	if in.CorrelationID == "" {
		in.CorrelationID = newID("cor")
	}
	if in.SourceSystem == "" {
		in.SourceSystem = "efaas-sandbox"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)
	requestHash := hashRequest(in.WalletID, in.PartnerReference, amount.StringFixed(2), in.Currency)
	var existing Transaction
	var existingHash, existingAmount string
	err = tx.QueryRow(ctx, `SELECT jt.public_id,jt.status,jt.currency,jt.idempotency_key,jt.correlation_id,jt.request_hash,
		(SELECT COALESCE(SUM(amount),0)::text FROM journal_entries WHERE transaction_id=jt.id AND side='debit')
		FROM journal_transactions jt JOIN legal_entities le ON le.id=jt.legal_entity_id
		WHERE le.public_id=$1 AND jt.tenant_id=$2 AND jt.source_system=$3 AND jt.idempotency_key=$4`,
		in.LegalEntityID, in.TenantID, in.SourceSystem, in.IdempotencyKey).
		Scan(&existing.ID, &existing.Status, &existing.Currency, &existing.IdempotencyKey, &existing.CorrelationID, &existingHash, &existingAmount)
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
	err = tx.QueryRow(ctx, `SELECT le.id::text,ba.id::text,wa.id::text,w.currency
		FROM legal_entities le JOIN wallets w ON w.legal_entity_id=le.id
		JOIN ledger_accounts ba ON ba.legal_entity_id=le.id AND ba.account_purpose='bank_clearing' AND ba.currency=w.currency
		JOIN ledger_accounts wa ON wa.wallet_id=w.id AND wa.account_purpose='wallet_available'
		WHERE le.public_id=$1 AND w.tenant_id=$2 AND w.public_id=$3 AND le.status='active' AND w.status='active'
		FOR UPDATE OF ba,wa,w`, in.LegalEntityID, in.TenantID, in.WalletID).
		Scan(&entityUUID, &bankAccount, &walletAccount, &walletCurrency)
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
	err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(
		public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,
		external_reference,idempotency_key,request_hash,correlation_id,source_system,effective_at,metadata)
		VALUES($1,$2,$3,'sandbox_funding','sandbox.wallet.funding','posted',$4,$5,$6,$7,$8,$9,$10,
		jsonb_build_object('wallet_id',$11::text,'synthetic',true)) RETURNING id::text`,
		result.ID, entityUUID, in.TenantID, in.Currency, in.PartnerReference, in.IdempotencyKey,
		requestHash, in.CorrelationID, in.SourceSystem, time.Now().UTC(), in.WalletID).Scan(&transactionUUID)
	if err != nil {
		return Transaction{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency)
		VALUES($1,$2,$3,'debit',$4,$5),($6,$2,$7,'credit',$4,$5)`,
		newID("ent"), transactionUUID, bankAccount, amount.StringFixed(2), in.Currency,
		newID("ent"), walletAccount); err != nil {
		return Transaction{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload)
		VALUES($1,'sandbox.wallet.funded','journal_transaction',$2::varchar,
		jsonb_build_object('transaction_id',$2::varchar,'wallet_id',$3::text,'amount',$4::text,'currency',$5::text,'synthetic',true))`,
		newID("evt"), result.ID, in.WalletID, amount.StringFixed(2), in.Currency); err != nil {
		return Transaction{}, err
	}
	return result, tx.Commit(ctx)
}

func (s *Service) WalletTransactions(ctx context.Context, legalEntityID, tenantID, walletID, cursor string, limit int) ([]WalletTransaction, bool, error) {
	if legalEntityID == "" || tenantID == "" || walletID == "" || limit < 1 || limit > 100 {
		return nil, false, ErrConflict
	}
	rows, err := s.pool.Query(ctx, `SELECT jt.public_id,
		CASE
		  WHEN jt.transaction_type='sandbox_funding' THEN 'deposit'
		  WHEN jt.transaction_type='wallet_transfer' AND own.side='debit' THEN 'transfer_out'
		  WHEN jt.transaction_type='wallet_transfer' THEN 'transfer_in'
		  ELSE jt.transaction_type
		END,
		jt.status,own.amount::numeric(20,2)::text,jt.currency,w.public_id,counter_wallet.public_id,
		COALESCE(jt.external_reference,''),jt.booked_at
		FROM wallets w
		JOIN legal_entities le ON le.id=w.legal_entity_id
		JOIN ledger_accounts own_account ON own_account.wallet_id=w.id AND own_account.account_purpose='wallet_available'
		JOIN journal_entries own ON own.account_id=own_account.id
		JOIN journal_transactions jt ON jt.id=own.transaction_id
		LEFT JOIN journal_entries counter_entry ON counter_entry.transaction_id=jt.id AND counter_entry.id<>own.id
		LEFT JOIN ledger_accounts counter_account ON counter_account.id=counter_entry.account_id
		LEFT JOIN wallets counter_wallet ON counter_wallet.id=counter_account.wallet_id
		WHERE le.public_id=$1 AND w.tenant_id=$2 AND w.public_id=$3
		  AND ($4='' OR (jt.booked_at,jt.public_id) < (SELECT booked_at,public_id FROM journal_transactions WHERE public_id=$4))
		ORDER BY jt.booked_at DESC,jt.public_id DESC LIMIT $5`, legalEntityID, tenantID, walletID, cursor, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := make([]WalletTransaction, 0, limit)
	for rows.Next() {
		var item WalletTransaction
		if err = rows.Scan(&item.ID, &item.Type, &item.Status, &item.Amount, &item.Currency, &item.WalletID,
			&item.CounterpartyWalletID, &item.PartnerReference, &item.CreatedAt); err != nil {
			return nil, false, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}
