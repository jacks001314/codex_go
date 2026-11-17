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
	// Keys carries the canonical binding specs behind the rendered label.
	// The keyboard diagram resolves them back to key caps; rows without a
	// binding (or whose action is unbound) leave it empty.
	Keys []string
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
		{Key: "/", Action: "Commands", Keys: []string{"/"}},
		{Key: "@", Action: "Mention files", Keys: []string{"@"}},
		{Key: "!", Action: "Shell command", Keys: []string{"!"}},
		{Key: PlainKey(keymsg.KeyCtrlJ).Label(), Action: "New line", Keys: []string{"ctrl-j"}},
		{Key: PlainKey(keymsg.KeyCtrlV).Label(), Action: "Paste image", Keys: []string{"ctrl-v"}},
		{Key: PlainKey(keymsg.KeyCtrlG).Label(), Action: "External editor", Keys: []string{"ctrl-g"}},
		{Key: PlainKey(keymsg.KeyCtrlR).Label(), Action: "Search history", Keys: []string{"ctrl-r"}},
		{Key: "esc esc", Action: "Edit last message", Keys: []string{"esc"}},
	}}
	session := shortcutOverlayGroup{Title: "Session", Entries: []shortcutOverlayEntry{
		{Key: PlainKey(keymsg.KeyTab).Label(), Action: "Send message", Keys: []string{"tab"}},
		{Key: "shift+tab", Action: "Change mode", Keys: []string{"shift-tab"}},
		{Key: "alt+,", Action: "Less reasoning", Keys: []string{"alt-,"}},
		{Key: "alt+.", Action: "More reasoning", Keys: []string{"alt-."}},
		{Key: "f8", Action: "Voice", Keys: []string{"f8"}},
		{Key: "alt+\u2190 / alt+\u2192", Action: "Subagents (empty prompt)", Keys: []string{"alt-left", "alt-right"}},
		{Key: "f4", Action: "Inspect activity", Keys: []string{"f4"}},
		{Key: PlainKey(keymsg.KeyCtrlC).Label(), Action: "Quit", Keys: []string{"ctrl-c"}},
	}}
	transcript := shortcutOverlayGroup{Title: "Transcript (open first)", Entries: []shortcutOverlayEntry{
		{Key: "ctrl+t", Action: "Open transcript", Keys: []string{"ctrl-t"}},
		{Key: "f3", Action: "Find text", Keys: []string{"f3"}},
		{Key: "pgup / pgdn", Action: "Scroll", Keys: []string{"page-up", "page-down"}},
		{Key: "ctrl+space", Action: "Start selection", Keys: []string{"ctrl-space"}},
		{Key: "ctrl+home / ctrl+end", Action: "Top / latest", Keys: []string{"ctrl-home", "ctrl-end"}},
		{Key: "shift+alt+, / shift+alt+.", Action: "Top / latest", Keys: []string{"shift-alt-,", "shift-alt-."}},
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
	var resolvedKeys []string
	if bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, context, action); len(bindings) > 0 {
		label = displayKeyBinding(bindings[0])
		resolvedKeys = bindings[:1]
	}
	resolved := make([]shortcutOverlayEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Action != actionLabel {
			resolved = append(resolved, entry)
			continue
		}
		if label != "" {
			entry.Key = label
			entry.Keys = resolvedKeys
			resolved = append(resolved, entry)
		}
	}
	return resolved
}

// shortcutOverlayLines renders the reference Rust shows after pressing `?` on
// an empty composer. The grouped list is the default view; the keyboard view
// paints Rust's US QWERTY diagram and falls back to the list when the terminal
// is too narrow or the diagram plus its footer does not fit. height < 0 means
// unbounded (Rust's `lines()` helper passes usize::MAX).
func (m *Model) shortcutOverlayLines(width int, height int) []string {
	if m == nil {
		return nil
	}
	width = max(width, 1)
	level := codextui.DetectStdoutColorLevel()
	accent := codextui.ForcedColorStyle().
		Foreground(lipgloss.Color(codextui.ThemeAccentColor(m.activeTUITheme())))
	heading := lipgloss.NewStyle().Bold(true).Render("Keyboard shortcuts")
	footer := m.shortcutOverlayFooterLine()
	guideKey, hasGuideKey := m.shortcutOverlayGuideKeys()
	switchSuffix := func(view string) string {
		if !hasGuideKey {
			return ""
		}
		return "  ·  " + guideKey.label + ": " + view
	}
	if m.shortcutOverlayKeyboard && shortcutOverlayKeyboardFitsWidth(width) {
		actions := m.shortcutOverlayActions()
		empty := len(actions) == 0
		diagram := shortcutKeyboardLines(actions, width, level)
		title := heading + "  " + accent.Render("Base")
		if len(actions) > 0 {
			title += m.footerStyle.Render(" · More keys in list")
		}
		title += m.footerStyle.Render(switchSuffix("List"))
		result := []string{title}
		result = append(result, diagram...)
		if empty {
			result = append(result, m.footerStyle.Render("No shortcuts in this layer"))
		}
		if height < 0 || len(result)+1 <= height {
			return append(result, footer)
		}
	}
	groups := m.shortcutOverlayGroups()
	rendered := make([][]string, 0, len(groups))
	widths := make([]int, 0, len(groups))
	for _, group := range groups {
		lines, groupWidth := renderShortcutOverlayGroup(group, accent)
		rendered = append(rendered, lines)
		widths = append(widths, groupWidth)
	}
	listHeading := heading
	switch {
	case !m.shortcutOverlayKeyboard && shortcutOverlayKeyboardFitsWidth(width):
		listHeading += m.footerStyle.Render(switchSuffix("Keyboard"))
	case m.shortcutOverlayKeyboard:
		listHeading += m.footerStyle.Render("  ·  Keyboard unavailable")
		listHeading += m.footerStyle.Render(switchSuffix("List"))
	}
	lines := []string{listHeading, ""}
	lines = append(lines, layoutShortcutOverlayColumns(rendered, widths, width)...)
	lines = append(lines, "")
	lines = append(lines, footer)
	return lines
}

