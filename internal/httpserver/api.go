package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
	"github.com/Mightyfin/wallet-ledger/internal/financial"
)

type api struct{ financial *financial.Service }

func (a *api) routes(mux *http.ServeMux) {
	mux.Handle("POST /v1/legal-entities", require("wallet.admin", "wallet-ledger-admin", http.HandlerFunc(a.createLegalEntity)))
	mux.Handle("POST /v1/wallets", require("wallet.write", "wallet-ledger-admin", http.HandlerFunc(a.createWallet)))
	// Both legacy URLs share this route to avoid intersecting ServeMux wildcards.
	mux.Handle("GET /v1/wallets/{first}/{second}", require("wallet.read", "wallet-ledger-admin", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("first") == "by-owner" {
			r.SetPathValue("party_id", r.PathValue("second"))
			a.walletsForOwner(w, r)
		} else if r.PathValue("second") == "balance" {
			r.SetPathValue("wallet_id", r.PathValue("first"))
			a.balance(w, r)
		} else {
			http.NotFound(w, r)
		}
	})))
	mux.Handle("POST /v1/transfers", require("wallet.transfer", "wallet-ledger-admin", http.HandlerFunc(a.transfer)))
	mux.Handle("POST /v1/loan-disbursements", require("wallet.disburse", "wallet-ledger-admin", http.HandlerFunc(a.disburseLoan)))
	mux.Handle("POST /v1/loan-repayments/wallet", require("wallet.repay", "wallet-ledger-admin", http.HandlerFunc(a.repayLoanFromWallet)))
	mux.Handle("POST /v1/loan-repayments/external-settlements", require("wallet.settlement", "wallet-ledger-admin", http.HandlerFunc(a.recordExternalRepayment)))
	mux.Handle("POST /v1/deposits/external-settlements", require("wallet.settlement", "wallet-ledger-admin", http.HandlerFunc(a.recordSettledDeposit)))
	mux.Handle("POST /v1/withdrawals/external-settlements", require("wallet.settlement", "wallet-ledger-admin", http.HandlerFunc(a.recordSettledWithdrawal)))
	mux.Handle("POST /v1/wallets/{wallet_id}/holds", require("wallet.hold", "wallet-ledger-admin", http.HandlerFunc(a.createHold)))
	mux.Handle("POST /v1/wallets/{wallet_id}/holds/{hold_id}/release", require("wallet.hold", "wallet-ledger-admin", http.HandlerFunc(a.releaseHold)))
}

func (a *api) walletsForOwner(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	legalEntityID := strings.TrimSpace(r.URL.Query().Get("legal_entity_id"))
	partyID := strings.TrimSpace(r.PathValue("party_id"))
	if legalEntityID == "" || partyID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "legal_entity_id_and_party_id_required"})
		return
	}
	wallets, err := a.financial.WalletsForOwner(r.Context(), legalEntityID, p.TenantID, partyID, r.URL.Query().Get("currency"))
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": wallets})
}

func (a *api) createLegalEntity(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		CountryCode  string `json:"country_code"`
		BaseCurrency string `json:"base_currency"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	entity, err := a.financial.CreateLegalEntity(r.Context(), body.Name, body.CountryCode, body.BaseCurrency)
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, entity)
}

func (a *api) createWallet(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var body struct {
		LegalEntityID string `json:"legal_entity_id"`
		OwnerType     string `json:"owner_type"`
		OwnerID       string `json:"owner_id"`
		Currency      string `json:"currency"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	wallet, err := a.financial.CreateWallet(r.Context(), financial.Wallet{LegalEntityID: body.LegalEntityID, TenantID: p.TenantID, OwnerType: body.OwnerType, OwnerID: body.OwnerID, Currency: body.Currency})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, wallet)
}

func (a *api) balance(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	entityID := strings.TrimSpace(r.URL.Query().Get("legal_entity_id"))
	balance, err := a.financial.GetBalance(r.Context(), entityID, p.TenantID, r.PathValue("wallet_id"))
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"wallet_id": balance.WalletID, "currency": balance.Currency, "ledger_balance": balance.Ledger.StringFixed(2), "held_balance": balance.Held.StringFixed(2), "available_balance": balance.Available.StringFixed(2)})
}

func (a *api) transfer(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	idempotencyKey, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		LegalEntityID       string `json:"legal_entity_id"`
		SourceWalletID      string `json:"source_wallet_id"`
		DestinationWalletID string `json:"destination_wallet_id"`
		Amount              string `json:"amount"`
		Currency            string `json:"currency"`
		ExternalReference   string `json:"external_reference"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := a.financial.Transfer(r.Context(), financial.Transfer{LegalEntityID: body.LegalEntityID, TenantID: p.TenantID, SourceWalletID: body.SourceWalletID, DestinationWalletID: body.DestinationWalletID, Amount: body.Amount, Currency: body.Currency, IdempotencyKey: idempotencyKey, ExternalReference: body.ExternalReference, CorrelationID: r.Header.Get("X-Correlation-Id"), SourceSystem: p.ApplicationID})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeTransaction(w, result)
}

func (a *api) disburseLoan(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	idempotencyKey, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		LegalEntityID          string `json:"legal_entity_id"` // Optional for a delegated facility workload; Wallet resolves it from the destination wallet.
		DestinationWalletID    string `json:"destination_wallet_id"`
		FacilityID             string `json:"facility_id"`
		DisbursementID         string `json:"disbursement_id"`
		FundingSourceReference string `json:"funding_source_reference"`
		Amount                 string `json:"amount"`
		Currency               string `json:"currency"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := a.financial.DisburseLoan(r.Context(), financial.LoanDisbursement{LegalEntityID: body.LegalEntityID, TenantID: p.TenantID, DestinationWalletID: body.DestinationWalletID, FacilityID: body.FacilityID, DisbursementID: body.DisbursementID, FundingSourceReference: body.FundingSourceReference, Amount: body.Amount, Currency: body.Currency, IdempotencyKey: idempotencyKey, CorrelationID: r.Header.Get("X-Correlation-Id"), SourceSystem: p.ApplicationID})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeTransaction(w, result)
}

