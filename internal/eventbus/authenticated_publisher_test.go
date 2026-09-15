package eventbus

import "testing"

func TestPublisherCredentialsFailClosed(t *testing.T) {
	for _, tc := range []struct{ user, password, environment string }{
		{"", "password", "sandbox"}, {"wallet-publisher", "", "sandbox"}, {"wallet-publisher", "password", ""},
	} {
		p, close, err := NewPublisherWithCredentials("nats://127.0.0.1:1", tc.user, tc.password, tc.environment)
		if err == nil || p != nil || close != nil {
			t.Fatal("invalid credentials accepted")
		}
	}
}
