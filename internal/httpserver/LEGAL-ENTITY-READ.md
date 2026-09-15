# Lender registry lookup

`GET /v1/internal/legal-entities/{entity_id}` returns only the entity ID,
status, base currency and deployment environment. It does not verify bank
ownership, liquidity, KYC completion, capital reservation or payment authority.

The verified token must have no tenant and must carry both
`platform-tenant-delegator` and `legal-entity-reader` roles plus
`wallet.legal_entity.read`. Acting-tenant/application headers are prohibited.
Ordinary wallet routes retain their tenant-delegation requirements. The new
permission does not authorize other wallet operations.

Consumers must reject inactive entities; this read endpoint returns their
actual status for diagnosis. Currency is the entity's accounting base currency,
not a declaration that it can only maintain bank accounts in that currency.

Tests cover authorization and ordinary-route isolation, plus active/suspended/
closed and missing records in a disposable PostgreSQL database. Runtime client
permissions and the Payment Rails owner adapter have not yet been deployed.
