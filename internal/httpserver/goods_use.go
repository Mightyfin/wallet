package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgconn"
)

var goodsUseAmount = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})\.[0-9]{2}$`)

// Separate execution authority: a registration credential cannot post purchases.
func (a *api) postGoodsUse(w http.ResponseWriter, r *http.Request) {
	if !a.goodsWorkload(w, r, "goods-credit-executor", "wallet.goods.use") {
		return
	}
	var in struct {
		LegalEntityID string `json:"legal_entity_id"`
		UseID         string `json:"use_id"`
		Amount        string `json:"amount"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if r.URL.RawQuery != "" || d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF || !goodsUseAmount.MatchString(in.Amount) || in.Amount == "0.00" || r.Header.Get("Idempotency-Key") != in.UseID || !goodsUseIDs(in.LegalEntityID, in.UseID, r.PathValue("authorization_id")) {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	p := principal(r)
	// Verify immutable entity binding before any write, including exact retries.
	if _, err := a.financial.ReadGoodsAuthorization(r.Context(), p.TenantID, in.LegalEntityID, r.PathValue("authorization_id")); err != nil {
		writeFinancialError(w, err)
		return
	}
	_, err := a.financial.PostGoodsUse(r.Context(), p.TenantID, r.PathValue("authorization_id"), in.UseID, in.Amount, p.Subject)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23514" {
			writeJSON(w, 409, map[string]string{"error": "goods_use_not_permitted"})
			return
		}
		writeFinancialError(w, err)
		return
	}
	a.goodsUseResult(w, r, in.LegalEntityID, in.UseID)
}

func (a *api) readGoodsUse(w http.ResponseWriter, r *http.Request) {
	if !a.goodsWorkload(w, r, "goods-credit-executor", "wallet.goods.read") {
		return
	}
	entity, invalid := parseGoodsCapacityQuery(r)
	if invalid || !goodsUseIDs(r.PathValue("use_id")) {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	a.goodsUseResult(w, r, entity, r.PathValue("use_id"))
}

func (a *api) goodsUseResult(w http.ResponseWriter, r *http.Request, entity, useID string) {
	result, err := a.financial.ReadGoodsUse(r.Context(), principal(r).TenantID, entity, r.PathValue("authorization_id"), useID)
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"use": result, "environment": a.environment})
}

func goodsUseIDs(values ...string) bool {
	for _, v := range values {
		if v == "" || len(v) > 128 || strings.TrimSpace(v) != v || strings.ContainsFunc(v, unicode.IsControl) {
			return false
		}
	}
	return true
}
