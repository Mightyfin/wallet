-- +goose Up
ALTER TABLE wallets
  ADD COLUMN source_application VARCHAR(120),
  ADD COLUMN creation_idempotency_key VARCHAR(128),
  ADD COLUMN creation_request_hash CHAR(64);
CREATE UNIQUE INDEX wallets_creation_idempotency_unique
  ON wallets(tenant_id,source_application,creation_idempotency_key)
  WHERE creation_idempotency_key IS NOT NULL;

-- Existing wallets predate this command contract and remain explicitly without a creation key.

-- +goose Down
DROP INDEX IF EXISTS wallets_creation_idempotency_unique;
ALTER TABLE wallets DROP COLUMN IF EXISTS creation_request_hash,
  DROP COLUMN IF EXISTS creation_idempotency_key,
  DROP COLUMN IF EXISTS source_application;
