package httpserver

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

func (a *api) readGoodsCapacity(w http.ResponseWriter, r *http.Request) {
	if !a.goodsAuthority(w, r, "wallet.goods.read") {
		return
	}
	entity, invalid := parseGoodsCapacityQuery(r)
	if invalid {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	result, err := a.financial.ReadGoodsCapacity(r.Context(), principal(r).TenantID, entity, r.PathValue("authorization_id"))
	if err != nil {
		writeFinancialError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"capacity": result, "environment": a.environment})
}

func parseGoodsCapacityQuery(r *http.Request) (string, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", true
	}
	if len(q) != 1 || len(q["legal_entity_id"]) != 1 {
		return "", true
	}
	// ParseQuery explicitly rejects malformed query encodings rather than quietly
	// dropping them and accepting the remaining fields.
	for _, v := range []string{q.Get("legal_entity_id"), r.PathValue("authorization_id")} {
		if v == "" || len(v) > 128 || strings.TrimSpace(v) != v || strings.ContainsFunc(v, unicode.IsControl) {
			return "", true
		}
	}
	return q.Get("legal_entity_id"), false
}
