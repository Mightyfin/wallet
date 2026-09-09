package eventbus

import (
	"context"
	"encoding/json"
	"github.com/Mightyfin/wallet-ledger/internal/outbox"
	"github.com/nats-io/nats.go"
	"os"
	"testing"
	"time"
)

// Only point EVENTBUS_TEST_NATS_URL at a disposable dedicated test broker.
func TestPublisherEnvironmentAndBrokerDeduplication(t *testing.T) {
	url := os.Getenv("EVENTBUS_TEST_NATS_URL")
	if url == "" {
		t.Skip("dedicated disposable NATS required")
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	// Refuse a pre-existing stream rather than modifying an operational broker.
	if _, err = js.StreamInfo(streamName); err != nats.ErrStreamNotFound {
		t.Fatal("test requires absent stream", err)
	}
	if _, err = js.AddStream(&nats.StreamConfig{Name: streamName, Subjects: []string{"mightyfin.wallet.>"}, Storage: nats.MemoryStorage}); err != nil {
		t.Fatal(err)
	}
	defer js.DeleteStream(streamName)
	sandbox, closeSandbox, err := NewPublisher(url, "", "sandbox")
	if err != nil {
		t.Fatal(err)
	}
	defer closeSandbox()
	live, closeLive, err := NewPublisher(url, "", "production")
	if err != nil {
		t.Fatal(err)
	}
	defer closeLive()
	ctx := context.Background()
	if err = sandbox.EnsureStream(ctx); err != nil {
		t.Fatal(err)
	}
	event := outbox.Event{ID: "evt_test", Type: "ledger.transaction.reversed", TenantID: "ten_test", AggregateID: "txn_test", Payload: json.RawMessage(`{}`), OccurredAt: time.Now()}
	if err = sandbox.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err = sandbox.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err = live.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	info, err := js.StreamInfo(streamName)
	if err != nil || info.State.Msgs != 2 {
		t.Fatal("environment collision or duplicate", info, err)
	}
	for i, want := range []string{"sandbox", "production"} {
		msg, err := js.GetMsg(streamName, uint64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Environment string `json:"environment"`
		}
		if json.Unmarshal(msg.Data, &body) != nil || body.Environment != want {
			t.Fatal("missing environment", body)
		}
	}
}
