package httpserver

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
)

func TestExternalBankLookupRequiresReadAuthority(t *testing.T) {
	for _, mode := range []string{"read", "write-only", "read-cannot-write", "ordinary-admin", "foreign-header", "foreign-env", "spoof-body", "query"} {
		t.Run(mode, func(t *testing.T) {
			p := auth.Principal{Subject: "workload", TenantID: "green", ApplicationID: "reconciler", Environment: "sandbox", Scopes: map[string]struct{}{"wallet.disburse.external.read": {}}, Roles: map[string]struct{}{"manual-bank-reconciler": {}, "platform-tenant-delegator": {}}}
			body := `{"payment_id":"payment"}`
			path := "/v1/internal/loan-disbursements/external-bank/lookup"
			want := 403
			switch mode {
			case "read":
				want = 503 // Correct authority reaches unavailable service; no writes.
			case "write-only":
				p.Scopes = map[string]struct{}{"wallet.disburse.external": {}}
			case "ordinary-admin":
				p.Roles = map[string]struct{}{"wallet-ledger-admin": {}, "platform-tenant-delegator": {}}
			case "foreign-env":
				p.Environment = "production"
			case "spoof-body":
				body = `{"payment_id":"payment","tenant_id":"other"}`
				want = 400
			case "query":
				path += "?tenant_id=other"
				want = 400
			}
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set("X-Acting-Tenant-Id", "green")
			r.Header.Set("X-Acting-Application-Id", "reconciler")
			r.Header.Set("X-Expected-Environment", "sandbox")
			r.Header.Set("Idempotency-Key", "payment")
			if mode == "foreign-header" {
				r.Header.Set("X-Acting-Tenant-Id", "other")
			}
			r = r.WithContext(context.WithValue(r.Context(), principalKey, p))
			w := httptest.NewRecorder()
			a := &api{environment: "sandbox"}
			if mode == "read-cannot-write" {
				a.recordConfirmedBankDisbursement(w, r)
			} else {
				a.findConfirmedBankDisbursement(w, r)
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
