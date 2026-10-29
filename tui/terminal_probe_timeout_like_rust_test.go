package tui

import (
	"testing"
	"time"
)

// Rust 7c1e485c871d (#46855) raised `terminal_probe::DEFAULT_TIMEOUT` from
// 100 ms to 250 ms and uses it for every terminal probe and startup probe
// group, including the OSC 10/11 default-color query. Go keeps one constant per
// seam, so both must track the shared budget.
func TestTerminalProbeTimeoutMatchesRustDefault(t *testing.T) {
	if DefaultTerminalProbeTimeout != 250*time.Millisecond {
		t.Fatalf("DefaultTerminalProbeTimeout = %s, want 250ms (Rust DEFAULT_TIMEOUT)", DefaultTerminalProbeTimeout)
	}
	if terminalColorProbeTimeout != DefaultTerminalProbeTimeout {
		t.Fatalf("terminalColorProbeTimeout = %s, want %s to share the Rust DEFAULT_TIMEOUT budget",
			terminalColorProbeTimeout, DefaultTerminalProbeTimeout)
	}
}

// The color probe falls back to the shared budget when a caller passes no
// explicit timeout, so the raised constant is what a real query uses.
func TestTerminalColorProbeUsesDefaultBudgetWhenUnset(t *testing.T) {
	if got := defaultTerminalColorProbeTimeout(0); got != 250*time.Millisecond {
		t.Fatalf("defaultTerminalColorProbeTimeout(0) = %s, want 250ms", got)
	}
	if got := defaultTerminalColorProbeTimeout(750 * time.Millisecond); got != 750*time.Millisecond {
		t.Fatalf("defaultTerminalColorProbeTimeout(750ms) = %s, want the caller's budget", got)
	}
}
