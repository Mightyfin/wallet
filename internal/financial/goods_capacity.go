package financial

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GoodsCapacity reports committed use, not cash, debt after repayments, or a
// promise that a subsequent draw will pass current account/eligibility checks.
type GoodsCapacity struct {
	AuthorizationID     string    `json:"authorization_id"`
	TenantID            string    `json:"tenant_id"`
	LegalEntityID       string    `json:"legal_entity_id"`
	FacilityID          string    `json:"facility_id"`
	Currency            string    `json:"currency"`
	ApprovedAmount      string    `json:"approved_amount"`
	UsedAmount          string    `json:"used_amount"`
	RemainingAmount     string    `json:"remaining_amount"`
	ExpiredUnusedAmount string    `json:"expired_unused_amount"`
	UseCount            int64     `json:"use_count"`
	AllowPartialUse     bool      `json:"allow_partial_use"`
	State               string    `json:"state"`
	ExpiresAt           time.Time `json:"expires_at"`
	AsOf                time.Time `json:"as_of"`
}

func (s *Service) ReadGoodsCapacity(ctx context.Context, tenant, entity, id string) (GoodsCapacity, error) {
	var out GoodsCapacity
	// One statement/snapshot keeps the count, sum and remainder consistent during
	// concurrent posting. Failed/uncommitted uses are never included. Expiry uses
	// the database clock; expired unused credit is retained for reconciliation.
	err := s.pool.QueryRow(ctx, `WITH capacity AS (
 SELECT a.id,a.tenant_id,le.public_id AS entity,r.facility_id,a.currency,a.amount,
 a.allow_partial_use,a.expires_at,statement_timestamp() AS as_of,
 coalesce(u.used,0) AS used,coalesce(u.n,0) AS n
 FROM goods_credit_authorizations a
 JOIN lender_liquidity_reservations r ON r.id=a.reservation_id
 JOIN ledger_accounts la ON la.id=r.account_id
 JOIN legal_entities le ON le.id=la.legal_entity_id
 LEFT JOIN LATERAL (SELECT sum(amount) AS used,count(*) AS n FROM goods_credit_uses WHERE authorization_id=a.id) u ON true
 WHERE a.id=$1 AND a.tenant_id=$2 AND le.public_id=$3
 ) SELECT id,tenant_id,entity,facility_id,currency,amount::text,used::numeric(20,2)::text,
 (CASE WHEN expires_at<=as_of THEN 0 ELSE amount-used END)::numeric(20,2)::text,
 (CASE WHEN expires_at<=as_of THEN amount-used ELSE 0 END)::numeric(20,2)::text,
 n,allow_partial_use,CASE WHEN used=amount THEN 'exhausted' WHEN expires_at<=as_of THEN 'expired' ELSE 'active' END,
 expires_at,as_of FROM capacity`, id, tenant, entity).Scan(
		&out.AuthorizationID, &out.TenantID, &out.LegalEntityID, &out.FacilityID, &out.Currency,
		&out.ApprovedAmount, &out.UsedAmount, &out.RemainingAmount, &out.ExpiredUnusedAmount,
		&out.UseCount, &out.AllowPartialUse, &out.State, &out.ExpiresAt, &out.AsOf)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}
