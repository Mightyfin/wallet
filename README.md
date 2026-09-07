# MightyFin Wallet and Ledger

Controlled EFaaS requirements and release evidence are routed through the
[EFaaS controlled-requirements map](https://github.com/Mightyfin/platform-infrastructure/blob/main/docs/efaas/CONTROLLED-REQUIREMENTS.md).
This service's financial invariants do not establish regulatory approval, safeguarding evidence
or authorization to move real customer funds.

Status: **Shared platform foundation under implementation**

This is the authoritative operational financial subledger for MightyFin Direct Embedded Finance,
EFaaS, lending disbursements, repayments, collections, partner payouts and future wallet products.
No product service may maintain an independent production balance or directly edit ledger data.

## Initial deployment boundary

Wallet and Ledger are modules in one Go service and one PostgreSQL transaction boundary:

- **Wallet** owns wallet lifecycle, ownership, status, available/pending/held presentation and
  customer/product capabilities.
- **Ledger** owns accounts, balanced journal transactions, immutable entries, reversals, posting
  rules, control totals and trial-balance truth.
- **Funding instructions** identify the approved source of value. They never manufacture money.
- **Rail and reconciliation adapters** confirm external movement and settlement before pending
  value becomes spendable.

The modules may become separate deployments only after correctness and operational evidence show
that the additional distributed-transaction boundary is justified.

## Financial invariants

- Debits equal credits per transaction and currency.
- Posted entries are immutable; corrections are linked reversals.
- Monetary values use decimal database types and decimal strings at APIs—never floating point.
- Every mutation is idempotent and tenant/legal-entity scoped.
- Wallet balances are derived from ledger entries, never directly updated columns.
- Pending, held, available and settled are distinct.
- Production credit requires an approved source event and posting rule.
- Customer value, MightyFin operating cash, loan receivables, revenue and suspense remain separate.
- External evidence and reconciliation determine settlement finality.

## Product integration

```text
EFaaS / Direct EF / Lending / Collections
                  |
          Wallet command API
                  |
        Posting-rule validation
                  |
     Immutable double-entry Ledger
                  |
       Outbox events + adapters
                  |
 Bank / mobile money / partner rails
                  |
            Reconciliation
```

The implemented financial kernel provides legal-entity and wallet provisioning, an immutable
double-entry journal, derived balances, idempotent wallet transfers, loan disbursements, holds,
wallet repayments, reconciled external repayments, linked internal reversals and a leased
transactional outbox. Capability-specific HTTP commands use shared-IDP OIDC tokens and tenant,
environment and scope claims. Generic journal posting and reversal HTTP APIs are intentionally
absent. Live rail initiation remains disabled until a licensed provider and reconciliation control
are configured.

In the sandbox environment only, the service also exposes synthetic funding and wallet transaction
history for EFaaS certification. Synthetic funding is journaled under `sandbox.wallet.funding`,
emits `sandbox.wallet.funded`, and is not available when Wallet/Ledger runs in staging or production.

The machine-readable internal contract lives in the separate API Contracts repository at
`openapi/wallet-ledger/wallet-ledger.openapi.yaml`.

## Authoritative boundaries

- This service is the operational wallet subledger, not the statutory general ledger.
- The loan service remains authoritative for contractual loan schedules and exposure.
- Sage remains the transition accounting authority until approved replacement gates pass.
- Reconciliation owns matching and break cases; it cannot rewrite posted ledger entries.
- EFaaS owns partner tenancy, credentials, capabilities and webhooks; it calls this service for
  production financial effects.