func (a *api) repayLoanFromWallet(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		LegalEntityID string `json:"legal_entity_id"`
		WalletID      string `json:"wallet_id"`
		FacilityID    string `json:"facility_id"`
		Amount        string `json:"amount"`
		Currency      string `json:"currency"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := a.financial.RepayLoanFromWallet(r.Context(), financial.WalletRepayment{LegalEntityID: body.LegalEntityID, TenantID: p.TenantID, WalletID: body.WalletID, FacilityID: body.FacilityID, Amount: body.Amount, Currency: body.Currency, IdempotencyKey: key, CorrelationID: r.Header.Get("X-Correlation-Id"), SourceSystem: p.ApplicationID})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeTransaction(w, result)
}

func (a *api) recordExternalRepayment(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		LegalEntityID       string `json:"legal_entity_id"`
		WalletID            string `json:"wallet_id"`
		FacilityID          string `json:"facility_id"`
		SettlementReference string `json:"settlement_reference"`
		EvidenceID          string `json:"evidence_id"`
		Amount              string `json:"amount"`
		Currency            string `json:"currency"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := a.financial.RecordSettledExternalRepayment(r.Context(), financial.SettledExternalRepayment{LegalEntityID: body.LegalEntityID, TenantID: p.TenantID, WalletID: body.WalletID, FacilityID: body.FacilityID, SettlementReference: body.SettlementReference, EvidenceID: body.EvidenceID, Amount: body.Amount, Currency: body.Currency, IdempotencyKey: key, CorrelationID: r.Header.Get("X-Correlation-Id"), SourceSystem: p.ApplicationID})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeTransaction(w, result)
}

func (a *api) recordSettledDeposit(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		LegalEntityID       string `json:"legal_entity_id"`
		WalletID            string `json:"wallet_id"`
		SettlementReference string `json:"settlement_reference"`
		EvidenceID          string `json:"evidence_id"`
		Amount              string `json:"amount"`
		Currency            string `json:"currency"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := a.financial.RecordSettledDeposit(r.Context(), financial.SettledDeposit{LegalEntityID: body.LegalEntityID, TenantID: p.TenantID, WalletID: body.WalletID, SettlementReference: body.SettlementReference, EvidenceID: body.EvidenceID, Amount: body.Amount, Currency: body.Currency, IdempotencyKey: key, CorrelationID: r.Header.Get("X-Correlation-Id"), SourceSystem: p.ApplicationID})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeTransaction(w, result)
}

func (a *api) recordSettledWithdrawal(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		LegalEntityID       string `json:"legal_entity_id"`
		WalletID            string `json:"wallet_id"`
		HoldID              string `json:"hold_id"`
		SettlementReference string `json:"settlement_reference"`
		EvidenceID          string `json:"evidence_id"`
		Amount              string `json:"amount"`
		Currency            string `json:"currency"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := a.financial.RecordSettledWithdrawal(r.Context(), financial.SettledWithdrawal{LegalEntityID: body.LegalEntityID, TenantID: p.TenantID, WalletID: body.WalletID, HoldID: body.HoldID, SettlementReference: body.SettlementReference, EvidenceID: body.EvidenceID, Amount: body.Amount, Currency: body.Currency, IdempotencyKey: key, CorrelationID: r.Header.Get("X-Correlation-Id"), SourceSystem: p.ApplicationID})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeTransaction(w, result)
}

func (a *api) createHold(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		LegalEntityID string `json:"legal_entity_id"`
		Amount        string `json:"amount"`
		Currency      string `json:"currency"`
		Reason        string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	hold, err := a.financial.CreateHold(r.Context(), financial.HoldRequest{LegalEntityID: body.LegalEntityID, TenantID: p.TenantID, WalletID: r.PathValue("wallet_id"), Amount: body.Amount, Currency: body.Currency, Reason: body.Reason, IdempotencyKey: key})
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, hold)
}

func (a *api) releaseHold(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	entityID := strings.TrimSpace(r.URL.Query().Get("legal_entity_id"))
	if entityID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "legal_entity_id_required"})
		return
	}
	hold, err := a.financial.ReleaseHold(r.Context(), entityID, p.TenantID, r.PathValue("wallet_id"), r.PathValue("hold_id"))
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hold)
}

func writeTransaction(w http.ResponseWriter, transaction financial.Transaction) {
	writeJSON(w, http.StatusCreated, map[string]string{"transaction_id": transaction.ID, "status": transaction.Status, "currency": transaction.Currency, "amount": transaction.Amount.StringFixed(2), "idempotency_key": transaction.IdempotencyKey, "correlation_id": transaction.CorrelationID})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return false
	}
	return true
}

func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) < 8 || len(key) > 128 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_idempotency_key"})
		return "", false
	}
	return key, true
}

func writeFinancialError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, financial.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	case errors.Is(err, financial.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "conflict"})
	case errors.Is(err, financial.ErrInsufficientBalance):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "insufficient_available_balance"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
	}
}

func require(scope, role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := r.Context().Value(principalKey).(auth.Principal)
		if !ok || (!p.HasScope(scope) && !p.HasRole(role)) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func principal(r *http.Request) auth.Principal {
	p, _ := r.Context().Value(principalKey).(auth.Principal)
	return p
}
