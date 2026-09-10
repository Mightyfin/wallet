package httpserver

import (
	"net/http"
	"strings"
)

func (a *api) readLenderLiquidity(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !p.HasRole("lender-liquidity-reader") || !p.HasRole("platform-tenant-delegator") || !p.HasScope("wallet.liquidity.read") || p.Subject == "" || p.TenantID == "" || strings.TrimSpace(r.Header.Get("X-Acting-Tenant-Id")) != p.TenantID {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	query := r.URL.Query()
	if len(query) != 1 || len(query["legal_entity_id"]) != 1 || strings.TrimSpace(query.Get("legal_entity_id")) == "" {
		writeJSON(w, 400, map[string]string{"error": "legal_entity_id_required"})
		return
	}
	result, err := a.financial.ReadLenderLiquidity(r.Context(), p.TenantID, query.Get("legal_entity_id"), r.PathValue("reservation_id"))
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	result.Environment = a.environment
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
