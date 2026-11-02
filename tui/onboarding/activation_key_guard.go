package onboarding

import (
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// Rust parity: codex-rs/tui/src/activation_key_guard.rs (Rust #58427). A key
// held across a view transition must not activate the view that replaces it:
// holding Enter on "Sign in another way" used to select the first alternative
// and then submit it.
//
// Bubble Tea does not report key releases, so the guard always takes Rust's
// legacy path: the same physical key stays blocked for a bounded window and
// every further press extends it. Any other key ends the guard.
const (
	activationGuardWindow       = time.Second
	activationGuardRepeatWindow = time.Second
)

type activationKeyGuard struct {
	key        string
	blockUntil time.Time
	active     bool
	now        func() time.Time
}

func newActivationKeyGuard(key string, now func() time.Time) *activationKeyGuard {
	if now == nil {
		now = time.Now
	}
	return &activationKeyGuard{
		key:        key,
		blockUntil: now().Add(activationGuardWindow),
		active:     true,
		now:        now,
	}
}

// consume reports whether this key press must be dropped.
func (g *activationKeyGuard) consume(message bubbletea.KeyMsg) bool {
	if g == nil || !g.active {
		return false
	}
	if keyIdentity(message) != g.key {
		g.active = false
		return false
	}
	now := g.now()
	if now.After(g.blockUntil) {
		g.active = false
		return false
	}
	g.blockUntil = now.Add(activationGuardRepeatWindow)
	return true
}

// keyIdentity names the physical key a Bubble Tea message came from. The guard
// only compares a key with itself, so the printable form is enough.
func keyIdentity(message bubbletea.KeyMsg) string {
	return message.String()
}
