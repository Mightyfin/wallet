-- +goose Up
-- Internal posting foundation. No public route or automated approval is enabled.
CREATE TABLE goods_credit_authorizations (
 id text PRIMARY KEY,
 tenant_id varchar(64) NOT NULL,
 reservation_id uuid NOT NULL UNIQUE REFERENCES lender_liquidity_reservations(id),
 borrower_party_id text NOT NULL CHECK(length(trim(borrower_party_id))>0),
 supplier_party_id text NOT NULL CHECK(length(trim(supplier_party_id))>0 AND supplier_party_id<>borrower_party_id),
 destination_wallet_id uuid NOT NULL REFERENCES wallets(id),
 order_reference text NOT NULL CHECK(length(trim(order_reference))>0),
 amount numeric(20,2) NOT NULL CHECK(amount>0),
 currency char(3) NOT NULL,
 allow_partial_use boolean NOT NULL,
 expires_at timestamptz NOT NULL,
 authorized_by text NOT NULL CHECK(length(trim(authorized_by))>0),
 request_hash char(64) NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(id,tenant_id)
);
CREATE TABLE goods_credit_uses (
 tenant_id varchar(64) NOT NULL,
 use_id text NOT NULL,
 authorization_id text NOT NULL,
 transaction_id uuid NOT NULL UNIQUE REFERENCES journal_transactions(id),
 amount numeric(20,2) NOT NULL CHECK(amount>0),
 requested_by text NOT NULL CHECK(length(trim(requested_by))>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,use_id),
 FOREIGN KEY(authorization_id,tenant_id) REFERENCES goods_credit_authorizations(id,tenant_id)
);
CREATE INDEX goods_credit_uses_authorization ON goods_credit_uses(authorization_id);
CREATE TRIGGER goods_authorization_history BEFORE UPDATE OR DELETE ON goods_credit_authorizations
 FOR EACH ROW EXECUTE FUNCTION reject_posted_financial_mutation();
CREATE TRIGGER goods_use_history BEFORE UPDATE OR DELETE ON goods_credit_uses
 FOR EACH ROW EXECUTE FUNCTION reject_posted_financial_mutation();

-- +goose StatementBegin
CREATE FUNCTION validate_goods_authorization() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r lender_liquidity_reservations%ROWTYPE; w wallets%ROWTYPE; a ledger_accounts%ROWTYPE;
BEGIN
 SELECT * INTO r FROM lender_liquidity_reservations WHERE id=NEW.reservation_id;
 SELECT * INTO a FROM ledger_accounts WHERE id=r.account_id FOR UPDATE;
 SELECT * INTO w FROM wallets WHERE id=NEW.destination_wallet_id FOR UPDATE;
 IF r.id IS NULL OR w.id IS NULL OR r.tenant_id<>NEW.tenant_id OR r.amount<>NEW.amount
 OR r.currency<>NEW.currency OR w.currency<>NEW.currency OR w.tenant_id<>NEW.tenant_id
 OR w.owner_id<>NEW.supplier_party_id OR w.owner_type='system'
 OR w.legal_entity_id<>a.legal_entity_id OR w.status<>'active' OR a.status<>'active'
 OR a.account_purpose<>'lender_liquidity' OR NEW.expires_at<=clock_timestamp()
 OR NOT EXISTS(SELECT 1 FROM legal_entities WHERE id=a.legal_entity_id AND status='active') THEN
  RAISE EXCEPTION 'goods authorization does not match reservation and supplier' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER goods_authorization_binding BEFORE INSERT ON goods_credit_authorizations
 FOR EACH ROW EXECUTE FUNCTION validate_goods_authorization();

-- +goose StatementBegin
CREATE FUNCTION validate_goods_use() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE a goods_credit_authorizations%ROWTYPE; used numeric;
BEGIN
 SELECT * INTO a FROM goods_credit_authorizations WHERE id=NEW.authorization_id AND tenant_id=NEW.tenant_id FOR UPDATE;
 SELECT coalesce(sum(amount),0) INTO used FROM goods_credit_uses WHERE authorization_id=a.id;
 IF a.id IS NULL OR a.expires_at<=clock_timestamp() OR NEW.amount>a.amount-used
 OR (NOT a.allow_partial_use AND (used<>0 OR NEW.amount<>a.amount)) THEN
  RAISE EXCEPTION 'goods use exceeds current authorization' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER goods_use_limit BEFORE INSERT ON goods_credit_uses
 FOR EACH ROW EXECUTE FUNCTION validate_goods_use();

