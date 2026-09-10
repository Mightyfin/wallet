-- +goose Up
-- Reservations against explicitly designated lender assets, never wallet liabilities.
CREATE TABLE lender_liquidity_reservations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 public_id varchar(64) NOT NULL UNIQUE,
 account_id uuid NOT NULL REFERENCES ledger_accounts(id),
 tenant_id varchar(64) NOT NULL,
 facility_id varchar(128) NOT NULL,
 authorization_id varchar(128) NOT NULL,
 requested_by text NOT NULL CHECK(length(trim(requested_by))>0),
 amount numeric(20,2) NOT NULL CHECK(amount>0),
 currency char(3) NOT NULL,
 status text NOT NULL DEFAULT 'reserved' CHECK(status='reserved'),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,authorization_id)
);
CREATE INDEX lender_liquidity_reserved_account ON lender_liquidity_reservations(account_id);
CREATE TRIGGER lender_liquidity_history BEFORE UPDATE OR DELETE ON lender_liquidity_reservations
 FOR EACH ROW EXECUTE FUNCTION reject_posted_financial_mutation();

-- +goose StatementBegin
CREATE FUNCTION enforce_lender_liquidity_available() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE purpose text; available numeric; reserved numeric; account_currency char(3);
BEGIN
 SELECT account_purpose,currency INTO purpose,account_currency FROM ledger_accounts WHERE id=NEW.account_id;
 IF purpose<>'lender_liquidity' THEN
  IF TG_TABLE_NAME='lender_liquidity_reservations' THEN RAISE EXCEPTION 'not a lender liquidity account'; END IF;
  RETURN NULL;
 END IF;
 PERFORM 1 FROM ledger_accounts WHERE id=NEW.account_id FOR UPDATE;
 IF NEW.currency<>account_currency THEN RAISE EXCEPTION 'liquidity currency mismatch'; END IF;
 SELECT coalesce(sum(CASE WHEN side='debit' THEN amount ELSE -amount END),0) INTO available FROM journal_entries WHERE account_id=NEW.account_id;
 SELECT coalesce(sum(amount),0) INTO reserved FROM lender_liquidity_reservations WHERE account_id=NEW.account_id;
 IF available<reserved THEN RAISE EXCEPTION 'lender liquidity is reserved or insufficient' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER lender_liquidity_journal_guard AFTER INSERT ON journal_entries
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_lender_liquidity_available();
CREATE CONSTRAINT TRIGGER lender_liquidity_reservation_guard AFTER INSERT ON lender_liquidity_reservations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_lender_liquidity_available();

-- +goose StatementBegin
CREATE FUNCTION protect_lender_account_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (OLD.account_purpose='lender_liquidity' OR NEW.account_purpose='lender_liquidity') AND
 ROW(NEW.public_id,NEW.legal_entity_id,NEW.wallet_id,NEW.account_class,NEW.account_purpose,NEW.normal_side,NEW.currency)
 IS DISTINCT FROM ROW(OLD.public_id,OLD.legal_entity_id,OLD.wallet_id,OLD.account_class,OLD.account_purpose,OLD.normal_side,OLD.currency) THEN
  RAISE EXCEPTION 'lender account identity is immutable';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER lender_account_identity BEFORE UPDATE ON ledger_accounts
 FOR EACH ROW EXECUTE FUNCTION protect_lender_account_identity();

-- +goose Down
-- Deliberately no destructive automatic rollback of financial reservation history.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'lender liquidity history requires a reviewed forward migration'; END $$;
-- +goose StatementEnd
