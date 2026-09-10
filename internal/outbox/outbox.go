package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Event struct {
	ID, Type, AggregateType, AggregateID, TenantID string
	Payload                                        json.RawMessage
	OccurredAt                                     time.Time
}

type Publisher interface {
	Publish(context.Context, Event) error
}
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Claim(ctx context.Context, limit int, lease time.Duration) ([]Event, error) {
	if limit < 1 || limit > 500 {
		return nil, fmt.Errorf("invalid outbox claim limit")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `WITH candidates AS (SELECT id FROM outbox_events WHERE published_at IS NULL AND next_attempt_at<=now() AND (locked_until IS NULL OR locked_until<now()) ORDER BY occurred_at,id FOR UPDATE SKIP LOCKED LIMIT $1), claimed AS (UPDATE outbox_events event SET locked_until=now()+$2::interval,attempts=attempts+1 FROM candidates WHERE event.id=candidates.id RETURNING event.public_id,event.event_type,event.aggregate_type,event.aggregate_id,event.payload,event.occurred_at) SELECT claimed.public_id,claimed.event_type,claimed.aggregate_type,claimed.aggregate_id,claimed.payload,claimed.occurred_at,COALESCE(jt.tenant_id,hold_wallet.tenant_id,aggregate_wallet.tenant_id,lqr.tenant_id) FROM claimed LEFT JOIN journal_transactions jt ON claimed.aggregate_type='journal_transaction' AND jt.public_id=claimed.aggregate_id LEFT JOIN balance_holds h ON claimed.aggregate_type='balance_hold' AND h.public_id=claimed.aggregate_id LEFT JOIN wallets hold_wallet ON hold_wallet.id=h.wallet_id LEFT JOIN wallets aggregate_wallet ON claimed.aggregate_type='wallet' AND aggregate_wallet.public_id=claimed.aggregate_id LEFT JOIN lender_liquidity_reservations lqr ON claimed.aggregate_type='liquidity_reservation' AND lqr.public_id=claimed.aggregate_id WHERE COALESCE(jt.tenant_id,hold_wallet.tenant_id,aggregate_wallet.tenant_id,lqr.tenant_id) IS NOT NULL`, limit, lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0, limit)
	for rows.Next() {
		var event Event
		if err = rows.Scan(&event.ID, &event.Type, &event.AggregateType, &event.AggregateID, &event.Payload, &event.OccurredAt, &event.TenantID); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return events, tx.Commit(ctx)
}

func (s *Store) MarkPublished(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE outbox_events SET published_at=now(),locked_until=NULL,last_error=NULL WHERE public_id=$1 AND published_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("outbox event unavailable")
	}
	return nil
}
func (s *Store) MarkFailed(ctx context.Context, id string, cause error) error {
	message := "unknown publish failure"
	if cause != nil {
		message = cause.Error()
	}
	_, err := s.pool.Exec(ctx, `UPDATE outbox_events SET locked_until=NULL,last_error=$2,next_attempt_at=now()+LEAST(interval '15 minutes',interval '5 seconds'*power(2,LEAST(attempts,8))) WHERE public_id=$1 AND published_at IS NULL`, id, message)
	return err
}

type Worker struct {
	Store     *Store
	Publisher Publisher
	BatchSize int
	Lease     time.Duration
}

func (w Worker) RunOnce(ctx context.Context) error {
	events, err := w.Store.Claim(ctx, w.BatchSize, w.Lease)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err = w.Publisher.Publish(ctx, event); err != nil {
			if markErr := w.Store.MarkFailed(ctx, event.ID, err); markErr != nil {
				return markErr
			}
			continue
		}
		if err = w.Store.MarkPublished(ctx, event.ID); err != nil {
			return err
		}
	}
	return nil
}
