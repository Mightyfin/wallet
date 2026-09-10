package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
	"github.com/Mightyfin/wallet-ledger/internal/database"
	"github.com/Mightyfin/wallet-ledger/internal/financial"
)

func TestDelegatedReleaseWithoutEntityAndReplay(t *testing.T) {
	dsn := os.Getenv("WALLET_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated disposable database required")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := financial.New(db)
	e, err := s.CreateLegalEntity(ctx, "Synthetic HTTP release", "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateWallet(ctx, financial.Wallet{LegalEntityID: e.ID, TenantID: "http-release-tenant", OwnerType: "customer", OwnerID: "synthetic", Currency: "ZMW"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DisburseLoan(ctx, financial.LoanDisbursement{TenantID: w.TenantID, DestinationWalletID: w.ID, FacilityID: newID("fac"), DisbursementID: newID("draw"), Amount: "100.00", Currency: "ZMW", IdempotencyKey: newID("fund")})
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CreateHold(ctx, financial.HoldRequest{TenantID: w.TenantID, WalletID: w.ID, Amount: "20.00", Currency: "ZMW", Reason: "external_payment", IdempotencyKey: newID("hold")})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&api{financial: s}).routes(mux)
	p := auth.Principal{Subject: "payment-rails", Environment: "sandbox", Scopes: map[string]struct{}{"wallet.hold": {}}, Roles: map[string]struct{}{"platform-tenant-delegator": {}}}
	handler := authenticate(fakeVerifier{principal: p}, false, mux)
	for _, tenant := range []string{"another-tenant", w.TenantID, w.TenantID} {
		r := httptest.NewRequest(http.MethodPost, "/v1/wallets/"+w.ID+"/holds/"+h.ID+"/release", nil)
		r.Header.Set("Authorization", "Bearer synthetic")
		r.Header.Set("X-Acting-Tenant-Id", tenant)
		r.Header.Set("X-Acting-Application-Id", "app_synthetic")
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, r)
		if tenant != w.TenantID {
			if out.Code != 404 {
				t.Fatalf("foreign tenant status=%d", out.Code)
			}
			continue
		}
		var got financial.Hold
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &got) != nil || got.ID != h.ID || got.Status != "released" {
			t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
		}
	}
}
