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
	id := fmt.Sprintf("evt_outbox_test_%d", time.Now().UnixNano())
	_, err = pool.Exec(ctx, `INSERT INTO outbox_events(public_id,event_type,aggregate_type,aggregate_id,payload) VALUES($1,'test.event','test','test-1','{}')`, id)
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
