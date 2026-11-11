package tea

import (
	"sort"
	"strings"

	codextui "codex_go/tui"
)

// rustKeyChordHUD mirrors Rust #60637's shared floating guide: after a short
// delay a pending chord reveals the actions that complete it, plus the Esc
// cancellation hint. It replaces the older footer notice and is shared by the
// conversation, transcript overlay, and picker surfaces.
const (
	keyChordHUDCancel = "Esc cancel"
	keyChordHUDMore   = "… more shortcuts"
)

type keyChordHUDCompletion struct {
	Completion string
	Label      string
}

type keyChordHUDGroup struct {
	Title   string
	Entries []keyChordHUDCompletion
}

// keyChordCompletions lists the configured actions whose binding starts with
// prefix. Custom bindings own the pending state, so only those are offered.
func (m *Model) keyChordCompletions(prefix string) []keyChordHUDCompletion {
	prefix = strings.TrimSpace(prefix)
	if m == nil || prefix == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []keyChordHUDCompletion
	for _, descriptor := range m.keymapActionDescriptors() {
		bindings, _, custom := codextui.ResolvedKeymapBindings(m.keymapConfig, descriptor.Context, descriptor.Action)
		if !custom {
			continue
		}
		for _, binding := range bindings {
			strokes := strings.Fields(binding)
			if len(strokes) != 2 || strokes[0] != prefix {
				continue
			}
			key := descriptor.Action + "\x00" + strokes[1]
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, keyChordHUDCompletion{
				Completion: codextui.KeybindingDisplayLabel(strokes[1]),
				Label:      descriptor.Label,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Completion == out[j].Completion {
			return out[i].Label < out[j].Label
		}
		return out[i].Completion < out[j].Completion
	})
	return out
}

func (m *Model) keymapActionDescriptors() []codextui.KeymapAction {
	fastMode := false
	if m != nil {
		fastMode = m.featureSettings["fast_mode"]
	}
	return codextui.KeymapActions(codextui.KeymapActionFilter{FastModeEnabled: fastMode})
}

// leaderChordHUDGroups mirrors workspace_hud.rs: leader completions are grouped
// into Panes, Move, and This pane, and unavailable pane actions are omitted.
func (m *Model) workspaceSidebarFocused() bool {
	if m == nil {
		return false
	}
	if m.sidebarChooserVisible() && m.sidebarChooserFocused {
		return true
	}
	return m.activeSide != nil && m.activeSide.ShowingSide
}

func (m *Model) leaderChordHUDGroups() []keyChordHUDGroup {
	if m == nil || len(m.leaderChordActions) == 0 {
		return nil
	}
	side := m.workspaceSidebarFocused()
	canSize := m.sidePaneSplitAvailable()
	canResize := m.sideBarResizableSplitAvailable()
	hasSideTab := m.activeSide != nil && strings.TrimSpace(m.activeSide.ParentThreadID) != ""
	items := make([][]keyChordHUDCompletion, 3)
	for completion, action := range m.leaderChordActions {
		group, label, ok := leaderHUDEntry(action, side, canSize, canResize, hasSideTab, m.sideZoomed)
		if !ok {
			continue
		}
		items[group] = append(items[group], keyChordHUDCompletion{
			Completion: codextui.KeybindingDisplayLabel(completion),
			Label:      label,
		})
	}
	titles := []string{"Panes", "Move", "This pane"}
	groups := make([]keyChordHUDGroup, 0, len(titles))
	for index, title := range titles {
		if len(items[index]) == 0 {
			continue
		}
		sort.SliceStable(items[index], func(i, j int) bool {
			if items[index][i].Completion == items[index][j].Completion {
				return items[index][i].Label < items[index][j].Label
			}
			return items[index][i].Completion < items[index][j].Completion
		})
		groups = append(groups, keyChordHUDGroup{Title: title, Entries: items[index]})
	}
	return groups
}

// leaderHUDEntry maps a workspace leader action to its guide group and label.
// The action strings keep their keymap context prefix ("global.", "chat.").
func leaderHUDEntry(action string, side bool, canSize bool, canResize bool, hasSideTab bool, zoomed bool) (int, string, bool) {
	action = strings.TrimPrefix(action, "global.")
	action = strings.TrimPrefix(action, "chat.")
	switch action {
	case "focus_command_center":
		return 0, "Command Center", true
	case "focus_main_composer":
		return 0, "Main composer", true
	case "focus_sidebar_tabs":
		return 0, "Open in sidebar", true
	case "focus_sidebar_content":
		if side {
			return 0, "Hide sidebar", true
		}
		return 0, "Sidebar content", true
	case "resize_panes":
		if canSize {
			return 2, "Resize panes", true
		}
	case "minimize_pane_width":
		if canSize {
			return 2, "Minimum width", true
		}
	case "maximize_pane_width":
		if canSize {
			return 2, "Maximum width", true
		}
	case "equalize_pane_widths":
		if canSize {
			return 2, "Equal widths", true
		}
	case "move_divider_left":
		if canResize {
			return 2, "Divider left", true
		}
	case "move_divider_right":
		if canResize {
			return 2, "Divider right", true
		}
	case "toggle_pane_zoom":
		if zoomed {
			return 2, "Restore layout", true
		}
		return 2, "Zoom pane", true
	case "close_sidebar_tab":
		if hasSideTab {
			return 2, "Close tab", true
		}
	case "sidebar_up":
		return 1, "Previous tab", true
	case "sidebar_down":
		return 1, "Next tab", true
	case "hide_pane":
		if side {
			return 2, "Hide pane", true
		}
	case "focus_pane_left":
		if side {
			return 1, "Pane left", true
		}
	case "focus_pane_right":
		if !side {
			return 1, "Pane right", true
		}
	case "open_presets":
		return 2, "Presets ›", true
	case "find_transcript":
		return 2, "Find text", true
	case "select_transcript":
		return 2, "Select transcript", true
	case "focus_conversation_composer":
		return 2, "Composer", true
	default:
		return 2, strings.ReplaceAll(action, "_", " "), true
	}
	return 0, "", false
}

// pendingChordGuide reports the chord whose guide should be painted after the
// reveal delay and whether it came from an explicit two-stroke binding.
func (m *Model) pendingChordGuide() (string, bool, bool) {
	if m == nil {
		return "", false, false
	}
	if prefix := strings.TrimSpace(m.pendingKeyChordPrefix); prefix != "" && !m.pendingKeyChordStarted.IsZero() {
		return prefix, true, true
	}
	if m.leaderChordPending && !m.leaderChordStartedAt.IsZero() {
		return strings.TrimSpace(m.leaderChordPrefix), false, true
	}
	return "", false, false
}

// renderKeyChordHUD returns the floating guide, or nothing while the prefix is
// absent or still inside the reveal delay.
func (m *Model) renderKeyChordHUD() string {
	prefix, custom, ok := m.pendingChordGuide()
	if !ok {
		return ""
	}
	started := m.pendingKeyChordStarted
	if !custom {
		started = m.leaderChordStartedAt
	}
	if m.currentTime().Sub(started) < keyChordRevealDelay {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	inner := max(width-2, 1)
	title := codextui.KeybindingDisplayLabel(prefix)
	lines := []string{}
	if custom {
		entries := m.keyChordCompletions(prefix)
		lines = append(lines, title, "")
		if len(entries) == 0 {
			lines = append(lines, "No matching shortcuts.")
		} else {
			rows := make([]string, 0, len(entries))
			for _, entry := range entries {
				rows = append(rows, entry.Completion+" · "+entry.Label)
			}
			columns := 1
			if len(rows) > 5 && inner >= 66 {
				columns = 2
			}
			lines = append(lines, layoutQuickDialEntries(rows, inner, columns)...)
		}
		lines = append(lines, "", keyChordHUDCancel)
	} else {
		origin := "Conversation"
		if m.workspaceSidebarFocused() {
			origin = "Sidebar"
		}
		lines = append(lines, title+" · "+origin)
		groups := m.leaderChordHUDGroups()
		if len(groups) == 0 {
			lines = append(lines, "", "No matching shortcuts.")
		}
		for _, group := range groups {
			lines = append(lines, "", group.Title)
			keyWidth := 0
			for _, entry := range group.Entries {
				keyWidth = max(keyWidth, codextui.DisplayWidth(entry.Completion))
			}
			for _, entry := range group.Entries {
				padding := strings.Repeat(" ", max(keyWidth-codextui.DisplayWidth(entry.Completion)+1, 1))
				lines = append(lines, entry.Completion+padding+entry.Label)
			}
		}
		lines = append(lines, "", keyChordHUDCancel)
	}
	lines = clipKeyChordHUD(lines, m.height, inner)
	for index, line := range lines {
		lines[index] = "  " + fitTerminalLine(line, inner)
	}
	return strings.Join(lines, "\n")
}

// clipKeyChordHUD adapts the guide to short viewports. Below seven rows Rust
// merges the cancel hint into the title and clips the body to the available
// space; taller terminals keep at least the title and the hint visible.
func clipKeyChordHUD(lines []string, height int, inner int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	if height < 7 {
		title := lines[0]
		body := lines[1:]
		if codextui.DisplayWidth(title)+3+len(keyChordHUDCancel) <= inner {
			title = title + " · " + keyChordHUDCancel
		}
		budget := max(height-1, 0)
		if len(body) > budget {
			body = body[:budget]
		}
		if len(body) > 0 && len(lines)-1 > budget {
			body[len(body)-1] = keyChordHUDMore
		}
		return append([]string{title}, body...)
	}
	out := append([]string(nil), lines[:height]...)
	out[height-1] = keyChordHUDMore
	return out
}

// composeWithKeyChordHUD keeps a full-screen surface inside the terminal while
// showing the pending guide at its bottom edge. Rust paints the guide as a
// floating overlay; Go's renderer is line-oriented, so it trims the base view
// when the guide would otherwise push past the terminal height.
func (m *Model) composeWithKeyChordHUD(view string) string {
	hud := m.renderKeyChordHUD()
	if hud == "" {
		return view
	}
	lines := strings.Split(view, "\n")
	hudLines := strings.Split(hud, "\n")
	if m != nil && m.height > 0 && len(lines)+len(hudLines) > m.height {
		keep := max(m.height-len(hudLines), 0)
		if keep < len(lines) {
			lines = lines[:keep]
		}
	}
	return strings.Join(append(lines, hudLines...), "\n")
}
