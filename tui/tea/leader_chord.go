package tea

import (
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// handleLeaderChordKey implements the minimal persistent leader chord used by
// `chat.open_presets` (default `leader p`, Rust #54381). A pending prefix
// consumes its completion key; another key or the timeout cancels the chord and
// is dispatched normally.
func (m *Model) handleLeaderChordKey(keySpec string) (bool, bubbletea.Cmd) {
	if m == nil || keySpec == "" {
		return false, nil
	}
	now := m.currentTime()
	if m.leaderChordPending {
		completion := m.leaderChordCompletion
		startedAt := m.leaderChordStartedAt
		m.clearLeaderChord()
		if now.Sub(startedAt) > codextui.LeaderChordTimeout {
			return false, nil
		}
		if keySpec == completion {
			return true, m.openQuickDialMenu()
		}
		return false, nil
	}
	completion, prefixes, ok := m.presetsLeaderBinding()
	if !ok {
		return false, nil
	}
	for _, prefix := range prefixes {
		if keySpec == prefix {
			m.leaderChordPending = true
			m.leaderChordStartedAt = now
			m.leaderChordCompletion = completion
			return true, nil
		}
	}
	return false, nil
}

func (m *Model) presetsLeaderBinding() (string, []string, bool) {
	if m == nil {
		return "", nil, false
	}
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "chat", "open_presets")
	for _, binding := range bindings {
		if completion, ok := codextui.LeaderSpecCompletion(binding); ok {
			prefixes, err := codextui.LeaderKeybindings(m.keymapConfig)
			if err != nil || len(prefixes) == 0 {
				return "", nil, false
			}
			return completion, prefixes, true
		}
	}
	return "", nil, false
}

func (m *Model) clearLeaderChord() {
	if m == nil {
		return
	}
	m.leaderChordPending = false
	m.leaderChordStartedAt = time.Time{}
	m.leaderChordCompletion = ""
}
