# Architecture decision: unified Wallet/Ledger deployment

## Decision

Build Wallet and Ledger in one repository and deployment for the first production generation,
with explicit internal module boundaries and a single database transaction for financial posting.

## Why

Atomic balance enforcement, deterministic account locking, idempotency, journal immutability and
transactional event creation matter more than independent deployment at this stage. Splitting the
services now would introduce distributed consistency and failure modes without proven scale data.

## Scale path

API replicas and posting workers may scale horizontally against PostgreSQL. High-volume reads may
use projections, but projections never become financial truth. If Ledger later becomes a separate
deployment, Wallet submits commands through the frozen posting contract and consumes committed
ledger events; products do not change integration.

## Trust boundaries

- Product callers authenticate as workloads through the shared IDP.
- Authorization is tenant, legal entity, environment, application and scope aware.
- Generic journal posting is internal-only; partner applications receive capability-specific APIs.
- Manual adjustments use a separate maker-checker workflow and are never direct database edits.
- External rail callbacks are authenticated, deduplicated and reconciled before final posting.
- Multi-tenant platform workers may assert `X-Acting-Tenant-Id` and
  `X-Acting-Application-Id` only when their OIDC service account has the dedicated
  `platform-tenant-delegator` role. Tenant-bearing partner tokens cannot use delegation headers.

## Initial posting rules

| Business event | Debit | Credit | Caller |
|---|---|---|---|
| Approved loan disbursement | Loan receivable | Customer wallet liability | Lending/EFaaS workload |
| Wallet-to-wallet transfer | Source wallet liability | Destination wallet liability | Authorized product workload |
| Wallet loan repayment | Customer wallet liability | Loan receivable | Lending/collections workload |
| Settled external repayment | Bank clearing | Loan receivable | Reconciliation workload |
| Settled external deposit | Bank clearing | Customer wallet liability | Reconciliation workload |
| Linked reversal | Opposite of original entries | Opposite of original entries | Internal maker-checker workflow only |

Bank statement uploads and receipts first become reconciliation evidence. They are not posting
authority by themselves. The reconciliation service must match and approve the evidence before it
can invoke an external-settlement command.

An approved funding-source reference may be carried with an authorised loan
disbursement for audit and reconciliation. It is an operational approval
control, not an extra journal entry: adding a second debit or credit to the
wallet-credit posting would make the transaction economically incorrect.

EFaaS owns partner API semantics and authorization but never owns balances. For production
transactions it calls these capability-specific commands using a service token carrying the
originating EFaaS tenant. EFaaS sandbox simulations remain isolated from this production ledger.
