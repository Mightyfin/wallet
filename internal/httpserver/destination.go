package httpserver

import (
	"encoding/json"
	"github.com/Mightyfin/wallet-ledger/internal/financial"
	"io"
	"net/http"
	"strings"
)

func (a *api) verifyDestination(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !p.HasRole("wallet-destination-verifier") || !p.HasRole("platform-tenant-delegator") || !p.HasScope("wallet.destination.verify") || p.Subject == "" || p.TenantID == "" || strings.TrimSpace(r.Header.Get("X-Acting-Tenant-Id")) != p.TenantID {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	var body struct {
		LegalEntityID string `json:"legal_entity_id"`
		WalletID      string `json:"wallet_id"`
		PartyID       string `json:"party_id"`
		Currency      string `json:"currency"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	result, err := a.financial.VerifyDestination(r.Context(), financial.DestinationRequest{LegalEntityID: body.LegalEntityID, TenantID: p.TenantID, WalletID: body.WalletID, PartyID: body.PartyID, Currency: body.Currency})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	result.Environment = a.environment
	writeJSON(w, 200, result)
}
