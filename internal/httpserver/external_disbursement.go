package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/financial"
)

// Only the dedicated payment reconciliation workload can submit the verified
// instruction. Ordinary wallet admin roles and tenant scopes are insufficient.
func (a *api) recordConfirmedBankDisbursement(w http.ResponseWriter, r *http.Request) {
	if !a.financialWorkload(w, r, "manual-bank-reconciler", "wallet.disburse.external") {
		return
	}
	if r.Header.Get("X-Expected-Environment") != a.environment {
		writeJSON(w, 403, map[string]string{"error": "environment_mismatch"})
		return
	}
	if r.URL.RawQuery != "" {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	var in struct {
		LegalEntityID        string    `json:"legal_entity_id"`
		WalletID             string    `json:"wallet_id"`
		PartyID              string    `json:"party_id"`
		FacilityID           string    `json:"facility_id"`
		PaymentID            string    `json:"payment_id"`
		AuthorizationID      string    `json:"authorization_id"`
		SourceAccountID      string    `json:"source_account_id"`
		DestinationAccountID string    `json:"destination_account_id"`
		EvidenceDigest       string    `json:"evidence_digest"`
		ReviewedBy           string    `json:"reviewed_by"`
		AmountMinor          int64     `json:"amount_minor"`
		Currency             string    `json:"currency"`
		PaidAt               time.Time `json:"paid_at"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || in.PaymentID == "" || r.Header.Get("Idempotency-Key") != in.PaymentID {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	if a.financial == nil {
		writeJSON(w, 503, map[string]string{"error": "ledger_unavailable"})
		return
	}
	p := principal(r)
	result, err := a.financial.RecordConfirmedBankDisbursement(r.Context(), financial.ConfirmedBankDisbursement{LegalEntityID: in.LegalEntityID, TenantID: p.TenantID, Environment: p.Environment, WalletID: in.WalletID, PartyID: in.PartyID, FacilityID: in.FacilityID, PaymentID: in.PaymentID, AuthorizationID: in.AuthorizationID, SourceAccountID: in.SourceAccountID, DestinationAccountID: in.DestinationAccountID, EvidenceDigest: in.EvidenceDigest, ReviewedBy: in.ReviewedBy, AmountMinor: in.AmountMinor, Currency: in.Currency, PaidAt: in.PaidAt})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeTransaction(w, result)
}
