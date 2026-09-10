package financial

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type LiquidityVerification struct {
	Reservation   LiquidityReservation `json:"reservation"`
	LegalEntityID string               `json:"legal_entity_id"`
	Environment   string               `json:"environment"`
	SourceActive  bool                 `json:"source_active"`
	VerifiedAt    time.Time            `json:"verified_at"`
}

// ReadLenderLiquidity returns historical reservation facts and current source
// status. It never creates/replays a reservation or reports spendable credit.
func (s *Service) ReadLenderLiquidity(ctx context.Context, tenant, legalEntity, id string) (LiquidityVerification, error) {
	var out LiquidityVerification
	for _, value := range []string{tenant, legalEntity, id} {
		if value == "" || strings.TrimSpace(value) != value {
			return out, ErrConflict
		}
	}
	err := s.pool.QueryRow(ctx, `SELECT r.public_id,a.public_id,r.tenant_id,r.facility_id,r.authorization_id,r.amount::text,r.currency,r.status,le.public_id,
 (a.status='active' AND le.status='active'),clock_timestamp()
 FROM lender_liquidity_reservations r JOIN ledger_accounts a ON a.id=r.account_id
 JOIN legal_entities le ON le.id=a.legal_entity_id
 WHERE r.public_id=$1 AND r.tenant_id=$2 AND le.public_id=$3`, id, tenant, legalEntity).Scan(
		&out.Reservation.ID, &out.Reservation.AccountID, &out.Reservation.TenantID, &out.Reservation.FacilityID, &out.Reservation.AuthorizationID, &out.Reservation.Amount, &out.Reservation.Currency, &out.Reservation.Status, &out.LegalEntityID, &out.SourceActive, &out.VerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}
