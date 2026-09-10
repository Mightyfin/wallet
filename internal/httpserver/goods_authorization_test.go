package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
)

func TestGoodsAuthorizationPermissions(t *testing.T) {
	for _, method := range []string{"POST", "GET"} {
		for _, tc := range []struct {
			name, tenant, role, scope, environment string
			want                                   int
		}{
			{"tenant", "tenant", "goods-credit-authorizer", "wallet.goods.authorize", "sandbox", 403},
			{"generic-admin", "", "wallet-ledger-admin", "wallet.goods.authorize", "sandbox", 403},
			{"liquidity-only", "", "lender-liquidity-reserver", "wallet.goods.authorize", "sandbox", 403},
			{"wrong-scope", "", "goods-credit-authorizer", "wallet.disburse", "sandbox", 403},
			{"wrong-environment", "", "goods-credit-authorizer", "wallet.goods.authorize", "production", 403},
			{"valid-authority-invalid-body", "", "goods-credit-authorizer", "wallet.goods.authorize", "sandbox", 400},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				scope := tc.scope
				if method == "GET" && scope == "wallet.goods.authorize" {
					scope = "wallet.goods.read"
				}
				p := auth.Principal{Subject: "facility-workload", TenantID: tc.tenant, Environment: tc.environment, Scopes: map[string]struct{}{scope: {}}, Roles: map[string]struct{}{tc.role: {}, "platform-tenant-delegator": {}}}
				mux := http.NewServeMux()
				(&api{environment: "sandbox"}).routes(mux)
				h := authenticate(fakeVerifier{principal: p}, false, mux)
				path := "/v1/internal/goods-credit/authorizations"
				if method == "GET" {
					path += "/gca_fac?tenant_id=override"
				}
				r := httptest.NewRequest(method, path, strings.NewReader(`{"authorized_by":"forged"}`))
				r.Header.Set("Authorization", "Bearer synthetic")
				r.Header.Set("X-Acting-Tenant-Id", "tenant")
				r.Header.Set("X-Acting-Application-Id", "app")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != tc.want {
					t.Fatal(w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestGoodsAuthorizationRejectsIncompleteInstructionBeforeStorage(t *testing.T) {
	for _, field := range []string{"allow_partial_use", "expires_at", "reservation_id", "borrower_party_id", "supplier_party_id", "destination_wallet_id", "order_reference", "amount", "currency"} {
		t.Run(field, func(t *testing.T) {
			body := map[string]any{"authorization_id": "gca_fac", "facility_id": "fac", "legal_entity_id": "lender", "reservation_id": "rsv", "borrower_party_id": "buyer", "supplier_party_id": "supplier", "destination_wallet_id": "wallet", "order_reference": "order", "amount": "70.00", "currency": "ZMW", "allow_partial_use": true, "expires_at": "2030-01-01T00:00:00Z"}
			delete(body, field)
			raw, _ := json.Marshal(body)
			p := auth.Principal{Subject: "facility", Environment: "sandbox", Scopes: map[string]struct{}{"wallet.goods.authorize": {}}, Roles: map[string]struct{}{"goods-credit-authorizer": {}, "platform-tenant-delegator": {}}}
			mux := http.NewServeMux()
			(&api{environment: "sandbox"}).routes(mux)
			r := httptest.NewRequest("POST", "/v1/internal/goods-credit/authorizations", strings.NewReader(string(raw)))
			r.Header.Set("Authorization", "Bearer synthetic")
			r.Header.Set("X-Acting-Tenant-Id", "tenant")
			r.Header.Set("X-Acting-Application-Id", "app")
			r.Header.Set("Idempotency-Key", "gca_fac")
			w := httptest.NewRecorder()
			authenticate(fakeVerifier{principal: p}, false, mux).ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
