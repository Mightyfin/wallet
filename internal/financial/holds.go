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

type HoldRequest struct {
	LegalEntityID, TenantID, WalletID, Amount, Currency, Reason, IdempotencyKey string
	ExpiresAt                                                                   *time.Time
}

type Hold struct {
	ID        string     `json:"id"`
	WalletID  string     `json:"wallet_id"`
	Amount    string     `json:"amount"`
	Currency  string     `json:"currency"`
	Reason    string     `json:"reason"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (s *Service) CreateHold(ctx context.Context, in HoldRequest) (Hold, error) {
	amount, err := decimal.NewFromString(in.Amount)
	if err != nil || !amount.IsPositive() || amount.Exponent() < -2 {
		return Hold{}, fmt.Errorf("invalid amount: %w", ErrConflict)
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.LegalEntityID == "" || in.TenantID == "" || in.WalletID == "" || in.Reason == "" || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128 || len(in.Currency) != 3 {
		return Hold{}, ErrConflict
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Hold{}, err
	}
	defer tx.Rollback(ctx)
	var existing Hold
	err = tx.QueryRow(ctx, `SELECT h.public_id,w.public_id,h.amount::text,h.currency,h.reason,h.status,h.expires_at
		FROM balance_holds h JOIN wallets w ON w.id=h.wallet_id JOIN legal_entities le ON le.id=w.legal_entity_id
		WHERE le.public_id=$1 AND w.tenant_id=$2 AND w.public_id=$3 AND h.idempotency_key=$4`, in.LegalEntityID, in.TenantID, in.WalletID, in.IdempotencyKey).Scan(&existing.ID, &existing.WalletID, &existing.Amount, &existing.Currency, &existing.Reason, &existing.Status, &existing.ExpiresAt)
	if err == nil {
		if existing.Amount != amount.StringFixed(2) || existing.Currency != in.Currency || existing.Reason != in.Reason {
			return Hold{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Hold{}, err
	}
	var walletUUID, accountUUID string
	err = tx.QueryRow(ctx, `SELECT w.id::text,a.id::text FROM wallets w JOIN legal_entities le ON le.id=w.legal_entity_id JOIN ledger_accounts a ON a.wallet_id=w.id AND a.account_purpose='wallet_available' WHERE le.public_id=$1 AND w.tenant_id=$2 AND w.public_id=$3 AND w.currency=$4 AND w.status='active' FOR UPDATE OF w,a`, in.LegalEntityID, in.TenantID, in.WalletID, in.Currency).Scan(&walletUUID, &accountUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Hold{}, ErrNotFound
	}
	if err != nil {
		return Hold{}, err
	}
	var availableText string
	err = tx.QueryRow(ctx, `SELECT (COALESCE(SUM(CASE WHEN e.side=a.normal_side THEN e.amount ELSE -e.amount END),0)-COALESCE((SELECT SUM(h.amount) FROM balance_holds h WHERE h.wallet_id=$1 AND h.status='active' AND (h.expires_at IS NULL OR h.expires_at>now())),0))::text FROM ledger_accounts a LEFT JOIN journal_entries e ON e.account_id=a.id WHERE a.id=$2 GROUP BY a.id`, walletUUID, accountUUID).Scan(&availableText)
	if err != nil {
		return Hold{}, err
	}
	available, _ := decimal.NewFromString(availableText)
	if available.LessThan(amount) {
		return Hold{}, ErrInsufficientBalance
	}
	hold := Hold{ID: newID("hld"), WalletID: in.WalletID, Amount: amount.StringFixed(2), Currency: in.Currency, Reason: in.Reason, Status: "active", ExpiresAt: in.ExpiresAt}
	_, err = tx.Exec(ctx, `INSERT INTO balance_holds(public_id,wallet_id,amount,currency,reason,status,idempotency_key,expires_at) VALUES($1,$2,$3,$4,$5,'active',$6,$7)`, hold.ID, walletUUID, hold.Amount, hold.Currency, hold.Reason, in.IdempotencyKey, in.ExpiresAt)
	if err != nil {
		return Hold{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'wallet.hold.created','balance_hold',$2::varchar,jsonb_build_object('hold_id',$2::varchar,'wallet_id',$3::text,'amount',$4::text,'currency',$5::text))`, newID("evt"), hold.ID, hold.WalletID, hold.Amount, hold.Currency)
	if err != nil {
		return Hold{}, err
	}
	return hold, tx.Commit(ctx)
}

func (s *Service) ReleaseHold(ctx context.Context, legalEntityID, tenantID, walletID, holdID string) (Hold, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Hold{}, err
	}
	defer tx.Rollback(ctx)
	var hold Hold
	err = tx.QueryRow(ctx, `UPDATE balance_holds h SET status='released',updated_at=now() FROM wallets w,legal_entities le
		WHERE h.wallet_id=w.id AND le.id=w.legal_entity_id AND le.public_id=$1 AND w.tenant_id=$2 AND w.public_id=$3 AND h.public_id=$4 AND h.status='active'
		RETURNING h.public_id,w.public_id,h.amount::text,h.currency,h.reason,h.status,h.expires_at`, legalEntityID, tenantID, walletID, holdID).Scan(&hold.ID, &hold.WalletID, &hold.Amount, &hold.Currency, &hold.Reason, &hold.Status, &hold.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Hold{}, ErrNotFound
	}
	if err != nil {
		return Hold{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'wallet.hold.released','balance_hold',$2::varchar,jsonb_build_object('hold_id',$2::varchar,'wallet_id',$3::text))`, newID("evt"), hold.ID, hold.WalletID)
	if err != nil {
		return Hold{}, err
	}
	return hold, tx.Commit(ctx)
}
