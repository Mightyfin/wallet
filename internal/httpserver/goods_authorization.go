package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/Mightyfin/wallet-ledger/internal/financial"
	"github.com/jackc/pgx/v5/pgconn"
)

// This boundary accepts only a dedicated Facility workload, never a tenant or
// generic wallet administrator. Registration records authority; it moves no money.
func (a *api) goodsAuthority(w http.ResponseWriter, r *http.Request, scope string) bool {
	return a.goodsWorkload(w, r, "goods-credit-authorizer", scope)
}

func (a *api) goodsWorkload(w http.ResponseWriter, r *http.Request, role, scope string) bool {
	p := principal(r)
	w.Header().Set("Cache-Control", "no-store")
	if !p.HasRole(role) || !p.HasRole("platform-tenant-delegator") || !p.HasScope(scope) ||
		p.Subject == "" || p.TenantID == "" || p.ApplicationID == "" || p.Environment != a.environment ||
		strings.TrimSpace(r.Header.Get("X-Acting-Tenant-Id")) != p.TenantID ||
		strings.TrimSpace(r.Header.Get("X-Acting-Application-Id")) != p.ApplicationID {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return false
	}
	return true
}

func (a *api) registerGoodsAuthorization(w http.ResponseWriter, r *http.Request) {
	if !a.goodsAuthority(w, r, "wallet.goods.authorize") {
		return
	}
	if r.URL.RawQuery != "" {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	var in struct {
		ID                  string    `json:"authorization_id"`
		LegalEntityID       string    `json:"legal_entity_id"`
		FacilityID          string    `json:"facility_id"`
		ReservationID       string    `json:"reservation_id"`
		BorrowerPartyID     string    `json:"borrower_party_id"`
		SupplierPartyID     string    `json:"supplier_party_id"`
		DestinationWalletID string    `json:"destination_wallet_id"`
		OrderReference      string    `json:"order_reference"`
		Amount              string    `json:"amount"`
		Currency            string    `json:"currency"`
		AllowPartialUse     *bool     `json:"allow_partial_use"`
		ExpiresAt           time.Time `json:"expires_at"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || in.FacilityID == "" || in.ID != "gca_"+in.FacilityID ||
		strings.TrimSpace(r.Header.Get("Idempotency-Key")) != in.ID || in.LegalEntityID == "" || in.AllowPartialUse == nil || in.ExpiresAt.IsZero() || len(in.ID) > 64 {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	for _, v := range []string{in.ID, in.LegalEntityID, in.FacilityID, in.ReservationID, in.BorrowerPartyID, in.SupplierPartyID, in.DestinationWalletID, in.OrderReference, in.Amount, in.Currency} {
		if v == "" || len(v) > 128 || strings.TrimSpace(v) != v || strings.ContainsFunc(v, unicode.IsControl) {
			writeJSON(w, 400, map[string]string{"error": "invalid_request"})
			return
		}
	}
	p := principal(r)
	// Read the reservation's immutable binding. Registration itself locks and
	// checks active accounts; an exact historical retry remains recoverable.
	reservation, err := a.financial.ReadLenderLiquidity(r.Context(), p.TenantID, in.LegalEntityID, in.ReservationID)
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	if reservation.Reservation.FacilityID != in.FacilityID || reservation.Reservation.Amount != in.Amount ||
		reservation.Reservation.Currency != in.Currency || reservation.Reservation.Status != "reserved" {
		writeJSON(w, 409, map[string]string{"error": "reservation_mismatch"})
		return
	}
	err = a.financial.RegisterGoodsAuthorization(r.Context(), financial.GoodsAuthorization{
		ID: in.ID, TenantID: p.TenantID, ReservationID: in.ReservationID, BorrowerPartyID: in.BorrowerPartyID,
		SupplierPartyID: in.SupplierPartyID, DestinationWalletID: in.DestinationWalletID, OrderReference: in.OrderReference,
		Amount: in.Amount, Currency: in.Currency, AllowPartialUse: *in.AllowPartialUse, ExpiresAt: in.ExpiresAt, AuthorizedBy: p.Subject,
	})
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && (pg.Code == "23514" || pg.Code == "23505" || pg.Code == "23503") {
			writeJSON(w, 409, map[string]string{"error": "authorization_conflict"})
			return
		}
		writeFinancialError(w, err)
		return
	}
	// Return stored fields, never an echo treated as successful persistence.
	result, err := a.financial.ReadGoodsAuthorization(r.Context(), p.TenantID, in.LegalEntityID, in.ID)
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"authorization": result, "environment": a.environment})
}

func (a *api) readGoodsAuthorization(w http.ResponseWriter, r *http.Request) {
	if !a.goodsAuthority(w, r, "wallet.goods.read") {
		return
	}
	q := r.URL.Query()
	if len(q) != 1 || len(q["legal_entity_id"]) != 1 || strings.TrimSpace(q.Get("legal_entity_id")) == "" {
		writeJSON(w, 400, map[string]string{"error": "legal_entity_id_required"})
		return
	}
	result, err := a.financial.ReadGoodsAuthorization(r.Context(), principal(r).TenantID, q.Get("legal_entity_id"), r.PathValue("authorization_id"))
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"authorization": result, "environment": a.environment})
}
