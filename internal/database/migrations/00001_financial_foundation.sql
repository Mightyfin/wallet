-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE legal_entities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    public_id VARCHAR(64) NOT NULL UNIQUE,
    name TEXT NOT NULL,
    country_code CHAR(2) NOT NULL,
    base_currency CHAR(3) NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('active','suspended','closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    public_id VARCHAR(64) NOT NULL UNIQUE,
    legal_entity_id UUID NOT NULL REFERENCES legal_entities(id),
    tenant_id VARCHAR(64) NOT NULL,
    owner_type VARCHAR(30) NOT NULL CHECK (owner_type IN ('customer','organization','partner','system')),
    owner_id VARCHAR(128) NOT NULL,
    currency CHAR(3) NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('pending','active','restricted','suspended','closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (legal_entity_id, tenant_id, owner_type, owner_id, currency)
);

CREATE TABLE ledger_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    public_id VARCHAR(64) NOT NULL UNIQUE,
    legal_entity_id UUID NOT NULL REFERENCES legal_entities(id),
    wallet_id UUID REFERENCES wallets(id),
    account_code VARCHAR(80) NOT NULL,
    account_class VARCHAR(30) NOT NULL CHECK (account_class IN ('asset','liability','equity','income','expense','memo')),
    account_purpose VARCHAR(50) NOT NULL,
    normal_side VARCHAR(6) NOT NULL CHECK (normal_side IN ('debit','credit')),
    currency CHAR(3) NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('active','blocked','closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (legal_entity_id, account_code, currency),
    UNIQUE NULLS NOT DISTINCT (legal_entity_id, wallet_id, account_purpose, currency)
);

CREATE TABLE journal_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    public_id VARCHAR(64) NOT NULL UNIQUE,
    legal_entity_id UUID NOT NULL REFERENCES legal_entities(id),
    tenant_id VARCHAR(64) NOT NULL,
    transaction_type VARCHAR(50) NOT NULL,
    posting_rule VARCHAR(80) NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('posted','reversed')),
    currency CHAR(3) NOT NULL,
    external_reference VARCHAR(160),
    idempotency_key VARCHAR(128) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    correlation_id VARCHAR(128) NOT NULL,
    source_system VARCHAR(80) NOT NULL,
    effective_at TIMESTAMPTZ NOT NULL,
    booked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reversal_of_id UUID UNIQUE REFERENCES journal_transactions(id),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (legal_entity_id, tenant_id, source_system, idempotency_key)
);

CREATE UNIQUE INDEX journal_transactions_external_reference_key
ON journal_transactions(legal_entity_id, source_system, external_reference)
WHERE external_reference IS NOT NULL;

CREATE TABLE journal_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    public_id VARCHAR(64) NOT NULL UNIQUE,
    transaction_id UUID NOT NULL REFERENCES journal_transactions(id),
    account_id UUID NOT NULL REFERENCES ledger_accounts(id),
    side VARCHAR(6) NOT NULL CHECK (side IN ('debit','credit')),
    amount NUMERIC(20,2) NOT NULL CHECK (amount > 0),
    currency CHAR(3) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (transaction_id, account_id, side)
);

CREATE INDEX journal_entries_account_time_idx ON journal_entries(account_id, created_at, id);

CREATE TABLE balance_holds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    public_id VARCHAR(64) NOT NULL UNIQUE,
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    amount NUMERIC(20,2) NOT NULL CHECK (amount > 0),
    currency CHAR(3) NOT NULL,
    reason VARCHAR(80) NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('active','captured','released','expired')),
    idempotency_key VARCHAR(128) NOT NULL,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (wallet_id, idempotency_key)
);

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    public_id VARCHAR(64) NOT NULL UNIQUE,
    event_type VARCHAR(100) NOT NULL,
    aggregate_type VARCHAR(60) NOT NULL,
    aggregate_id VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_posted_financial_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'posted financial records are immutable';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER journal_transactions_immutable BEFORE UPDATE OR DELETE ON journal_transactions
FOR EACH ROW EXECUTE FUNCTION reject_posted_financial_mutation();
CREATE TRIGGER journal_entries_immutable BEFORE UPDATE OR DELETE ON journal_entries
FOR EACH ROW EXECUTE FUNCTION reject_posted_financial_mutation();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_balanced_journal_transaction() RETURNS trigger AS $$
DECLARE debit_total NUMERIC(20,2); credit_total NUMERIC(20,2); entry_currency CHAR(3);
BEGIN
    SELECT COALESCE(SUM(amount) FILTER (WHERE side='debit'),0),
           COALESCE(SUM(amount) FILTER (WHERE side='credit'),0), MIN(currency)
      INTO debit_total, credit_total, entry_currency
      FROM journal_entries WHERE transaction_id=NEW.transaction_id;
    IF debit_total <> credit_total THEN
      RAISE EXCEPTION 'journal transaction % is unbalanced', NEW.transaction_id;
    END IF;
    IF EXISTS (SELECT 1 FROM journal_entries WHERE transaction_id=NEW.transaction_id AND currency<>entry_currency) THEN
      RAISE EXCEPTION 'journal transaction % mixes currencies', NEW.transaction_id;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER journal_transaction_balanced
AFTER INSERT ON journal_entries DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_balanced_journal_transaction();

-- +goose Down
DROP TABLE IF EXISTS outbox_events, balance_holds, journal_entries, journal_transactions,
  ledger_accounts, wallets, legal_entities CASCADE;
DROP FUNCTION IF EXISTS enforce_balanced_journal_transaction();
DROP FUNCTION IF EXISTS reject_posted_financial_mutation();
