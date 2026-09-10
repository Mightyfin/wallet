package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/financial"
)

// Uses the other repository's real adapter without importing its code into
// Wallet or adding a production dependency between the two Go modules.
func testConnectedFacilityAdapter(t *testing.T, handler http.Handler, a financial.GoodsAuthorizationRecord) {
	t.Helper()
	root, goBin := os.Getenv("GOODS_CONNECTED_FACILITY_ROOT"), os.Getenv("GOODS_CONNECTED_GO")
	if root == "" || goBin == "" {
		t.Log("connected Facility adapter not requested")
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			id, secret, ok := r.BasicAuth()
			r.ParseForm()
			if !ok || id != "synthetic-executor" || secret != "synthetic-only" || (r.Form.Get("scope") != "wallet.goods.use" && r.Form.Get("scope") != "wallet.goods.read") {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "synthetic"})
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	instruction := map[string]any{"authorization": map[string]any{"authorization_id": a.ID, "tenant_id": a.TenantID, "legal_entity_id": a.LegalEntityID, "facility_id": a.FacilityID, "reservation_id": a.ReservationID, "borrower_party_id": a.BorrowerPartyID, "supplier_party_id": a.SupplierPartyID, "destination_wallet_id": a.DestinationWalletID, "order_reference": a.OrderReference, "amount_minor": 7000, "currency": a.Currency, "allow_partial_use": true, "expires_at": a.ExpiresAt}, "use_id": "connected-use", "amount_minor": 1000, "attempts": 1, "status": "working"}
	raw, err := json.Marshal(instruction)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "test", "-count=1", "-run", "^TestGoodsUseConnectedWallet$", "./eventbus")
	cmd.Dir = root
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOODS_CONNECTED_TEST_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GOODS_CONNECTED_TEST_URL="+server.URL, "GOODS_CONNECTED_TEST_INSTRUCTION="+string(raw))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("connected Facility adapter: %v\n%s", err, output)
	}
	t.Log("connected Facility adapter passed with a real Wallet commit and lost-response recovery")
}
