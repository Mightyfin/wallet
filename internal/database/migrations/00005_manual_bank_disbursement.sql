-- +goose Up
-- The verified payment reference is globally unique for this posting source,
-- not just unique inside a tenant or legal entity. No cash account is credited.
CREATE UNIQUE INDEX journal_manual_bank_payment_unique
ON journal_transactions(external_reference)
WHERE source_system='manual-bank-reconciliation';

-- +goose StatementBegin
CREATE FUNCTION enforce_manual_bank_posting() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE valid_entries integer;
BEGIN
 IF NEW.source_system <> 'manual-bank-reconciliation' THEN RETURN NEW; END IF;
 IF NOT (NEW.transaction_type='loan_disbursement' AND NEW.posting_rule='loan.disbursement.external_bank'
   AND NEW.status='posted' AND NEW.external_reference<>''
   AND NEW.idempotency_key=NEW.external_reference
   AND NEW.metadata->>'payment_id'=NEW.external_reference
   AND NEW.metadata->>'environment' IN ('sandbox','production')
   AND NEW.metadata->>'facility_id'<>'' AND NEW.metadata->>'authorization_id'<>''
   AND NEW.metadata->>'source_account_id'<>'' AND NEW.metadata->>'destination_account_id'<>''
   AND NEW.metadata->>'reviewed_by'<>'' AND NEW.metadata->>'evidence_digest' ~ '^[0-9a-f]{64}$') IS TRUE THEN
   RAISE EXCEPTION 'Invalid manual bank posting instruction' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM wallets w WHERE w.public_id=NEW.metadata->>'wallet_id'
   AND w.legal_entity_id=NEW.legal_entity_id AND w.tenant_id=NEW.tenant_id
   AND w.owner_id=NEW.metadata->>'party_id' AND w.currency=NEW.currency) THEN
   RAISE EXCEPTION 'Manual payment borrower mismatch' USING ERRCODE='23514';
 END IF;
 SELECT count(*) INTO valid_entries FROM journal_entries e JOIN ledger_accounts a ON a.id=e.account_id
 WHERE e.transaction_id=NEW.id AND a.legal_entity_id=NEW.legal_entity_id
   AND e.currency=NEW.currency AND a.currency=NEW.currency AND a.account_class='asset' AND a.normal_side='debit'
   AND ((e.side='debit' AND a.account_purpose='loan_receivable') OR (e.side='credit' AND a.account_purpose='bank_clearing'));
 IF valid_entries<>2 OR (SELECT count(*) FROM journal_entries WHERE transaction_id=NEW.id)<>2 THEN
   RAISE EXCEPTION 'Manual bank posting must debit receivable and credit bank clearing only' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER manual_bank_posting_checked AFTER INSERT ON journal_transactions
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_manual_bank_posting();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
RAISE EXCEPTION 'Manual bank payment deduplication must not be removed while financial history exists';
END $$;
-- +goose StatementEnd
