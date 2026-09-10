package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
)

func TestLenderLiquidityRejectsTenantAndGenericWalletPermissions(t *testing.T) {
	for _, tc := range []struct {
		tenant, role, scope string
		want                int
	}{
		{"tenant", "lender-liquidity-reserver", "wallet.liquidity.reserve", 403},
		{"", "wallet-ledger-admin", "wallet.liquidity.reserve", 403},
		{"", "lender-liquidity-reserver", "wallet.hold", 403},
		{"", "lender-liquidity-reserver", "wallet.liquidity.reserve", 400},
	} {
		mux := http.NewServeMux()
		(&api{}).routes(mux)
		p := auth.Principal{Subject: "service", TenantID: tc.tenant, Scopes: map[string]struct{}{tc.scope: {}}, Roles: map[string]struct{}{tc.role: {}, "platform-tenant-delegator": {}}}
		h := authenticate(fakeVerifier{principal: p}, false, mux)
		r := httptest.NewRequest("POST", "/v1/internal/lender-liquidity/reservations", strings.NewReader(`{"tenant_id":"override"}`))
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
