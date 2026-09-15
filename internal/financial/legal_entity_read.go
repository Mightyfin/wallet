package financial

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// LegalEntityRecord returns registry identity only, never liquidity or payment
// authority. Deployment-level database isolation supplies the environment.
func (s *Service) LegalEntityRecord(ctx context.Context, id string) (LegalEntity, error) {
	if id == "" || len(id) > 64 || strings.TrimSpace(id) != id {
		return LegalEntity{}, ErrNotFound
	}
	var entity LegalEntity
	err := s.pool.QueryRow(ctx, `SELECT public_id,name,country_code,base_currency,status FROM legal_entities WHERE public_id=$1`, id).Scan(&entity.ID, &entity.Name, &entity.CountryCode, &entity.BaseCurrency, &entity.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return LegalEntity{}, ErrNotFound
	}
	return entity, err
}
