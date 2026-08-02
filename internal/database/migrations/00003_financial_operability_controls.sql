-- +goose Up
ALTER TABLE outbox_events
  ADD COLUMN delivery_status varchar(20) NOT NULL DEFAULT 'pending' CHECK(delivery_status IN ('pending','published','dead_letter')),
  ADD COLUMN max_attempts integer NOT NULL DEFAULT 12 CHECK(max_attempts > 0),
  ADD COLUMN dead_lettered_at timestamptz;
ALTER TABLE journal_transactions DROP CONSTRAINT journal_transactions_status_check;
ALTER TABLE journal_transactions ADD CONSTRAINT journal_transactions_status_check CHECK(status='posted');
DROP INDEX outbox_events_delivery_idx;
CREATE INDEX outbox_events_delivery_idx ON outbox_events(next_attempt_at,occurred_at)
  WHERE delivery_status='pending' AND published_at IS NULL;
CREATE INDEX outbox_events_dead_letter_idx ON outbox_events(dead_lettered_at)
  WHERE delivery_status='dead_letter';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_journal_currency_matches_accounts() RETURNS trigger AS $$
DECLARE account_currency char(3); transaction_currency char(3);
BEGIN
  SELECT currency INTO account_currency FROM ledger_accounts WHERE id=NEW.account_id;
  SELECT currency INTO transaction_currency FROM journal_transactions WHERE id=NEW.transaction_id;
  IF NEW.currency<>account_currency OR NEW.currency<>transaction_currency THEN
    RAISE EXCEPTION 'journal entry currency must match account and transaction currency';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER journal_entry_currency_guard BEFORE INSERT ON journal_entries
FOR EACH ROW EXECUTE FUNCTION enforce_journal_currency_matches_accounts();

INSERT INTO ledger_accounts(public_id,legal_entity_id,account_code,account_class,account_purpose,normal_side,currency,status)
SELECT 'acc_'||encode(gen_random_bytes(16),'hex'),id,'SUSPENSE:'||base_currency,'liability','suspense','credit',base_currency,'active' FROM legal_entities
ON CONFLICT(legal_entity_id,account_code,currency) DO NOTHING;
INSERT INTO ledger_accounts(public_id,legal_entity_id,account_code,account_class,account_purpose,normal_side,currency,status)
SELECT 'acc_'||encode(gen_random_bytes(16),'hex'),id,'FEE_INCOME:'||base_currency,'income','fee_income','credit',base_currency,'active' FROM legal_entities
ON CONFLICT(legal_entity_id,account_code,currency) DO NOTHING;
INSERT INTO ledger_accounts(public_id,legal_entity_id,account_code,account_class,account_purpose,normal_side,currency,status)
SELECT 'acc_'||encode(gen_random_bytes(16),'hex'),id,'PAYMENT_FEES:'||base_currency,'expense','payment_fees','debit',base_currency,'active' FROM legal_entities
ON CONFLICT(legal_entity_id,account_code,currency) DO NOTHING;

-- +goose Down
DELETE FROM ledger_accounts WHERE account_purpose IN ('suspense','fee_income','payment_fees');
DROP TRIGGER IF EXISTS journal_entry_currency_guard ON journal_entries;
DROP FUNCTION IF EXISTS enforce_journal_currency_matches_accounts();
DROP INDEX IF EXISTS outbox_events_dead_letter_idx;
DROP INDEX IF EXISTS outbox_events_delivery_idx;
CREATE INDEX outbox_events_delivery_idx ON outbox_events(next_attempt_at,occurred_at) WHERE published_at IS NULL;
ALTER TABLE outbox_events DROP COLUMN IF EXISTS dead_lettered_at,DROP COLUMN IF EXISTS max_attempts,DROP COLUMN IF EXISTS delivery_status;
ALTER TABLE journal_transactions DROP CONSTRAINT journal_transactions_status_check;
ALTER TABLE journal_transactions ADD CONSTRAINT journal_transactions_status_check CHECK(status IN ('posted','reversed'));
