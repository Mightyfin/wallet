package httpserver

import (
	"github.com/Mightyfin/wallet-ledger/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDestinationVerificationRequiresDedicatedDelegation(t *testing.T) {
	for _, tc := range []struct {
		tenant, role, scope string
		want                int
	}{{"tenant", "wallet-destination-verifier", "wallet.destination.verify", 403}, {"", "wallet-ledger-admin", "wallet.destination.verify", 403}, {"", "wallet-destination-verifier", "wallet.read", 403}, {"", "wallet-destination-verifier", "wallet.destination.verify", 400}} {
		mux := http.NewServeMux()
		(&api{environment: "sandbox"}).routes(mux)
		p := auth.Principal{Subject: "service", TenantID: tc.tenant, Scopes: map[string]struct{}{tc.scope: {}}, Roles: map[string]struct{}{tc.role: {}, "platform-tenant-delegator": {}}}
		handler := authenticate(fakeVerifier{principal: p}, false, mux)
		r := httptest.NewRequest("POST", "/v1/internal/wallet-destinations/verify", strings.NewReader(`{"tenant_id":"override"}`))
		r.Header.Set("Authorization", "Bearer test")
		r.Header.Set("X-Acting-Tenant-Id", "tenant")
		r.Header.Set("X-Acting-Application-Id", "app")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}