-- Validate both directions at commit: a draw must have its exact posting, and
-- a goods posting cannot exist without its authorized draw.
-- +goose StatementBegin
CREATE FUNCTION validate_goods_posting() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE jid uuid; u goods_credit_uses%ROWTYPE; a goods_credit_authorizations%ROWTYPE;
 j journal_transactions%ROWTYPE; r lender_liquidity_reservations%ROWTYPE; entity uuid;
BEGIN
 IF TG_TABLE_NAME='journal_transactions' THEN
  IF NEW.posting_rule<>'goods.supplier_wallet' AND NEW.transaction_type<>'goods_credit_use' THEN RETURN NEW; END IF;
  jid:=NEW.id;
 ELSE jid:=NEW.transaction_id;
 END IF;
 SELECT * INTO j FROM journal_transactions WHERE id=jid;
 IF TG_TABLE_NAME='journal_entries' AND j.posting_rule<>'goods.supplier_wallet'
 AND j.transaction_type<>'goods_credit_use' THEN RETURN NEW; END IF;
 SELECT * INTO u FROM goods_credit_uses WHERE transaction_id=jid;
 SELECT * INTO a FROM goods_credit_authorizations WHERE id=u.authorization_id;
 SELECT * INTO r FROM lender_liquidity_reservations WHERE id=a.reservation_id;
 SELECT legal_entity_id INTO entity FROM ledger_accounts WHERE id=r.account_id;
 IF u.transaction_id IS NULL OR j.tenant_id IS DISTINCT FROM u.tenant_id
 OR j.currency IS DISTINCT FROM a.currency OR j.legal_entity_id IS DISTINCT FROM entity
 OR j.posting_rule IS DISTINCT FROM 'goods.supplier_wallet'
 OR j.transaction_type IS DISTINCT FROM 'goods_credit_use' OR j.status IS DISTINCT FROM 'posted'
 OR j.metadata->>'authorization_id' IS DISTINCT FROM a.id
 OR j.metadata->>'facility_id' IS DISTINCT FROM r.facility_id
 OR j.metadata->>'borrower_party_id' IS DISTINCT FROM a.borrower_party_id
 OR j.metadata->>'supplier_party_id' IS DISTINCT FROM a.supplier_party_id
 OR j.metadata->>'order_reference' IS DISTINCT FROM a.order_reference
 OR j.metadata->>'use_id' IS DISTINCT FROM u.use_id
 OR (SELECT count(*) FROM journal_entries WHERE transaction_id=jid)<>2
 OR (SELECT count(*) FROM journal_entries e JOIN ledger_accounts la ON la.id=e.account_id
     WHERE e.transaction_id=jid AND e.amount=u.amount AND e.currency=a.currency
     AND la.legal_entity_id=entity AND la.currency=a.currency AND (
       (e.side='debit' AND la.account_purpose='loan_receivable' AND la.account_class='asset' AND la.normal_side='debit')
       OR (e.side='credit' AND la.wallet_id=a.destination_wallet_id AND la.account_purpose='wallet_available'
           AND la.account_class='liability' AND la.normal_side='credit')))<>2 THEN
  RAISE EXCEPTION 'goods draw and supplier posting do not reconcile' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER goods_use_posting AFTER INSERT ON goods_credit_uses
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_goods_posting();
CREATE CONSTRAINT TRIGGER goods_journal_use AFTER INSERT ON journal_transactions
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_goods_posting();
CREATE CONSTRAINT TRIGGER goods_entry_posting AFTER INSERT ON journal_entries
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_goods_posting();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'goods credit history requires a reviewed forward migration'; END $$;
-- +goose StatementEnd
