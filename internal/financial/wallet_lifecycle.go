package financial

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type WalletLifecycleRequest struct {
	LegalEntityID, TenantID, WalletID, TargetStatus, Reason, EvidenceReference string
	ActorSubject, SourceApplication, IdempotencyKey                            string
}

func (s *Service) TransitionWallet(ctx context.Context, in WalletLifecycleRequest) (Wallet, error) {
	in.TargetStatus = strings.ToLower(strings.TrimSpace(in.TargetStatus))
	in.Reason = strings.TrimSpace(in.Reason)
	in.EvidenceReference = strings.TrimSpace(in.EvidenceReference)
	if in.LegalEntityID == "" || in.TenantID == "" || in.WalletID == "" || len(in.Reason) < 20 || len(in.Reason) > 500 || in.EvidenceReference == "" || len(in.EvidenceReference) > 200 || in.ActorSubject == "" || len(in.ActorSubject) > 200 || in.SourceApplication == "" || len(in.SourceApplication) > 100 || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128 {
		return Wallet{}, fmt.Errorf("invalid wallet lifecycle command: %w", ErrConflict)
	}
	requestHash := hashRequest(in.TargetStatus, in.Reason, in.EvidenceReference)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Wallet{}, err
	}
	defer tx.Rollback(ctx)
	var walletUUID, current string
	var result Wallet
	err = tx.QueryRow(ctx, `SELECT w.id::text,w.public_id,le.public_id,w.tenant_id,w.owner_type,w.owner_id,w.currency,w.status FROM wallets w JOIN legal_entities le ON le.id=w.legal_entity_id WHERE w.public_id=$1 AND le.public_id=$2 AND w.tenant_id=$3 FOR UPDATE OF w`, in.WalletID, in.LegalEntityID, in.TenantID).Scan(&walletUUID, &result.ID, &result.LegalEntityID, &result.TenantID, &result.OwnerType, &result.OwnerID, &result.Currency, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return Wallet{}, ErrNotFound
	}
	if err != nil {
		return Wallet{}, err
	}
	var priorHash, priorStatus string
	err = tx.QueryRow(ctx, `SELECT request_hash,to_status FROM wallet_status_history WHERE wallet_id=$1 AND idempotency_key=$2`, walletUUID, in.IdempotencyKey).Scan(&priorHash, &priorStatus)
	if err == nil {
		if priorHash != requestHash {
			return Wallet{}, ErrConflict
		}
		result.Status = priorStatus
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Wallet{}, err
	}
	if !validWalletTransition(current, in.TargetStatus) {
		return Wallet{}, fmt.Errorf("wallet transition %s to %s: %w", current, in.TargetStatus, ErrConflict)
	}
	if in.TargetStatus == "closed" {
		var ledgerText string
		var activeHolds int
		err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN e.side=a.normal_side THEN e.amount ELSE -e.amount END),0)::text,(SELECT count(*) FROM balance_holds h WHERE h.wallet_id=$1 AND h.status='active') FROM ledger_accounts a LEFT JOIN journal_entries e ON e.account_id=a.id WHERE a.wallet_id=$1 AND a.account_purpose='wallet_available' GROUP BY a.id`, walletUUID).Scan(&ledgerText, &activeHolds)
		if err != nil {
			return Wallet{}, err
		}
		ledger, parseErr := decimal.NewFromString(ledgerText)
		if parseErr != nil || !ledger.IsZero() || activeHolds != 0 {
			return Wallet{}, fmt.Errorf("wallet must have zero balance and no active holds before closure: %w", ErrConflict)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE wallets SET status=$2,updated_at=now() WHERE id=$1`, walletUUID, in.TargetStatus)
	if err != nil {
		return Wallet{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wallet_status_history(wallet_id,from_status,to_status,reason,evidence_reference,actor_subject,source_application,idempotency_key,request_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, walletUUID, current, in.TargetStatus, in.Reason, in.EvidenceReference, in.ActorSubject, in.SourceApplication, in.IdempotencyKey, requestHash)
	if err != nil {
		return Wallet{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'wallet.status.changed','wallet',$2::varchar,jsonb_build_object('wallet_id',$2::text,'tenant_id',$3::text,'from_status',$4::text,'to_status',$5::text,'evidence_reference',$6::text))`, newID("evt"), result.ID, in.TenantID, current, in.TargetStatus, in.EvidenceReference)
	if err != nil {
		return Wallet{}, err
	}
	result.Status = in.TargetStatus
	return result, tx.Commit(ctx)
}

func validWalletTransition(from, to string) bool {
	switch from {
	case "pending":
		return to == "active" || to == "closed"
	case "active":
		return to == "restricted" || to == "suspended" || to == "closed"
	case "restricted":
		return to == "active" || to == "suspended" || to == "closed"
	case "suspended":
		return to == "active" || to == "restricted" || to == "closed"
	default:
		return false
	}
}