// shortcutOverlayActions maps the visible rows to the key caps the diagram can
// highlight (Rust's `guide_lines` action set). Keys with modifiers, chords, and
// keys outside the US layout never enter the set, so they stay discoverable in
// the list.
func (m *Model) shortcutOverlayActions() map[string]bool {
	actions := map[string]bool{}
	for _, group := range m.shortcutOverlayGroups() {
		for _, entry := range group.Entries {
			for _, spec := range entry.Keys {
				position, ok := m.shortcutOverlayKeyPosition(spec)
				if !ok || position.modifiers != 0 {
					continue
				}
				actions[position.code] = true
			}
		}
	}
	return actions
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
// standard two-column popup inset. The keyboard diagram asks for every row it
// needs (Rust's `height_budget` returns u16::MAX for that view); only when the
// terminal cannot hold it does the guide clip itself and keep Rust's
// "… resize to see all" hint above the `/keymap customize` footer.
func (m *Model) renderShortcutOverlay() string {
	if m == nil || !m.shortcutOverlay {
		return ""
	}
	width := max(m.width-2, 1)
	height := -1
	if m.height > 0 {
		height = max(m.height-m.shortcutOverlaySiblingRows(), 0)
	}
	lines := m.shortcutOverlayLines(width, height)
	if height >= 0 && len(lines) > height {
		footer := m.shortcutOverlayFooterLine()
		bodyHeight := max(height-1, 0)
		if bodyHeight > 0 {
			lines = lines[:bodyHeight-1]
			lines = append(lines, lipgloss.NewStyle().Faint(true).Render("… resize to see all"))
		} else {
			lines = nil
		}
		if height > 0 {
			lines = append(lines, footer)
		}
	}
	for index, line := range lines {
		lines[index] = "  " + line
	}
	return strings.Join(lines, "\n")
}

// shortcutOverlaySiblingRows counts the bottom-pane rows that share the frame
// with the guide, so the diagram can clip itself to the terminal instead of
// pushing the composer off screen.
func (m *Model) shortcutOverlaySiblingRows() int {
	return m.bottomReservedRowsExceptVoiceFor(true, 0)
}

// shortcutOverlayFooterLine paints the guide's `/keymap customize` entry point.
func (m *Model) shortcutOverlayFooterLine() string {
	accent := codextui.ForcedColorStyle().
		Foreground(lipgloss.Color(codextui.ThemeAccentColor(m.activeTUITheme())))
	return accent.Render("/keymap") + m.footerStyle.Render(" customize")
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
		if handled, cmd := m.handleShortcutOverlayViewKey(msg); handled {
			return true, cmd
		}
		if toggle || msg.Type == keymsg.KeyEsc {
			m.shortcutOverlay = false
			m.shortcutOverlayKeyboard = false
			return true, nil
		}
		m.shortcutOverlay = false
		m.shortcutOverlayKeyboard = false
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
	m.shortcutOverlayKeyboard = false
	return true, nil
}

// handleShortcutOverlayViewKey toggles between the list and the keyboard
// diagram on the guide's own switch key (Rust's `handle_shortcut_view_key`).
// It yields to an open sidebar chooser and to transcript interactions that own
// the key, and it never switches while the composer holds a draft.
func (m *Model) handleShortcutOverlayViewKey(msg keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || !m.shortcutOverlay {
		return false, nil
	}
	if msg.Type == keymsg.KeyRunes && msg.Paste {
		return false, nil
	}
	guideKey, ok := m.shortcutOverlayGuideKeys()
	if !ok || keySpecFromKeyMsg(msg) != guideKey.spec {
		return false, nil
	}
	if !m.composerIsEmpty() || len(m.attachments) > 0 {
		return false, nil
	}
	if m.sidebarChooserVisible() && m.sidebarChooserFocused {
		return false, nil
	}
	if m.transcriptSelection.active || m.renderedSelection.active || m.mainFind.active() {
		return false, nil
	}
	m.shortcutOverlayKeyboard = !m.shortcutOverlayKeyboard
	return true, nil
}
