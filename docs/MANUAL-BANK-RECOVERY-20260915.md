# Read-only manual bank journal recovery

`FindConfirmedBankDisbursement` looks up an existing manual-bank journal by
payment ID, legal entity, tenant and environment, then verifies the exact stored
command hash (including the reviewed evidence digest), status, amount, currency
and idempotency key. It writes nothing and does not require the lender to remain
active: this reconstructs an already completed accounting effect rather than
permitting a new disbursement.

Disposable PostgreSQL tests passed for missing payment, repeated exact lookup,
changed amount/payee/evidence/Party, foreign tenant/environment, and recovery
after lender suspension. Existing assertions still prove one event, two balanced
journal entries and no borrower-wallet cash. Targeted financial-package vet
passed. Unrelated local Wallet changes were not part of this work.

Not deployed or exposed over HTTP yet. Remaining work: restricted read endpoint,
Payment Rails client, and worker recovery before the new-posting guard. A lookup
miss must not authorize new posting; unavailable/ambiguous responses must not be
treated as a miss. Full Green UAT and live provider certification remain open.

The read-only endpoint is now implemented as
`POST /v1/internal/loan-disbursements/external-bank/lookup`, accepting the exact
original command without creating an entry. POST keeps command/evidence values
out of URL logs. It requires `manual-bank-reconciler`, platform delegation and
the separate `wallet.disburse.external.read` scope. Tenant, application and
environment must match authenticated context. Read scope cannot post; write-only
scope cannot invoke lookup. Unknown body fields and query scope overrides fail.
Success returns 200 with the existing transaction; no match returns 404 and a
changed same-scope instruction returns conflict. Results are not cacheable.

HTTP authorization tests and vet passed. Endpoint and read scope have not yet
been deployed/provisioned. Payment Rails lookup client and worker recovery remain
outstanding; no new credential privileges were granted by this code change.
# Lender-state posting serialization — 2026-09-15

Manual bank posting now holds shared locks on the lender legal entity and
borrower wallet rows alongside its ledger account locks until commit. This
keeps lender status and wallet ownership/currency checks from becoming stale
while a new journal is created. Exact historical journal lookup remains
available after lender suspension and does not create another financial effect.

A disposable PostgreSQL regression first reproduced the old behavior: an
uncommitted lender suspension did not prevent a new journal. With the fix, the
posting waits and its deadline leaves no journal. Rolling back the suspension
allows the existing eight-concurrent-retry test to produce exactly one balanced
K5,000 journal, one outbox event and no wallet cash. Three repeated runs passed
from committed archive `77ac4b3`, excluding unrelated dirty worktree files.
Financial and HTTP package tests and financial vet also passed from that archive.

This is a synthetic database test, not Green's actual bank evidence or complete
UAT. Remote Party membership/evidence safeguards and reviewer access remain
separate; this change alone does not permit enabling the posting worker.
