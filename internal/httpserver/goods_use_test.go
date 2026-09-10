package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
)

func TestGoodsUsePermissionsAndValidation(t *testing.T) {
	for _, method := range []string{"POST", "GET"} {
		for _, tc := range []struct {
			name, role, scope, tenant, env string
			want                           int
		}{
			{"tenant", "goods-credit-executor", "wallet.goods.use", "tenant", "sandbox", 403},
			{"registration-only", "goods-credit-authorizer", "wallet.goods.use", "", "sandbox", 403},
			{"admin", "wallet-ledger-admin", "wallet.goods.use", "", "sandbox", 403},
			{"scope", "goods-credit-executor", "wallet.goods.authorize", "", "sandbox", 403},
			{"environment", "goods-credit-executor", "wallet.goods.use", "", "production", 403},
			{"valid-invalid-payload", "goods-credit-executor", "wallet.goods.use", "", "sandbox", 400},
		} {
			t.Run(method+tc.name, func(t *testing.T) {
				scope := tc.scope
				if method == "GET" && scope == "wallet.goods.use" {
					scope = "wallet.goods.read"
				}
				p := auth.Principal{Subject: "executor", TenantID: tc.tenant, Environment: tc.env, Roles: map[string]struct{}{tc.role: {}, "platform-tenant-delegator": {}}, Scopes: map[string]struct{}{scope: {}}}
				mux := http.NewServeMux()
				(&api{environment: "sandbox"}).routes(mux)
				path := "/v1/internal/goods-credit/authorizations/gca_test/uses"
				if method == "GET" {
					path += "/use?tenant_id=forged"
				}
				r := httptest.NewRequest(method, path, strings.NewReader(`{"amount":"1.00","destination_wallet_id":"forged"}`))
				r.Header.Set("Authorization", "Bearer synthetic")
				r.Header.Set("X-Acting-Tenant-Id", "tenant")
				r.Header.Set("X-Acting-Application-Id", "app")
				w := httptest.NewRecorder()
				authenticate(fakeVerifier{principal: p}, false, mux).ServeHTTP(w, r)
				if w.Code != tc.want {
					t.Fatal(w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestGoodsUseRejectsMalformedAmounts(t *testing.T) {
	for _, amount := range []string{"1e2", "01.00", "-1.00", "1.001", " 1.00", "1000000000000000000.00"} {
		if goodsUseAmount.MatchString(amount) {
			t.Fatal(amount)
		}
	}
}

func TestGoodsUseRejectsIncompleteRequestsBeforeStorage(t *testing.T) {
	p := auth.Principal{Subject: "executor", Environment: "sandbox", Roles: map[string]struct{}{"goods-credit-executor": {}, "platform-tenant-delegator": {}}, Scopes: map[string]struct{}{"wallet.goods.use": {}}}
	mux := http.NewServeMux()
	(&api{environment: "sandbox"}).routes(mux)
	for _, tc := range []struct{ body, key string }{
		{`{"legal_entity_id":"entity","use_id":"use","amount":"0.00"}`, "use"},
		{`{"legal_entity_id":"entity","use_id":"use","amount":"1.00"}`, "changed"},
		{`{"legal_entity_id":"entity","use_id":"use","amount":"1.00"}`, ""},
		{`{"use_id":"use","amount":"1.00"}`, "use"},
		{`{"legal_entity_id":"entity","amount":"1.00"}`, "use"},
		{`{"legal_entity_id":"entity","use_id":"use"}`, "use"},
		{`{"legal_entity_id":"entity","use_id":"use","amount":1}`, "use"},
		{`{"legal_entity_id":"entity","use_id":"use","amount":"1.00"} {}`, "use"},
		{`{"legal_entity_id":"entity","use_id":"use","amount":"1.00","requested_by":"forged"}`, "use"},
	} {
		r := httptest.NewRequest("POST", "/v1/internal/goods-credit/authorizations/gca_test/uses", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer synthetic")
		r.Header.Set("X-Acting-Tenant-Id", "tenant")
		r.Header.Set("X-Acting-Application-Id", "app")
		r.Header.Set("Idempotency-Key", tc.key)
		w := httptest.NewRecorder()
		authenticate(fakeVerifier{principal: p}, false, mux).ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(tc.body, w.Code, w.Body.String())
		}
	}
}
