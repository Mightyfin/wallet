package httpserver

import (
	"net/http"
	"strings"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
)

func legalEntityReader(p auth.Principal, r *http.Request) bool {
	return p.Subject != "" && p.TenantID == "" && p.HasRole("platform-tenant-delegator") && p.HasRole("legal-entity-reader") && p.HasScope("wallet.legal_entity.read") &&
		r.Header.Get("X-Acting-Tenant-Id") == "" && r.Header.Get("X-Acting-Application-Id") == ""
}

func (a *api) readLegalEntity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p := principal(r)
	if !legalEntityReader(p, r) || p.Environment != a.environment {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	id := r.PathValue("entity_id")
	if r.URL.RawQuery != "" || id == "" || len(id) > 64 || strings.TrimSpace(id) != id {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	entity, err := a.financial.LegalEntityRecord(r.Context(), id)
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	// Bank verification does not need the entity's display name or any balances.
	writeJSON(w, 200, map[string]string{"id": entity.ID, "status": entity.Status, "environment": a.environment, "base_currency": entity.BaseCurrency})
}
