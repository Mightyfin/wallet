package financial

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GoodsAuthorizationRecord is persisted authority, not proof of a draw or a
// current credit decision. Reads remain valid after expiry for retry recovery.
type GoodsAuthorizationRecord struct {
	ID                  string    `json:"authorization_id"`
	TenantID            string    `json:"tenant_id"`
	LegalEntityID       string    `json:"legal_entity_id"`
	FacilityID          string    `json:"facility_id"`
	ReservationID       string    `json:"reservation_id"`
	BorrowerPartyID     string    `json:"borrower_party_id"`
	SupplierPartyID     string    `json:"supplier_party_id"`
	DestinationWalletID string    `json:"destination_wallet_id"`
	OrderReference      string    `json:"order_reference"`
	Amount              string    `json:"amount"`
	Currency            string    `json:"currency"`
	AllowPartialUse     bool      `json:"allow_partial_use"`
	ExpiresAt           time.Time `json:"expires_at"`
	RegisteredBy        string    `json:"registered_by"`
}

func (s *Service) ReadGoodsAuthorization(ctx context.Context, tenant, legalEntity, id string) (GoodsAuthorizationRecord, error) {
	var out GoodsAuthorizationRecord
	err := s.pool.QueryRow(ctx, `SELECT a.id,a.tenant_id,le.public_id,r.facility_id,r.public_id,a.borrower_party_id,a.supplier_party_id,w.public_id,a.order_reference,a.amount::text,a.currency,a.allow_partial_use,a.expires_at,a.authorized_by
 FROM goods_credit_authorizations a JOIN lender_liquidity_reservations r ON r.id=a.reservation_id
 JOIN ledger_accounts la ON la.id=r.account_id JOIN legal_entities le ON le.id=la.legal_entity_id
 JOIN wallets w ON w.id=a.destination_wallet_id
 WHERE a.id=$1 AND a.tenant_id=$2 AND le.public_id=$3`, id, tenant, legalEntity).Scan(&out.ID, &out.TenantID, &out.LegalEntityID, &out.FacilityID, &out.ReservationID, &out.BorrowerPartyID, &out.SupplierPartyID, &out.DestinationWalletID, &out.OrderReference, &out.Amount, &out.Currency, &out.AllowPartialUse, &out.ExpiresAt, &out.RegisteredBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}
