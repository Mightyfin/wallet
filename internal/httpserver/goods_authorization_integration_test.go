package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/auth"
	"github.com/Mightyfin/wallet-ledger/internal/database"
	"github.com/Mightyfin/wallet-ledger/internal/financial"
)

func TestGoodsAuthorizationHTTPPersistenceAndIsolation(t *testing.T) {
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
	entity, err := s.CreateLegalEntity(ctx, "Synthetic goods handoff", "ZM", "ZMW")
	if err != nil {
		t.Fatal(err)
	}
	tenant := newID("tenant")
	fac := newID("fac")
	account := newID("acc")
	var entityID, sourceID, offsetID string
	if err = db.QueryRow(ctx, `SELECT id::text FROM legal_entities WHERE public_id=$1`, entity.ID).Scan(&entityID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `INSERT INTO ledger_accounts(public_id,legal_entity_id,account_code,account_class,account_purpose,normal_side,currency,status) VALUES($1,$2,'goods-test-capital','asset','lender_liquidity','debit','ZMW','active') RETURNING id::text`, account, entityID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `INSERT INTO ledger_accounts(public_id,legal_entity_id,account_code,account_class,account_purpose,normal_side,currency,status) VALUES($1,$2,'goods-test-equity','equity','test_capital','credit','ZMW','active') RETURNING id::text`, newID("acc"), entityID).Scan(&offsetID); err != nil {
		t.Fatal(err)
	}
	// Synthetic posted capital, only in this disposable test database.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var journal string
	if err = tx.QueryRow(ctx, `INSERT INTO journal_transactions(public_id,legal_entity_id,tenant_id,transaction_type,posting_rule,status,currency,idempotency_key,request_hash,correlation_id,source_system,effective_at) VALUES($1,$2,$3,'test','test','posted','ZMW',$4,$5,$6,'test',now()) RETURNING id::text`, newID("txn"), entityID, tenant, newID("key"), "synthetic", newID("cor")).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO journal_entries(public_id,transaction_id,account_id,side,amount,currency) VALUES($1,$2,$3,'debit',70,'ZMW'),($4,$2,$5,'credit',70,'ZMW')`, newID("ent"), journal, sourceID, newID("ent"), offsetID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rsv, err := s.ReserveLenderLiquidity(ctx, financial.LiquidityRequest{RequestedBy: "synthetic", TenantID: tenant, LegalEntityID: entity.ID, AccountID: account, FacilityID: fac, AuthorizationID: "liq_" + fac, Amount: "70.00", Currency: "ZMW"})
	if err != nil {
		t.Fatal(err)
	}
	supplier, err := s.CreateWallet(ctx, financial.Wallet{TenantID: tenant, LegalEntityID: entity.ID, OwnerType: "organization", OwnerID: "synthetic-supplier", Currency: "ZMW"})
	if err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{Subject: "synthetic-facility-workload", Environment: "sandbox", Scopes: map[string]struct{}{"wallet.goods.authorize": {}, "wallet.goods.read": {}}, Roles: map[string]struct{}{"goods-credit-authorizer": {}, "platform-tenant-delegator": {}}}
	mux := http.NewServeMux()
	(&api{financial: s, environment: "sandbox"}).routes(mux)
	h := authenticate(fakeVerifier{principal: p}, false, mux)
	id := "gca_" + fac
	body := map[string]any{"authorization_id": id, "facility_id": fac, "legal_entity_id": entity.ID, "reservation_id": rsv.ID, "borrower_party_id": "synthetic-buyer", "supplier_party_id": supplier.OwnerID, "destination_wallet_id": supplier.ID, "order_reference": "synthetic-order", "amount": "70.00", "currency": "ZMW", "allow_partial_use": true, "expires_at": time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)}
	request := func(method, path, actingTenant string, payload map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(payload)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer synthetic")
		r.Header.Set("X-Acting-Tenant-Id", actingTenant)
		r.Header.Set("X-Acting-Application-Id", "synthetic-app")
		r.Header.Set("Idempotency-Key", id)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/v1/internal/goods-credit/authorizations"
	if w := request("POST", path, "other-tenant", body); w.Code != 404 {
		t.Fatal("cross tenant", w.Code, w.Body.String())
	}
	for range 2 {
		w := request("POST", path, tenant, body)
		var reply struct {
			Authorization financial.GoodsAuthorizationRecord `json:"authorization"`
			Environment   string                             `json:"environment"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.Authorization.RegisteredBy != p.Subject || reply.Authorization.FacilityID != fac || reply.Authorization.DestinationWalletID != supplier.ID || reply.Environment != "sandbox" {
			t.Fatal(w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable financial authority")
		}
	}
	body["order_reference"] = "substituted"
	if w := request("POST", path, tenant, body); w.Code != 409 {
		t.Fatal("changed retry", w.Code, w.Body.String())
	}
	body["order_reference"] = "synthetic-order"
	body["authorized_by"] = "forged-admin"
	if w := request("POST", path, tenant, body); w.Code != 400 {
		t.Fatal("actor spoof", w.Code, w.Body.String())
	}
	delete(body, "authorized_by")
	for _, scope := range []string{tenant, "other-tenant"} {
		w := request("GET", path+"/"+id+"?legal_entity_id="+entity.ID, scope, nil)
		want := 200
		if scope != tenant {
			want = 404
		}
		if w.Code != want {
			t.Fatal("read scope", w.Code, w.Body.String())
		}
	}
	var events, draws int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='goods.credit.authorized' AND aggregate_id=$1`, id).Scan(&events); err != nil || events != 1 {
		t.Fatal("registration not exactly once", events, err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM goods_credit_uses WHERE authorization_id=$1`, id).Scan(&draws); err != nil || draws != 0 {
		t.Fatal("registration created debt", draws, err)
	}
	balance, err := s.GetBalance(ctx, entity.ID, tenant, supplier.ID)
	if err != nil || !balance.Ledger.IsZero() {
		t.Fatal("registration funded supplier", balance, err)
	}
}
