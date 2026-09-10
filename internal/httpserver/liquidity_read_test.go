package httpserver

import (
	"github.com/Mightyfin/wallet-ledger/internal/auth"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiquidityReadRequiresSeparateDelegatedAuthority(t *testing.T) {
	for _, tc := range []struct {
		tenant, role, scope string
		want                int
	}{
		{"tenant", "lender-liquidity-reader", "wallet.liquidity.read", 403},
		{"", "lender-liquidity-reserver", "wallet.liquidity.read", 403},
		{"", "lender-liquidity-reader", "wallet.liquidity.reserve", 403},
		{"", "lender-liquidity-reader", "wallet.liquidity.read", 400},
	} {
		mux := http.NewServeMux()
		(&api{environment: "sandbox"}).routes(mux)
		p := auth.Principal{Subject: "reader", TenantID: tc.tenant, Roles: map[string]struct{}{tc.role: {}, "platform-tenant-delegator": {}}, Scopes: map[string]struct{}{tc.scope: {}}}
		h := authenticate(fakeVerifier{principal: p}, false, mux)
		r := httptest.NewRequest("GET", "/v1/internal/lender-liquidity/reservations/lqr?tenant_id=override", nil)
		r.Header.Set("Authorization", "Bearer test")
		r.Header.Set("X-Acting-Tenant-Id", "tenant")
		r.Header.Set("X-Acting-Application-Id", "app")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}
