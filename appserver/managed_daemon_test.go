package appserver

import "testing"

// TestManagedDaemonNoFileTargetLikeRust mirrors Rust #51470's resolution:
// raise to 4096, cap at the inherited hard limit, and keep a higher soft limit.
func TestManagedDaemonNoFileTargetLikeRust(t *testing.T) {
	cases := []struct {
		name         string
		current, max uint64
		want         uint64
	}{
		{name: "raise", current: 1024, max: 1 << 20, want: 4096},
		{name: "preserve higher soft", current: 8192, max: 1 << 20, want: 8192},
		{name: "cap at hard", current: 1024, max: 2048, want: 2048},
		{name: "already at target", current: 4096, max: 4096, want: 4096},
		{name: "unlimited hard", current: 256, max: ^uint64(0), want: 4096},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedDaemonNoFileTarget(tc.current, tc.max); got != tc.want {
				t.Fatalf("target(%d, %d) = %d, want %d", tc.current, tc.max, got, tc.want)
			}
		})
	}
}
