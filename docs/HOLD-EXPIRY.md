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

This is service-layer enforcement. The existing HTTP hold request does not expose
an expiry input, and this change does not add one or broaden tenant permissions.
Release/capture workflows and transaction authorization retain their existing
controls. No operational records are backfilled by this change.

`TestHoldExpiryRetryAndAdmission` runs only with a disposable database. It verifies
same-instant replay, changed/null expiry conflicts, expired new-hold rejection,
tenant isolation and one hold event with the unchanged reserved amount.
