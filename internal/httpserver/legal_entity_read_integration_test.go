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

func TestLegalEntityReadStoredRegistry(t *testing.T) {
	dsn := os.Getenv("WALLET_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable database required")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := financial.New(db)
	entity, err := s.CreateLegalEntity(ctx, "Synthetic registry test", "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	a := &api{financial: s, environment: "sandbox"}
	mux := http.NewServeMux()
	a.routes(mux)
	p := auth.Principal{Subject: "registry-reader", Environment: "sandbox", Roles: map[string]struct{}{"platform-tenant-delegator": {}, "legal-entity-reader": {}}, Scopes: map[string]struct{}{"wallet.legal_entity.read": {}}}
	for _, status := range []string{"active", "suspended", "closed"} {
		if _, err := db.Exec(ctx, `UPDATE legal_entities SET status=$2 WHERE public_id=$1`, entity.ID, status); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "/v1/internal/legal-entities/"+entity.ID, nil)
		r.Header.Set("Authorization", "Bearer test")
		w := httptest.NewRecorder()
		authenticate(fakeVerifier{principal: p}, false, mux).ServeHTTP(w, r)
		var out map[string]string
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out) != 4 || out["status"] != status || out["id"] != entity.ID || out["environment"] != "sandbox" || out["base_currency"] != "ZMW" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/v1/internal/legal-entities/nonexistent", nil)
	r.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	authenticate(fakeVerifier{principal: p}, false, mux).ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}
