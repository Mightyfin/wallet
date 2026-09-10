package financial

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GoodsUseRecord is the immutable committed result used for retry recovery.
type GoodsUseRecord struct {
	UseID               string    `json:"use_id"`
	AuthorizationID     string    `json:"authorization_id"`
	TenantID            string    `json:"tenant_id"`
	LegalEntityID       string    `json:"legal_entity_id"`
	FacilityID          string    `json:"facility_id"`
	BorrowerPartyID     string    `json:"borrower_party_id"`
	SupplierPartyID     string    `json:"supplier_party_id"`
	DestinationWalletID string    `json:"destination_wallet_id"`
	OrderReference      string    `json:"order_reference"`
	Amount              string    `json:"amount"`
	Currency            string    `json:"currency"`
	TransactionID       string    `json:"transaction_id"`
	CorrelationID       string    `json:"correlation_id"`
	Status              string    `json:"status"`
	RequestedBy         string    `json:"requested_by"`
	PostedAt            time.Time `json:"posted_at"`
}

func (s *Service) ReadGoodsUse(ctx context.Context, tenant, entity, authorization, useID string) (GoodsUseRecord, error) {
	var out GoodsUseRecord
	err := s.pool.QueryRow(ctx, `SELECT u.use_id,a.id,a.tenant_id,le.public_id,r.facility_id,
 a.borrower_party_id,a.supplier_party_id,w.public_id,a.order_reference,u.amount::text,
 a.currency,j.public_id,j.correlation_id,j.status,u.requested_by,j.effective_at
 FROM goods_credit_uses u JOIN goods_credit_authorizations a ON a.id=u.authorization_id
 JOIN lender_liquidity_reservations r ON r.id=a.reservation_id
 JOIN ledger_accounts la ON la.id=r.account_id JOIN legal_entities le ON le.id=la.legal_entity_id
 JOIN wallets w ON w.id=a.destination_wallet_id JOIN journal_transactions j ON j.id=u.transaction_id
 WHERE u.tenant_id=$1 AND le.public_id=$2 AND a.id=$3 AND u.use_id=$4`, tenant, entity, authorization, useID).Scan(
		&out.UseID, &out.AuthorizationID, &out.TenantID, &out.LegalEntityID, &out.FacilityID,
		&out.BorrowerPartyID, &out.SupplierPartyID, &out.DestinationWalletID, &out.OrderReference,
		&out.Amount, &out.Currency, &out.TransactionID, &out.CorrelationID, &out.Status, &out.RequestedBy, &out.PostedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}
