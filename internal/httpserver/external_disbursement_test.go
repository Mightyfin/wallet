package httpserver

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
)

func TestManualBankPostingRequiresDedicatedWorkload(t *testing.T) {
	for _, tc := range []struct {
		role, scope, env, expected string
		delegate                   bool
		want                       int
	}{
		{"wallet-ledger-admin", "wallet.disburse", "sandbox", "sandbox", true, 403},
		{"manual-bank-reconciler", "wallet.disburse.external", "sandbox", "sandbox", false, 403},
		{"manual-bank-reconciler", "wallet.disburse", "sandbox", "sandbox", true, 403},
		{"manual-bank-reconciler", "wallet.disburse.external", "production", "production", true, 403},
		{"manual-bank-reconciler", "wallet.disburse.external", "sandbox", "production", true, 403},
		{"manual-bank-reconciler", "wallet.disburse.external", "sandbox", "sandbox", true, 503},
	} {
		p := auth.Principal{Subject: "workload", TenantID: "green", ApplicationID: "reconciler", Environment: tc.env, Scopes: map[string]struct{}{tc.scope: {}}, Roles: map[string]struct{}{tc.role: {}}}
		if tc.delegate {
			p.Roles["platform-tenant-delegator"] = struct{}{}
		}
		r := httptest.NewRequest("POST", "/v1/internal/loan-disbursements/external-bank", strings.NewReader(`{"payment_id":"mpay_one"}`))
		r.Header.Set("X-Acting-Tenant-Id", "green")
		r.Header.Set("X-Acting-Application-Id", "reconciler")
		r.Header.Set("Idempotency-Key", "mpay_one")
		r.Header.Set("X-Expected-Environment", tc.expected)
		r = r.WithContext(context.WithValue(r.Context(), principalKey, p))
		w := httptest.NewRecorder()
		(&api{environment: "sandbox"}).recordConfirmedBankDisbursement(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v got %d: %s", tc, w.Code, w.Body.String())
		}
	}
}
