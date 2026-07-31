package httpserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
)

type contextKey string

const principalKey contextKey = "principal"

func authenticate(verifier auth.Verifier, disabled bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		if disabled {
			tenant := strings.TrimSpace(r.Header.Get("X-Tenant-Id"))
			if tenant == "" {
				tenant = "local-test"
			}
			p := auth.Principal{Subject: "local-developer", TenantID: tenant, ApplicationID: "local-client", Environment: "local", Scopes: map[string]struct{}{"wallet.admin": {}, "wallet.write": {}, "wallet.read": {}, "wallet.transfer": {}, "wallet.disburse": {}, "wallet.repay": {}, "wallet.settlement": {}, "wallet.hold": {}}, Roles: map[string]struct{}{"wallet-ledger-admin": {}}}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
			return
		}
		scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		p, err := verifier.Verify(r.Context(), strings.TrimSpace(token))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		actingTenant := strings.TrimSpace(r.Header.Get("X-Acting-Tenant-Id"))
		actingApplication := strings.TrimSpace(r.Header.Get("X-Acting-Application-Id"))
		if p.TenantID == "" {
			if !p.HasRole("platform-tenant-delegator") || actingTenant == "" || actingApplication == "" || len(actingTenant) > 64 || len(actingApplication) > 64 {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "delegation_forbidden"})
				return
			}
			p.TenantID = actingTenant
			p.ApplicationID = actingApplication
		} else if actingTenant != "" || actingApplication != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "delegation_forbidden"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}
