package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Mightyfin/wallet-ledger/internal/financial"
)

func (a *api) reserveLenderLiquidity(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	// Require a dedicated platform service role AND scope. Generic wallet admin,
	// settlement, hold or tenant credentials are not lender capital authority.
	if !p.HasRole("lender-liquidity-reserver") || !p.HasRole("platform-tenant-delegator") ||
		!p.HasScope("wallet.liquidity.reserve") || p.Subject == "" || p.TenantID == "" ||
		strings.TrimSpace(r.Header.Get("X-Acting-Tenant-Id")) != p.TenantID {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	var in struct {
		LegalEntityID   string `json:"legal_entity_id"`
		AccountID       string `json:"account_id"`
		FacilityID      string `json:"facility_id"`
		AuthorizationID string `json:"authorization_id"`
		Amount          string `json:"amount"`
		Currency        string `json:"currency"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	result, err := a.financial.ReserveLenderLiquidity(r.Context(), financial.LiquidityRequest{RequestedBy: p.Subject, LegalEntityID: in.LegalEntityID, AccountID: in.AccountID, TenantID: p.TenantID, FacilityID: in.FacilityID, AuthorizationID: in.AuthorizationID, Amount: in.Amount, Currency: in.Currency})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
