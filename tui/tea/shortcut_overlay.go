package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// shortcutOverlayColumnGap mirrors Rust's shortcut_help::COLUMN_GAP.
const shortcutOverlayColumnGap = 4

type shortcutOverlayEntry struct {
	Key    string
	Action string
}

type shortcutOverlayGroup struct {
	Title   string
	Entries []shortcutOverlayEntry
}

// defaultShortcutOverlayGroups mirrors Rust's shortcut_overlay::lines group
// table. Groups flow into three, two, or one column depending on width.
func defaultShortcutOverlayGroups() [3]shortcutOverlayGroup {
	return shortcutOverlayGroupsFor(nil)
}

// shortcutOverlayGroups resolves the shortcut reference against the live
// keymap. Rust #47338 renders "Inspect activity" from the runtime
// global.focus_activity binding (F4 by default) and hides the row when the
// action is unbound; the voice row follows chat.toggle_voice the same way.
func (m *Model) shortcutOverlayGroups() [3]shortcutOverlayGroup {
	return shortcutOverlayGroupsFor(m)
}

func shortcutOverlayGroupsFor(m *Model) [3]shortcutOverlayGroup {
	compose := shortcutOverlayGroup{Title: "Compose", Entries: []shortcutOverlayEntry{
		{Key: "/", Action: "Commands"},
		{Key: "@", Action: "Mention files"},
		{Key: "!", Action: "Shell command"},
		{Key: PlainKey(keymsg.KeyCtrlJ).Label(), Action: "New line"},
		{Key: PlainKey(keymsg.KeyCtrlV).Label(), Action: "Paste image"},
		{Key: PlainKey(keymsg.KeyCtrlG).Label(), Action: "External editor"},
		{Key: PlainKey(keymsg.KeyCtrlR).Label(), Action: "Search history"},
		{Key: "esc esc", Action: "Edit last message"},
	}}
	session := shortcutOverlayGroup{Title: "Session", Entries: []shortcutOverlayEntry{
		{Key: PlainKey(keymsg.KeyTab).Label(), Action: "Send message"},
		{Key: "shift+tab", Action: "Change mode"},
		{Key: "alt+,", Action: "Less reasoning"},
		{Key: "alt+.", Action: "More reasoning"},
		{Key: "f8", Action: "Voice"},
		{Key: "alt+\u2190 / alt+\u2192", Action: "Subagents (empty prompt)"},
		{Key: "f4", Action: "Inspect activity"},
		{Key: PlainKey(keymsg.KeyCtrlC).Label(), Action: "Quit"},
	}}
	transcript := shortcutOverlayGroup{Title: "Transcript (open first)", Entries: []shortcutOverlayEntry{
		{Key: "ctrl+t", Action: "Open transcript"},
		{Key: "f3", Action: "Find text"},
		{Key: "pgup / pgdn", Action: "Scroll"},
		{Key: "ctrl+space", Action: "Start selection"},
		{Key: "ctrl+home / ctrl+end", Action: "Top / latest"},
		{Key: "shift+alt+, / shift+alt+.", Action: "Top / latest"},
	}}
	session.Entries = resolveShortcutOverlayEntry(m, session.Entries, "Voice", "chat", "toggle_voice")
	session.Entries = resolveShortcutOverlayEntry(m, session.Entries, "Inspect activity", "global", "focus_activity")
	return [3]shortcutOverlayGroup{compose, session, transcript}
}

// resolveShortcutOverlayEntry swaps the placeholder key of one entry for the
// configured binding. An unbound action removes the entry, matching Rust's
// hint list which skips actions without a binding.
func resolveShortcutOverlayEntry(m *Model, entries []shortcutOverlayEntry, actionLabel, context, action string) []shortcutOverlayEntry {
	if m == nil {
		return entries
	}
	label := ""
	if bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, context, action); len(bindings) > 0 {
		label = displayKeyBinding(bindings[0])
	}
	resolved := make([]shortcutOverlayEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Action != actionLabel {
			resolved = append(resolved, entry)
			continue
		}
		if label != "" {
			entry.Key = label
			resolved = append(resolved, entry)
		}
	}
	return resolved
}

// shortcutOverlayLines renders the reference Rust shows after pressing `?` on
// an empty composer. Keys are accent-coloured, group titles bold, and the
// `/keymap customize` footer keeps the customization entry point visible.
func (m *Model) shortcutOverlayLines(width int) []string {
	if m == nil {
		return nil
	}
	width = max(width, 1)
	accent := codextui.ForcedColorStyle().
		Foreground(lipgloss.Color(codextui.ThemeAccentColor(m.activeTUITheme())))
	groups := m.shortcutOverlayGroups()
	rendered := make([][]string, 0, len(groups))
	widths := make([]int, 0, len(groups))
	for _, group := range groups {
		lines, groupWidth := renderShortcutOverlayGroup(group, accent)
		rendered = append(rendered, lines)
		widths = append(widths, groupWidth)
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Render("Keyboard shortcuts"), ""}
	lines = append(lines, layoutShortcutOverlayColumns(rendered, widths, width)...)
	lines = append(lines, "")
	lines = append(lines, accent.Render("/keymap")+m.footerStyle.Render(" customize"))
	return lines
}

