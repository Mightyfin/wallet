package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mightyfin/wallet-ledger/internal/outbox"
	"github.com/nats-io/nats.go"
)

const streamName = "WALLET_LEDGER_EVENTS"

type Publisher struct{ stream nats.JetStreamContext }

func NewPublisher(url, token string) (*Publisher, func(), error) {
	options := []nats.Option{nats.Name("wallet-ledger-outbox-publisher")}
	if token != "" {
		options = append(options, nats.Token(token))
	}
	connection, err := nats.Connect(url, options...)
	if err != nil {
		return nil, nil, err
	}
	stream, err := connection.JetStream()
	if err != nil {
		connection.Close()
		return nil, nil, err
	}
	return &Publisher{stream: stream}, connection.Close, nil
}

func (p *Publisher) EnsureStream(ctx context.Context) error {
	if _, err := p.stream.StreamInfo(streamName, nats.Context(ctx)); err == nil {
		return nil
	} else if err != nats.ErrStreamNotFound {
		return err
	}
	_, err := p.stream.AddStream(&nats.StreamConfig{Name: streamName, Subjects: []string{"mightyfin.wallet.>"}}, nats.Context(ctx))
	return err
}

func (p *Publisher) Publish(ctx context.Context, event outbox.Event) error {
	if event.TenantID == "" {
		return fmt.Errorf("wallet event has no tenant")
	}
	envelope, err := json.Marshal(map[string]any{"id": event.ID, "type": event.Type, "version": "1", "tenant_id": event.TenantID, "aggregate_id": event.AggregateID, "occurred_at": event.OccurredAt.UTC(), "data": event.Payload})
	if err != nil {
		return err
	}
	suffix := strings.TrimPrefix(event.Type, "wallet.")
	if suffix == "" {
		return fmt.Errorf("invalid wallet event type")
	}
	_, err = p.stream.Publish("mightyfin.wallet."+suffix, envelope, nats.MsgId(event.ID), nats.Context(ctx))
	return err
}
