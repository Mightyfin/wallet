package eventbus

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Mightyfin/wallet-ledger/internal/outbox"
	"github.com/nats-io/nats.go"
)

func TestRestrictedPublisherStoresOneConfirmedPayment(t *testing.T) {
	url := os.Getenv("EVENTBUS_ACL_TEST_URL")
	if url == "" {
		t.Skip("isolated permission-test broker required")
	}
	nc, err := nats.Connect(url, nats.UserInfo("bootstrap", os.Getenv("EVENTBUS_ACL_TEST_BOOTSTRAP_PASSWORD")))
	if err != nil {
		t.Fatal("bootstrap connection failed")
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = js.StreamInfo(streamName); err != nats.ErrStreamNotFound {
		t.Fatal("test requires absent stream")
	}
	if _, err = js.AddStream(&nats.StreamConfig{Name: streamName, Subjects: []string{"mightyfin.wallet.>"}, Storage: nats.MemoryStorage}); err != nil {
		t.Fatal(err)
	}
	defer js.DeleteStream(streamName)
	p, close, err := NewPublisherWithCredentials(url, "wallet-publisher", os.Getenv("EVENTBUS_ACL_TEST_WALLET_PASSWORD"), "sandbox")
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = p.EnsureStream(ctx); err != nil {
		t.Fatal("restricted publisher cannot read preprovisioned stream", err)
	}
	event := outbox.Event{ID: "synthetic-confirmed-payment", Type: "loan.external_disbursement.posted", TenantID: "synthetic-tenant", AggregateID: "synthetic-journal", Payload: json.RawMessage(`{"method":"manual_bank"}`), OccurredAt: time.Now().UTC()}
	for i := 0; i < 2; i++ {
		if err = p.Publish(ctx, event); err != nil {
			t.Fatal("authorized durable publish failed", err)
		}
	}
	info, err := js.StreamInfo(streamName)
	if err != nil || info.State.Msgs != 1 {
		t.Fatal("duplicate durable event", err)
	}
	msg, err := js.GetMsg(streamName, 1)
	if err != nil || msg.Subject != "mightyfin.wallet.loan.external_disbursement.posted" {
		t.Fatal("incorrect event subject", err)
	}
	var body struct {
		TenantID    string `json:"tenant_id"`
		Environment string `json:"environment"`
	}
	if json.Unmarshal(msg.Data, &body) != nil || body.TenantID != "synthetic-tenant" || body.Environment != "sandbox" {
		t.Fatal("event scope lost")
	}
}
