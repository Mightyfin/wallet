# Hold expiry and retries

Wallet's hold service reserves existing available funds. It does not grant credit,
approve a facility or settle a merchant. Do not use a hold as a substitute for
approved purchasing capacity.

The hold's idempotency identity includes amount, currency, reason and expiry.
An exact retry returns the existing hold without another event. Changing or
removing the expiry conflicts. Expiry is normalized to UTC microseconds to match
database precision; another timezone representing the same instant is equivalent.

New holds require an active wallet and legal entity. When an expiry is supplied,
it must be later than database wall time checked after the wallet/account lock.
The same time is used when subtracting other active holds from the available
balance. Existing exact retries are checked first and remain discoverable after
their deadline; retries never extend an existing reservation.

Settled-withdrawal capture also checks database wall time after acquiring the
wallet/account and hold locks, rather than transaction-start time. Waiting for a
lock cannot extend the hold. An expired capture rolls back without a journal,
capture state or settlement event. The capture admission instant is reused for
the conditional update while the locks remain held. Already-posted exact retries
still return their existing transaction before this check.

An expired hold does not prove that an external payment failed. Payment Rails must
retain confirmed or uncertain provider outcomes for reconciliation; it must not
resend money or treat this rejection as a refund. Durable reservation through an
external provider's uncertain processing window remains a separate workflow gate.

This is service-layer enforcement. The existing HTTP hold request does not expose
an expiry input, and this change does not add one or broaden tenant permissions.
Release/capture workflows and transaction authorization retain their existing
controls. No operational records are backfilled by this change.

`TestHoldExpiryRetryAndAdmission` runs only with a disposable database. It verifies
same-instant replay, changed/null expiry conflicts, expired new-hold rejection,
tenant isolation and one hold event with the unchanged reserved amount.
Its capture subtest holds the wallet row lock until the saved expiry passes, then
verifies that the waiting withdrawal rejects and leaves no journal posting.
