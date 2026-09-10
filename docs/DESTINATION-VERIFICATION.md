# Internal destination verification

POST `/v1/internal/wallet-destinations/verify` takes `legal_entity_id`, `wallet_id`,
`party_id`, and `currency`. Tenant identity comes from validated platform
delegation, never the body. Requires both `wallet-destination-verifier` and
`platform-tenant-delegator` roles plus `wallet.destination.verify` scope.

The exact destination must belong to that party/tenant/legal entity/currency.
The legal entity, wallet and receiving liability account must be active. System
wallets are excluded. No balances are disclosed and no journal or hold is created.
Returns matching identifiers, deployment environment, `active_owned_wallet` and
the server's verification timestamp. A mismatch returns not-found; service errors
are not successful verifications.

This is a point-in-time check, not a payment authorization, reservation, supplier
KYC or confirmation of commercial activity. Financial execution must recheck
status and ownership inside its own transaction to prevent changes between review
and payment. No new roles are automatically granted by this endpoint.
