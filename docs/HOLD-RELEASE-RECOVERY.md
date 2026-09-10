# Retry-safe hold release

`POST /v1/wallets/{wallet_id}/holds/{hold_id}/release` accepts an omitted
`legal_entity_id`, matching hold creation. The financial service resolves it only
through the authenticated tenant's wallet. An explicit entity remains scoped to
that same tenant and wallet. The existing `wallet.hold` permission is unchanged.

The service locks the hold before changing it. An active hold becomes released
and emits one release event. Repeating the call returns the released hold without
another event. A captured hold is rejected; another tenant or wallet cannot release
or inspect the hold. Serialization errors remain retryable with the same hold ID.

This enables Payment Rails to recover after losing Wallet's successful response.
It does not authorize release of a hold when the provider's outcome is uncertain.
No journal entry or credit facility is created by releasing a hold.

Tests use disposable synthetic data and cover service replay, one event only,
tenant isolation and the actual HTTP route with delegated workload authentication.
