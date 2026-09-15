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
