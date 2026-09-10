# Wallet-first goods credit

Status: internal posting foundation plus dedicated registration/read HTTP routes.
No HTTP draw endpoint, deployment or tenant activation is included.
Cash-disbursement guards remain enabled.

The initial destination is the approved supplier's MightyFin wallet, not a bank
account or mobile-money account. The borrower receives no cash. On each use,
Wallet debits loan receivable and credits supplier wallet liability by the same
amount. Borrower, facility, order and authorization references remain on the
immutable journal. Unused purchasing capacity is not a wallet cash balance.

## Boundaries

Facility must deliver the approved instruction through a dedicated authenticated
workload. Registration requires `goods-credit-authorizer` and
`platform-tenant-delegator` roles plus `wallet.goods.authorize`; historical reads
require the same roles plus `wallet.goods.read`. Environment and delegation must
match. The HTTP handler records the authenticated subject, rejects caller actor or
tenant fields, and verifies the exact facility/reservation/legal-entity binding.
It returns the saved record, not a request echo. This package does not decide
credit; the upstream staff approval/durable workflow is still pending.
Do not expose these methods as tenant-controlled approval APIs. Billing &
Collections must consume actual used-credit events for debt servicing; it must
not treat the unused approved amount as disbursed principal.

Registration binds one existing lender reservation to one authorization, tenant,
borrower, supplier, wallet, order, currency, limit, partial-use rule and expiry.
Draw requests cannot substitute those values. Exact retries return the original
result. Changed retries fail. Supplier/source status is rechecked on new draws.

Each draw commits its use record, balanced posting and outbox event together.
Database constraints enforce the limit, immutable history and exact posting
association. Concurrent draws serialize against the authorization. Registration
and execution acquire source-account locks before supplier-wallet locks.

Lender liquidity is **not released** when a supplier receives wallet credit. It
backs the resulting wallet liability and any remaining unused capacity. External
withdrawal, reservation consumption/release, cancellation, repayment restoration
and reversals require their own controlled workflows and are not implemented by
this foundation. Do not activate this as an end-to-end production product yet.

## Verification

Run the isolated regression runner in api-contracts-layer. The wallet financial
tests exercise synthetic supplier credit, zero borrower cash, concurrent retries,
competing draws, over-limit rejection, tenant isolation, inactive supplier,
outbox-failure rollback, immutable records and unbacked journal rejection.
These tests are not live provider certification or complete goods-credit UAT.
