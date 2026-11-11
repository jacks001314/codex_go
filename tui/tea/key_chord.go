package tea

import (
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

// keyChordRevealDelay mirrors Rust #60637: a pending prefix reveals its guide
// only after fast two-stroke shortcuts had time to complete.
const keyChordRevealDelay = 350 * time.Millisecond

// KeyChordRevealMsg re-renders after the reveal delay. The pending state itself
// is the source of truth, so the message carries no payload.
type KeyChordRevealMsg struct{}

// KeyChordTimeoutMsg expires the pending prefix that scheduled it. Stale ticks
// from an older prefix are ignored by comparing the recorded start time.
type KeyChordTimeoutMsg struct {
	Started time.Time
}

func (m *Model) clearKeyChord() {
	if m == nil {
		return
	}
	m.pendingKeyChordPrefix = ""
	m.pendingKeyChordStarted = time.Time{}
	m.activeKeyChord = ""
}

// handleKeyChordKey consumes the first stroke of an explicitly configured
// two-stroke chord, then carries the completed chord through the normal action
// dispatch so keyMatches can resolve it without action-specific rewrites.
func (m *Model) handleKeyChordKey(keySpec string, now time.Time) (bool, bubbletea.Cmd) {
	if m == nil || keySpec == "" {
		return false, nil
	}
	if prefix := strings.TrimSpace(m.pendingKeyChordPrefix); prefix != "" {
		started := m.pendingKeyChordStarted
		m.pendingKeyChordPrefix = ""
		m.pendingKeyChordStarted = time.Time{}
		if now.Sub(started) > codextui.LeaderChordTimeout {
			return false, nil
		}
		if keySpec == "esc" {
			m.notice = ""
			return true, nil
		}
		combined := prefix + " " + keySpec
		if m.keyChordConfigured(combined) {
			m.activeKeyChord = combined
			m.notice = ""
			return false, nil
		}
		return false, nil
	}
	if m.keyChordPrefixConfigured(keySpec) {
		m.pendingKeyChordPrefix = keySpec
		m.pendingKeyChordStarted = now
		if strings.Contains(m.notice, "second key of the shortcut chord") {
			m.notice = ""
		}
		return true, bubbletea.Batch(
			bubbletea.Tick(keyChordRevealDelay, func(time.Time) bubbletea.Msg { return KeyChordRevealMsg{} }),
			bubbletea.Tick(codextui.LeaderChordTimeout, func(time.Time) bubbletea.Msg {
				return KeyChordTimeoutMsg{Started: now}
			}),
		)
	}
	return false, nil
}

func (m *Model) keyChordPrefixConfigured(prefix string) bool {
	if m == nil {
		return false
	}
	for _, descriptor := range codextui.KeymapActions(codextui.KeymapActionFilter{FastModeEnabled: m.featureSettings["fast_mode"]}) {
		bindings, _, custom := codextui.ResolvedKeymapBindings(m.keymapConfig, descriptor.Context, descriptor.Action)
		if !custom {
			continue
		}
		for _, binding := range bindings {
			strokes := strings.Fields(binding)
			if len(strokes) == 2 && strokes[0] == prefix && strokes[0] != "leader" {
				return true
			}
		}
	}
	return false
}

func (m *Model) keyChordConfigured(spec string) bool {
	if m == nil {
		return false
	}
	for _, descriptor := range codextui.KeymapActions(codextui.KeymapActionFilter{FastModeEnabled: m.featureSettings["fast_mode"]}) {
		bindings, _, custom := codextui.ResolvedKeymapBindings(m.keymapConfig, descriptor.Context, descriptor.Action)
		if !custom {
			continue
		}
		for _, binding := range bindings {
			if binding == spec {
				return true
			}
		}
	}
	return false
}
