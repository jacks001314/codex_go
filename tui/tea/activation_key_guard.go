package tea

// Rust parity: codex-rs/tui/src/activation_key_guard.rs. A held activation key
// must not trigger the next view's action, so the key that caused a view
// transition is swallowed until its release, until a different key is pressed,
// or until a bounded window lapses. Bubble Tea v2 reports auto-repeat as fresh
// presses, so Go always uses Rust's legacy fallback window.

import (
	"time"

	"codex_go/tui/keymsg"
)

const (
	activationKeyInitialGuard = 1 * time.Second
	activationKeyRepeatGuard  = 1 * time.Second
)

type activationKeyGuard struct {
	key        keymsg.KeyMsg
	blockUntil time.Time
	active     bool
}

// newActivationKeyGuard mirrors ActivationKeyGuard::new_at with the legacy
// repeat fallback enabled, the path Rust uses on terminals without enhanced
// keyboard reporting.
func newActivationKeyGuard(key keymsg.KeyMsg, now time.Time) *activationKeyGuard {
	return &activationKeyGuard{
		key:        key,
		blockUntil: now.Add(activationKeyInitialGuard),
		active:     true,
	}
}

// consume mirrors ActivationKeyGuard::consume_at. It reports whether the event
// belongs to the held activation key and must be swallowed.
func (g *activationKeyGuard) consume(key keymsg.KeyMsg, released bool, now time.Time) bool {
	if g == nil || !g.active {
		return false
	}
	if samePhysicalActivationKey(g.key, key) {
		if released {
			g.active = false
			return true
		}
		if !now.After(g.blockUntil) {
			if repeatUntil := now.Add(activationKeyRepeatGuard); repeatUntil.After(g.blockUntil) {
				g.blockUntil = repeatUntil
			}
			return true
		}
		g.active = false
		return false
	}
	if !released {
		g.active = false
	}
	return false
}

// rearm extends the guard when replay work outlasts the initial press window
// (Rust #63146 ActivationKeyGuard::rearm): a held selection key must not act on
// an approval dialog the destination opens while its transcript replays.
func (g *activationKeyGuard) rearm(now time.Time) {
	if g == nil || !g.active {
		return
	}
	if until := now.Add(activationKeyInitialGuard); until.After(g.blockUntil) {
		g.blockUntil = until
	}
}

func (g *activationKeyGuard) isActive() bool {
	return g != nil && g.active
}

// samePhysicalActivationKey mirrors KeyBinding::is_same_physical_key: modifiers
// do not matter, and Shift+Tab counts as Tab (Bubble Tea already reports both
// with the Tab code).
func samePhysicalActivationKey(a keymsg.KeyMsg, b keymsg.KeyMsg) bool {
	return activationPhysicalCode(a) == activationPhysicalCode(b)
}

func activationPhysicalCode(key keymsg.KeyMsg) rune {
	if key.Code != 0 {
		return key.Code
	}
	if key.Type == keymsg.KeyRunes && len(key.Runes) > 0 {
		return key.Runes[0]
	}
	return 0
}

// suppressCurrentViewKeyUntilRelease mirrors
// ChatWidget::suppress_current_view_key_until_release. Only transitions caused by
// the key currently being handled arm the guard, so a click hours later cannot
// resurrect an old key.
// rearmActivationKeyGuard extends an armed view-transition guard so the switch
// into a replayed destination keeps swallowing the key that started it.
func (m *Model) rearmActivationKeyGuard() {
	if m == nil || m.activationKeyGuard == nil {
		return
	}
	m.activationKeyGuard.rearm(m.currentTime())
}

func (m *Model) suppressCurrentViewKeyUntilRelease() {
	if m == nil || !m.keyUpdate || !m.activationKeyAvailable {
		return
	}
	m.activationKeyGuard = newActivationKeyGuard(m.activationKey, m.activationKeyAt)
}
