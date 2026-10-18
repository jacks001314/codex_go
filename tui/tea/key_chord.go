package tea

import (
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

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
		m.notice = "Waiting for the second key of the shortcut chord."
		return true, nil
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