func renderShortcutOverlayGroup(group shortcutOverlayGroup, accent lipgloss.Style) ([]string, int) {
	keyWidth := 0
	for _, entry := range group.Entries {
		keyWidth = max(keyWidth, ansi.StringWidth(entry.Key))
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Render(group.Title)}
	width := ansi.StringWidth(group.Title)
	for _, entry := range group.Entries {
		padding := strings.Repeat(" ", keyWidth-ansi.StringWidth(entry.Key)+2)
		line := accent.Render(entry.Key) + padding + entry.Action
		width = max(width, ansi.StringWidth(entry.Key)+len(padding)+ansi.StringWidth(entry.Action))
		lines = append(lines, line)
	}
	return lines, width
}

// layoutShortcutOverlayColumns mirrors Rust's group_lines: three columns when
// they fit, then two, otherwise a single stacked column.
func layoutShortcutOverlayColumns(groups [][]string, widths []int, width int) []string {
	if widths[0]+widths[1]+widths[2]+shortcutOverlayColumnGap*2 <= width {
		return joinShortcutOverlayColumns(groups, widths)
	}
	if widths[0]+shortcutOverlayColumnGap+max(widths[1], widths[2]) <= width {
		right := append(append([]string{}, groups[1]...), "")
		right = append(right, groups[2]...)
		return joinShortcutOverlayColumns([][]string{groups[0], right}, []int{widths[0], max(widths[1], widths[2])})
	}
	var lines []string
	for index, group := range groups {
		if index > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, group...)
	}
	return lines
}

func joinShortcutOverlayColumns(groups [][]string, widths []int) []string {
	height := 0
	for _, group := range groups {
		height = max(height, len(group))
	}
	lines := make([]string, 0, height)
	for row := 0; row < height; row++ {
		var builder strings.Builder
		for column, group := range groups {
			entry := ""
			if row < len(group) {
				entry = group[row]
			}
			builder.WriteString(entry)
			if column+1 < len(groups) {
				// Rendered groups carry accent/bold SGR codes; measure the
				// visible cells so coloured terminals align like tests do.
				padding := widths[column] - ansi.StringWidth(entry) + shortcutOverlayColumnGap
				builder.WriteString(strings.Repeat(" ", max(padding, 0)))
			}
		}
		lines = append(lines, strings.TrimRight(builder.String(), " "))
	}
	return lines
}

// renderShortcutOverlay paints the reference block above the composer with the
// standard two-column popup inset.
func (m *Model) renderShortcutOverlay() string {
	if m == nil || !m.shortcutOverlay {
		return ""
	}
	lines := m.shortcutOverlayLines(max(m.width-2, 1))
	for index, line := range lines {
		lines[index] = "  " + line
	}
	return strings.Join(lines, "\n")
}

// handleShortcutOverlayKey mirrors Rust's empty-prompt `?` toggle: the overlay
// opens only for an empty composer with no popup or dialog competing for input,
// closes on `?`/Esc, and any other key dismisses it while still being handled.
func (m *Model) handleShortcutOverlayKey(msg keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil {
		return false, nil
	}
	toggle := msg.Type == keymsg.KeyRunes && string(msg.Runes) == "?" && !msg.Alt && !msg.Paste
	if m.shortcutOverlay {
		if toggle || msg.Type == keymsg.KeyEsc {
			m.shortcutOverlay = false
			return true, nil
		}
		m.shortcutOverlay = false
		return false, nil
	}
	if !toggle || msg.Alt || msg.Paste {
		return false, nil
	}
	if m.modal != nil || m.overlay != nil || m.agentsOverview != nil || m.readOnlyThread {
		return false, nil
	}
	// Vim normal mode owns `?` (backward search), matching Rust's
	// wants_vim_search_key check that runs before the footer toggle.
	if m.vimMode && !m.vimInsert {
		return false, nil
	}
	if m.slashPopup.Active || m.skillPopup.Active || m.mentionPopup != nil || m.historySearch != nil || m.vimSearchMode {
		return false, nil
	}
	if !m.composerIsEmpty() || len(m.attachments) > 0 {
		return false, nil
	}
	m.shortcutOverlay = true
	return true, nil
}
