# Wallet-first goods credit

Status: internal posting foundation plus dedicated registration/read/capacity and
purchase execution/recovery HTTP routes. No deployment or tenant activation is included.
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
credit. Facility has a separate staff approval and durable registration workflow;
deployed integration and public goods acceptance are still pending.
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

### Internal capacity read

`GET /v1/internal/goods-credit/authorizations/{authorization_id}/capacity?legal_entity_id=...`
uses the same dedicated workload roles, delegation and `wallet.goods.read` scope.
It is not a tenant endpoint. It returns approved, used, remaining and expired-unused
amounts as decimal strings, plus use count, expiry and database observation time.
All totals are read together from committed authorization/use records.

Approved = used + remaining + expired unused. At expiry, unused credit moves to
expired unused in this view; no ledger entry or liquidity release occurs. Used is
the total purchased, not the outstanding debt after repayments. Billing owns debt
servicing. State `active` means unexpired and not fully used, not permission to
spend: account status and all execution checks still apply when a purchase is made.
This read neither reserves additional credit nor authorizes a purchase.

### Internal purchase execution and recovery

`POST /v1/internal/goods-credit/authorizations/{authorization_id}/uses` accepts
only `legal_entity_id`, `use_id` and a positive two-decimal string `amount`.
`Idempotency-Key` must exactly match `use_id`. The approved supplier, borrower,
currency and order come from the immutable authorization, not the request.

Execution requires `goods-credit-executor` AND `platform-tenant-delegator` roles,
`wallet.goods.use` scope and exact environment/delegation. Registration-only
credentials and generic wallet administrators are rejected. No such credential
has been provisioned by this checkpoint. Financial effects still use the same
atomic posting engine and database guards; no second balance implementation.

`GET /v1/internal/goods-credit/authorizations/{authorization_id}/uses/{use_id}?legal_entity_id=...`
requires the execution roles and `wallet.goods.read`. Both routes return the saved
use, original actor, approved parties/order and journal/correlation references.
After a lost response, read this same reference or repeat the identical request.
Do not use a new reference or assume a timeout means no posting occurred.

These are workload-only operations, not tenant APIs. Facility purchase request
coordination, consent/commercial checks and Billing used-debt servicing remain
unfinished. Goods product/acceptance gates remain closed until that complete
journey is implemented and tested. No cash payout route is enabled here.

### Tests

Run the isolated regression runner in api-contracts-layer. The wallet financial
tests exercise synthetic supplier credit, zero borrower cash, concurrent retries,
competing draws, over-limit rejection, tenant isolation, inactive supplier,
outbox-failure rollback, immutable records and unbacked journal rejection.
These tests are not live provider certification or complete goods-credit UAT.
