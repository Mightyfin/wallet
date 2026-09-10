package financial

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// LiquidityRequest uses an explicit lender asset account. It must never infer
// lender funds from bank clearing or from a customer's wallet_available balance.
type LiquidityRequest struct {
	LegalEntityID, AccountID, TenantID, FacilityID, AuthorizationID, Amount, Currency string
	RequestedBy                                                                       string
}
type LiquidityReservation struct {
	ID              string `json:"id"`
	AccountID       string `json:"account_id"`
	TenantID        string `json:"tenant_id"`
	FacilityID      string `json:"facility_id"`
	AuthorizationID string `json:"authorization_id"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	Status          string `json:"status"`
}

// ReserveLenderLiquidity is an internal primitive. It neither approves credit
// nor creates a ledger balance. Consuming/releasing the reservation requires a
// separate, coordinated settlement implementation; it never expires on a timer.
func (s *Service) ReserveLenderLiquidity(ctx context.Context, in LiquidityRequest) (LiquidityReservation, error) {
	amount, err := decimal.NewFromString(in.Amount)
	if err != nil || !amount.IsPositive() || amount.Exponent() < -2 || amount.Exponent() > 17 || amount.GreaterThan(decimal.RequireFromString("999999999999999999.99")) {
		return LiquidityReservation{}, ErrConflict
	}
	for _, value := range []string{in.LegalEntityID, in.AccountID, in.TenantID, in.FacilityID, in.AuthorizationID, in.Currency, in.RequestedBy} {
		if strings.TrimSpace(value) == "" {
			return LiquidityReservation{}, ErrConflict
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LiquidityReservation{}, err
	}
	defer tx.Rollback(ctx)
	// Serializes authorization reuse even if a retried request switches accounts.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, hashRequest("lender-liquidity", in.TenantID, in.AuthorizationID)); err != nil {
		return LiquidityReservation{}, err
	}
	var old LiquidityReservation
	var owner string
	err = tx.QueryRow(ctx, `SELECT r.public_id,a.public_id,r.tenant_id,r.facility_id,r.authorization_id,r.amount::text,r.currency,r.status,le.public_id FROM lender_liquidity_reservations r JOIN ledger_accounts a ON a.id=r.account_id JOIN legal_entities le ON le.id=a.legal_entity_id WHERE r.tenant_id=$1 AND r.authorization_id=$2`, in.TenantID, in.AuthorizationID).Scan(&old.ID, &old.AccountID, &old.TenantID, &old.FacilityID, &old.AuthorizationID, &old.Amount, &old.Currency, &old.Status, &owner)
	if err == nil {
		if owner != in.LegalEntityID || old.AccountID != in.AccountID || old.FacilityID != in.FacilityID || old.Amount != amount.StringFixed(2) || old.Currency != in.Currency {
			return LiquidityReservation{}, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return LiquidityReservation{}, err
	}
	var accountID string
	err = tx.QueryRow(ctx, `SELECT a.id::text FROM ledger_accounts a JOIN legal_entities le ON le.id=a.legal_entity_id WHERE a.public_id=$1 AND le.public_id=$2 AND a.currency=$3 AND a.account_purpose='lender_liquidity' AND a.account_class='asset' AND a.normal_side='debit' AND a.wallet_id IS NULL AND a.status='active' AND le.status='active' FOR UPDATE OF a`, in.AccountID, in.LegalEntityID, in.Currency).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return LiquidityReservation{}, ErrNotFound
	}
	if err != nil {
		return LiquidityReservation{}, err
	}
	var availableText string
	err = tx.QueryRow(ctx, `SELECT (coalesce((SELECT sum(CASE WHEN side='debit' THEN amount ELSE -amount END) FROM journal_entries WHERE account_id=$1),0)-coalesce((SELECT sum(amount) FROM lender_liquidity_reservations WHERE account_id=$1),0))::text`, accountID).Scan(&availableText)
	if err != nil {
		return LiquidityReservation{}, err
	}
	available, err := decimal.NewFromString(availableText)
	if err != nil {
		return LiquidityReservation{}, err
	}
	if available.LessThan(amount) {
		return LiquidityReservation{}, ErrInsufficientBalance
	}
	out := LiquidityReservation{ID: newID("lqr"), AccountID: in.AccountID, TenantID: in.TenantID, FacilityID: in.FacilityID, AuthorizationID: in.AuthorizationID, Amount: amount.StringFixed(2), Currency: in.Currency, Status: "reserved"}
	if _, err = tx.Exec(ctx, `INSERT INTO lender_liquidity_reservations(public_id,account_id,tenant_id,facility_id,authorization_id,amount,currency,requested_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, out.ID, accountID, out.TenantID, out.FacilityID, out.AuthorizationID, out.Amount, out.Currency, in.RequestedBy); err != nil {
		return LiquidityReservation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'lender.liquidity.reserved','liquidity_reservation',$2::text,jsonb_build_object('reservation_id',$2::text,'account_id',$3::text,'tenant_id',$4::text,'facility_id',$5::text,'authorization_id',$6::text,'amount',$7::text,'currency',$8::text))`, newID("evt"), out.ID, out.AccountID, out.TenantID, out.FacilityID, out.AuthorizationID, out.Amount, out.Currency); err != nil {
		return LiquidityReservation{}, err
	}
	return out, tx.Commit(ctx)
}
