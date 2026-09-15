package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
)

func TestLegalEntityReadDoesNotGrantWalletAccess(t *testing.T) {
	for _, tc := range []struct {
		name, role, scope, tenant, acting, path string
		want                                    int
	}{
		{"reader", "legal-entity-reader", "wallet.legal_entity.read", "", "", "/v1/internal/legal-entities/entity", 204},
		{"tenant reader", "legal-entity-reader", "wallet.legal_entity.read", "green", "", "/v1/internal/legal-entities/entity", 403},
		{"delegated tenant", "legal-entity-reader", "wallet.legal_entity.read", "", "green", "/v1/internal/legal-entities/entity", 403},
		{"wallet admin insufficient", "wallet-ledger-admin", "wallet.legal_entity.read", "", "", "/v1/internal/legal-entities/entity", 403},
		{"missing scope", "legal-entity-reader", "wallet.read", "", "", "/v1/internal/legal-entities/entity", 403},
		{"ordinary wallet still tenant scoped", "legal-entity-reader", "wallet.legal_entity.read", "", "", "/v1/wallets/test/balance", 403},
		{"subpath not allowed", "legal-entity-reader", "wallet.legal_entity.read", "", "", "/v1/internal/legal-entities/entity/extra", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := auth.Principal{Subject: "service", TenantID: tc.tenant, Environment: "sandbox", Roles: map[string]struct{}{"platform-tenant-delegator": {}, tc.role: {}}, Scopes: map[string]struct{}{tc.scope: {}}}
			r := httptest.NewRequest("GET", tc.path, nil)
			r.Header.Set("Authorization", "Bearer test")
			r.Header.Set("X-Acting-Tenant-Id", tc.acting)
			w := httptest.NewRecorder()
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if principal(r).TenantID != "" {
					t.Fatal("manufactured tenant")
				}
				w.WriteHeader(204)
			})
			authenticate(fakeVerifier{principal: p}, false, next).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
