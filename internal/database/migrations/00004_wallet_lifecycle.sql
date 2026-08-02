-- +goose Up
CREATE TABLE wallet_status_history (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id uuid NOT NULL REFERENCES wallets(id),
  from_status varchar(20),
  to_status varchar(20) NOT NULL CHECK(to_status IN ('pending','active','restricted','suspended','closed')),
  reason varchar(500) NOT NULL,
  evidence_reference varchar(200) NOT NULL,
  actor_subject varchar(200) NOT NULL,
  source_application varchar(100) NOT NULL,
  idempotency_key varchar(128) NOT NULL,
  request_hash char(64) NOT NULL,
  occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX wallet_status_history_wallet_idx ON wallet_status_history(wallet_id,occurred_at,id);
CREATE UNIQUE INDEX wallet_status_history_idempotency_idx ON wallet_status_history(wallet_id,idempotency_key);
CREATE TRIGGER wallet_status_history_immutable BEFORE UPDATE OR DELETE ON wallet_status_history
FOR EACH ROW EXECUTE FUNCTION reject_posted_financial_mutation();

INSERT INTO wallet_status_history(wallet_id,from_status,to_status,reason,evidence_reference,actor_subject,source_application,idempotency_key,request_hash,occurred_at)
SELECT id,NULL,status,'Historical wallet status backfill','migration:00004','system:migration','wallet-ledger-migrate','migration:00004:'||public_id,encode(digest(public_id||':'||status,'sha256'),'hex'),created_at
FROM wallets;

-- +goose Down
DROP TRIGGER IF EXISTS wallet_status_history_immutable ON wallet_status_history;
DROP TABLE IF EXISTS wallet_status_history;
