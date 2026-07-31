package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
)

type fakeVerifier struct {
	principal auth.Principal
	err       error
}

func (f fakeVerifier) Verify(context.Context, string) (auth.Principal, error) {
	return f.principal, f.err
}

func TestAuthenticationRejectsMissingAndInvalidBearerTokens(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("protected handler called") })
	for _, test := range []struct {
		name, header string
		verifier     auth.Verifier
	}{
		{"missing", "", fakeVerifier{}},
		{"invalid", "Bearer rejected", fakeVerifier{err: errors.New("rejected")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/wallets/x/balance", nil)
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			authenticate(test.verifier, false, next).ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
}

func TestScopeAuthorization(t *testing.T) {
	p := auth.Principal{Subject: "service", TenantID: "tenant-a", Environment: "sandbox", Scopes: map[string]struct{}{"wallet.read": {}}, Roles: map[string]struct{}{}}
	verifier := fakeVerifier{principal: p}
	next := require("wallet.read", "wallet-ledger-admin", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal(r).TenantID != "tenant-a" {
			t.Fatal("tenant lost")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/wallets/x/balance", nil)
	request.Header.Set("Authorization", "Bearer accepted")
	response := httptest.NewRecorder()
	authenticate(verifier, false, next).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
