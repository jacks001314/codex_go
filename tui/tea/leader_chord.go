package tea

import (
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust #54381/#57223: persistent leader chords for `chat.open_presets`
// (default `leader p`) and the implicit `global.select_transcript`
// (default `leader [`). Several shortcuts share one prefix, so a pending prefix
// remembers every completion it can dispatch. Another key or the timeout
// cancels the chord and is dispatched normally.
func (m *Model) handleLeaderChordKey(keySpec string, allow func(action string) bool) (bool, bubbletea.Cmd) {
	if m == nil || keySpec == "" {
		return false, nil
	}
	now := m.currentTime()
	if m.leaderChordPending {
		actions := m.leaderChordActions
		startedAt := m.leaderChordStartedAt
		m.clearLeaderChord()
		if now.Sub(startedAt) > codextui.LeaderChordTimeout {
			return false, nil
		}
		action, ok := actions[keySpec]
		if !ok || (allow != nil && !allow(action)) {
			return false, nil
		}
		return true, m.dispatchLeaderAction(action)
	}
	actions, prefixes, ok := m.leaderBindingsFor(keySpec, allow)
	if !ok {
		return false, nil
	}
	for _, prefix := range prefixes {
		if keySpec == prefix {
			m.leaderChordPending = true
			m.leaderChordStartedAt = now
			m.leaderChordActions = actions
			return true, nil
		}
	}
	return false, nil
}

// selectTranscriptOnly restricts overlay chords to the selection shortcut.
func selectTranscriptOnly(action string) bool {
	return action == "global.select_transcript"
}

// leaderShortcuts lists the actions with implicit leader defaults in the same
// order Rust installs them (leader_defaults.rs).
var leaderShortcuts = []struct {
	context  string
	action   string
	implicit bool
}{
	{"global", "select_transcript", true},
	{"chat", "open_presets", true},
	{"global", "focus_main_composer", true},
	{"global", "focus_sidebar_content", true},
	{"global", "focus_sidebar_tabs", true},
	{"global", "hide_pane", true},
	{"global", "focus_pane_left", true},
	{"global", "focus_pane_right", true},
	{"global", "toggle_pane_zoom", true},
	{"global", "minimize_pane_width", true},
	{"global", "maximize_pane_width", true},
	{"global", "equalize_pane_widths", true},
	{"global", "resize_panes", true},
	{"global", "move_divider_left", true},
	{"global", "move_divider_right", true},
}

// implicitWorkspaceLeaderCompletions are the free leader chords Rust installs
// only when no explicit binding claims them (keymap/chords/leader_defaults.rs,
// #59130/#59208/#59287). Keeping them out of the catalog defaults lets a user
// assign the same chord to another action without a false conflict.
var implicitWorkspaceLeaderCompletions = map[string][]string{
	"global.focus_main_composer":   {"c"},
	"global.focus_sidebar_content": {"s"},
	"global.focus_sidebar_tabs":    {"t"},
	"global.hide_pane":             {"-"},
	"global.focus_pane_left":       {"left", "h"},
	"global.focus_pane_right":      {"right", "l"},
	// Rust #59595 pane layout shortcuts.
	"global.toggle_pane_zoom":     {"z"},
	"global.minimize_pane_width":  {","},
	"global.maximize_pane_width":  {"."},
	"global.equalize_pane_widths": {"="},
	"global.resize_panes":         {"r"},
	"global.move_divider_left":    {"shift-h"},
	"global.move_divider_right":   {"shift-l"},
}

// leaderBindingsFor collects every leader completion reachable from keySpec.
func (m *Model) leaderBindingsFor(keySpec string, allow func(action string) bool) (map[string]string, []string, bool) {
	if m == nil {
		return nil, nil, false
	}
	prefixes, err := codextui.LeaderKeybindings(m.keymapConfig)
	if err != nil || len(prefixes) == 0 {
		return nil, nil, false
	}
	isPrefix := false
	for _, prefix := range prefixes {
		if keySpec == prefix {
			isPrefix = true
			break
		}
	}
	if !isPrefix {
		return nil, nil, false
	}
	actions := map[string]string{}
	for _, shortcut := range leaderShortcuts {
		bindings, _, custom := codextui.ResolvedKeymapBindings(m.keymapConfig, shortcut.context, shortcut.action)
		action := shortcut.context + "." + shortcut.action
		if allow != nil && !allow(action) {
			continue
		}
		if shortcut.implicit && !custom {
			if completions, ok := implicitWorkspaceLeaderCompletions[action]; ok {
				if !m.leaderPrefixClaimed(keySpec, shortcut.context, shortcut.action) {
					for _, completion := range completions {
						actions[completion] = action
					}
				}
				continue
			}
		}
		for _, binding := range bindings {
			completion, ok := codextui.LeaderSpecCompletion(binding)
			if !ok {
				continue
			}
			if shortcut.implicit && !custom && m.leaderPrefixClaimed(keySpec, shortcut.context, shortcut.action) {
				continue
			}
			actions[completion] = action
		}
	}
	if len(actions) == 0 {
		return nil, nil, false
	}
	return actions, prefixes, true
}

// leaderPrefixClaimed mirrors leader_defaults.rs: an implicit leader shortcut
// yields to any action that already binds the prefix as a single key.
func (m *Model) leaderPrefixClaimed(prefix string, context string, action string) bool {
	for _, descriptor := range codextui.KeymapConfigActions(codextui.KeymapActionFilter{FastModeEnabled: true}) {
		if descriptor.Context == context && descriptor.Action == action {
			continue
		}
		if descriptor.Context == "global" && descriptor.Action == "leader" {
			continue
		}
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, descriptor.Context, descriptor.Action)
		for _, binding := range bindings {
			if normalized, err := codextui.NormalizeKeybindingSpec(binding); err == nil && normalized == prefix {
				return true
			}
		}
	}
	return false
}

func (m *Model) dispatchLeaderAction(action string) bubbletea.Cmd {
	switch action {
	case "global.select_transcript":
		return m.beginTranscriptSelection()
	case "chat.open_presets":
		return m.openQuickDialMenu()
	case "global.focus_main_composer":
		return m.focusMainComposerExplicit()
	case "global.focus_pane_left":
		return m.focusMainComposer()
	case "global.focus_sidebar_content", "global.focus_pane_right":
		return m.focusSidebarContent()
	case "global.focus_sidebar_tabs":
		return m.openSidebarChooser()
	case "global.hide_pane":
		return m.hideFocusedSidePane()
	case "global.toggle_pane_zoom":
		return m.togglePaneZoom()
	case "global.minimize_pane_width":
		return m.setFocusedPaneWidth(0)
	case "global.maximize_pane_width":
		return m.setFocusedPaneWidth(100)
	case "global.equalize_pane_widths":
		return m.setFocusedPaneWidth(50)
	case "global.resize_panes":
		return m.beginSidePaneResize()
	case "global.move_divider_left":
		return m.nudgeSidePaneWidth(2)
	case "global.move_divider_right":
		return m.nudgeSidePaneWidth(-2)
	default:
		return nil
	}
}

func (m *Model) clearLeaderChord() {
	if m == nil {
		return
	}
	m.leaderChordPending = false
	m.leaderChordStartedAt = time.Time{}
	m.leaderChordCompletion = ""
	m.leaderChordActions = nil
	m.leaderChordAction = ""
}
