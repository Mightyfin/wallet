package outbox

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/database"
)

func TestClaimAndPublish(t *testing.T) {
	databaseURL := os.Getenv("WALLET_LEDGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("WALLET_LEDGER_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entityID := "le_outbox_test_" + suffix
	walletID := "wal_outbox_test_" + suffix
	id := "evt_outbox_test_" + suffix
	var entityUUID string
	err = pool.QueryRow(ctx, `INSERT INTO legal_entities(public_id,name,country_code,base_currency,status)
		VALUES($1,'Outbox integration test','ZM','ZMW','active') RETURNING id::text`, entityID).Scan(&entityUUID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO wallets(public_id,legal_entity_id,tenant_id,owner_type,owner_id,currency,status)
		VALUES($1,$2::uuid,'tenant-outbox-test','system',$3,'ZMW','active')`, walletID, entityUUID, "owner-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload,occurred_at)
		VALUES($1,'test.event','wallet',$2,'{}','2000-01-01T00:00:00Z')`, id, walletID)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool)
	events, err := store.Claim(ctx, 500, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.ID == id {
			found = true
			if err = store.MarkPublished(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("new outbox event was not claimed")
	}
	var published bool
	if err = pool.QueryRow(ctx, `SELECT published_at IS NOT NULL FROM outbox_events WHERE public_id=$1`, id).Scan(&published); err != nil || !published {
		t.Fatalf("published=%v err=%v", published, err)
	}
}
