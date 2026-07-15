package tunnel

import "testing"

func TestBlockedForwardTarget(t *testing.T) {
	cases := []struct {
		target  string
		blocked bool
	}{
		{"127.0.0.1:22", false},      // loopback: legitimate (foreign server's own service)
		{"10.0.0.5:80", false},       // private: allowed by default
		{"1.1.1.1:53", false},        // public: allowed
		{"example.com:443", false},   // hostname: resolved at dial time
		{"169.254.169.254:80", true}, // link-local / cloud metadata: blocked
		{"0.0.0.0:80", true},         // unspecified: blocked
		{"224.0.0.1:80", true},       // multicast: blocked
		{"not-a-host-port", true},    // malformed: blocked
	}
	for _, tc := range cases {
		if got := blockedForwardTarget(tc.target); got != tc.blocked {
			t.Errorf("blockedForwardTarget(%q) = %v, want %v", tc.target, got, tc.blocked)
		}
	}
}
