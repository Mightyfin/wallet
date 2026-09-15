package eventbus

import (
	"fmt"
	"strings"

	"github.com/nats-io/nats.go"
)

// NewPublisherWithCredentials supports a subject-restricted broker identity.
// No fallback to a shared token is permitted when this mode is selected.
func NewPublisherWithCredentials(url, user, password, environment string) (*Publisher, func(), error) {
	if !validEnvironment(environment) || strings.TrimSpace(user) == "" || password == "" {
		return nil, nil, fmt.Errorf("explicit environment and publisher credentials required")
	}
	connection, err := nats.Connect(url, nats.Name("wallet-ledger-outbox-publisher"), nats.UserInfo(user, password))
	if err != nil {
		return nil, nil, fmt.Errorf("authenticated event connection failed")
	}
	stream, err := connection.JetStream()
	if err != nil {
		connection.Close()
		return nil, nil, fmt.Errorf("authenticated event stream unavailable")
	}
	return &Publisher{stream: stream, environment: environment}, connection.Close, nil
}
