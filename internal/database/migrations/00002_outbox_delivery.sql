-- +goose Up
ALTER TABLE outbox_events
  ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN locked_until TIMESTAMPTZ,
  ADD COLUMN last_error TEXT;

CREATE INDEX outbox_events_delivery_idx
ON outbox_events(next_attempt_at, occurred_at)
WHERE published_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS outbox_events_delivery_idx;
ALTER TABLE outbox_events
  DROP COLUMN IF EXISTS attempts,
  DROP COLUMN IF EXISTS next_attempt_at,
  DROP COLUMN IF EXISTS locked_until,
  DROP COLUMN IF EXISTS last_error;
