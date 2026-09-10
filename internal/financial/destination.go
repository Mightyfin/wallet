package financial

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type DestinationRequest struct{ LegalEntityID, TenantID, WalletID, PartyID, Currency string }
type DestinationVerification struct {
	Environment   string    `json:"environment"`
	WalletID      string    `json:"wallet_id"`
	LegalEntityID string    `json:"legal_entity_id"`
	TenantID      string    `json:"tenant_id"`
	PartyID       string    `json:"party_id"`
	Currency      string    `json:"currency"`
	Verification  string    `json:"verification"`
	VerifiedAt    time.Time `json:"verified_at"`
}

// This is a point-in-time ownership/receivability check, never a reservation or
// payment authorization. Execution must repeat these checks inside its transaction.
func (s *Service) VerifyDestination(ctx context.Context, in DestinationRequest) (DestinationVerification, error) {
	var out DestinationVerification
	for _, v := range []string{in.LegalEntityID, in.TenantID, in.WalletID, in.PartyID, in.Currency} {
		if strings.TrimSpace(v) == "" || strings.TrimSpace(v) != v {
			return out, ErrConflict
		}
	}
	err := s.pool.QueryRow(ctx, `SELECT w.public_id,le.public_id,w.tenant_id,w.owner_id,w.currency,'active_owned_wallet',clock_timestamp() FROM wallets w JOIN legal_entities le ON le.id=w.legal_entity_id WHERE w.public_id=$1 AND le.public_id=$2 AND w.tenant_id=$3 AND w.owner_id=$4 AND w.currency=$5 AND w.status='active' AND le.status='active' AND w.owner_type IN ('customer','organization','partner') AND EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.wallet_id=w.id AND a.legal_entity_id=w.legal_entity_id AND a.currency=w.currency AND a.account_class='liability' AND a.normal_side='credit' AND a.account_purpose='wallet_available' AND a.status='active')`, in.WalletID, in.LegalEntityID, in.TenantID, in.PartyID, in.Currency).Scan(&out.WalletID, &out.LegalEntityID, &out.TenantID, &out.PartyID, &out.Currency, &out.Verification, &out.VerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}
