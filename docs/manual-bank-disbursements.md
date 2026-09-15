# Confirmed external bank disbursements

Status: local implementation and disposable-database tests; not deployed or
connected to the Green workflow yet.

`financial.RecordConfirmedBankDisbursement` records a payment already verified
through Payment Rails. It is not an instruction to send bank funds. Its input
must be resolved from the authenticated, immutable verified payment by the
reconciliation adapter, not accepted as arbitrary tenant request JSON.

For a K5,000 bank payment:

- Debit loan receivable K5,000.
- Credit bank clearing K5,000.
- Borrower wallet cash remains unchanged.

Bank clearing is a reconciliation account, not a claim that a live bank API
reported settlement. The approved source and destination references remain in
immutable journal metadata. Subsequent bank reconciliation must link the
clearing movement to the actual bank statement and accounting source account.
Principal is not revenue; this posting does not book fees or future interest.

The legal entity, tenant, borrower party, wallet and currency must agree. An
already executed payment can be recorded even if the wallet was subsequently
suspended; this does not enable new spending or erase the external obligation.

One payment reference has one posting globally for the manual reconciliation
source. Retries are serialized and must match the entire original instruction.
The journal and `loan.external_disbursement.posted` outbox event commit together.
Migration 00005 adds global deduplication and a deferred database constraint
that permits only receivable debit and bank-clearing credit for this source.
Posted records remain immutable; corrections require controlled reversal work,
not editing the original journal.

Evidence digests and staff review references stay in internal journal metadata.
The event carries references and the amount, not bank receipts or account
details. It deliberately uses a separate event from wallet-cash disbursement.

Test evidence: eight concurrent attempts yield one journal and one event,
wallet cash stays zero, amount/payee/evidence/tenant changes are rejected,
borrower mismatches fail, and an injected outbox failure rolls back the journal.
Run against a disposable migrated database with `WALLET_LEDGER_TEST_DATABASE_URL`.

The internal POST endpoint `/v1/internal/loan-disbursements/external-bank`
requires all of `wallet.disburse.external`, `manual-bank-reconciler` and
`platform-tenant-delegator`, explicit acting tenant/application headers and
`X-Expected-Environment` matching this deployment. Ordinary wallet administrator
roles are not sufficient. The idempotency key must equal payment_id; body tenant
and environment overrides are not accepted. Payment Rails supplies the reviewed
instruction from its own store using its dedicated posting worker.

Remaining: deployment of the authenticated adapter and service authorization,
Facility confirmation/activation consumer, Billing & Collections intake,
staff UI, deployment and Green end-to-end repayment/reconciliation UAT.
The local function alone is not a production certification.
