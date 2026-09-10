# Lender liquidity reservation

This is a dedicated reservation API for platform services, not tenant wallet
funding. POST `/v1/internal/lender-liquidity/reservations` requires the dedicated
`wallet.liquidity.reserve` scope, `lender-liquidity-reserver` role, platform tenant
delegation role, and acting tenant/application headers. Existing wallet admin or
hold access alone cannot use it. No roles or credentials were automatically issued.

The request names a legal entity, a designated lender asset account, facility,
authorization, amount and currency. The service derives the tenant and records
the requesting service from verified authentication. Only active debit-normal
`lender_liquidity` asset accounts with no wallet owner qualify. A participant
balance or common bank-clearing account is not accepted as lender liquidity.

Reservations use posted ledger funds, not a configured balance. Concurrent calls
lock the account; tenant/authorization retries retain one immutable reservation.
Changed account, facility, currency or amount conflicts. Reservations do not expire.
Deferred database checks prevent later postings from spending reserved funds.
History cannot be updated or deleted by ordinary SQL. An atomic outbox event is
scoped from the stored reservation, not untrusted event payload fields.

## Release requirements and remaining work

Read-only verification is now available at
`GET /v1/internal/lender-liquidity/reservations/{reservation_id}?legal_entity_id=...`.
It requires separate `wallet.liquidity.read` scope and `lender-liquidity-reader`
role, plus the existing platform delegation role and acting headers. The tenant
comes from verified delegation, not a query parameter. A foreign tenant/entity
combination returns 404. The response includes the exact reservation, environment,
legal entity, current source-active flag and verification time. Inactive source
accounts retain readable reservation history; they do not appear active.
No lender account balance, other tenant allocation or requesting actor is exposed.
This read does not retry a reservation, change state or authorize payment.

Apply migration 00003 before deploying the API or updated outbox worker. No
production migration/deployment occurred. Automatic down migration deliberately
fails because deleting reservation history is unsafe.

This release does not create/fund lender accounts, establish verified opening
capital, consume/release reservations or grant payment authorization. No existing
disbursement path calls it yet. Lender account onboarding and verified capital
postings must have approved accounting configuration; tests use labelled synthetic
journal fixtures, not a production seeding endpoint. Facility must durably bind its
approved funding source/programme to this account and request, persist the result,
and handle uncertain replies using the original authorization. Goods-credit
execution stays disabled until that path, settlement and servicing are complete.

Account schema ownership/status/currency must remain under restricted ledger
administration; database owners can alter triggers and are outside this guard.
