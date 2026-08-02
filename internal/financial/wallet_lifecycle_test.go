package financial

import "testing"

func TestWalletLifecycleTransitionsFailClosed(t *testing.T) {
	tests := []struct {
		from, to string
		allowed  bool
	}{
		{"pending", "active", true},
		{"pending", "closed", true},
		{"pending", "suspended", false},
		{"active", "restricted", true},
		{"active", "suspended", true},
		{"restricted", "active", true},
		{"suspended", "restricted", true},
		{"closed", "active", false},
		{"active", "active", false},
		{"unknown", "active", false},
	}
	for _, test := range tests {
		if got := validWalletTransition(test.from, test.to); got != test.allowed {
			t.Errorf("transition %s -> %s: got %v", test.from, test.to, got)
		}
	}
}
