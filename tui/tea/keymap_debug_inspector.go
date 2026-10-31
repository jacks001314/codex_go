package tea

import (
	"fmt"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	codextui "codex_go/tui"
)

// keymapInspectorViewID names Rust's keymap_setup/debug.rs KeymapDebugView, the
// live keypress inspector the /keymap Debug tab opens.
const keymapInspectorViewID = "keymap-inspector"

// keymapInspectorHintDelay is MISSING_KEY_HINT_DELAY: the inspector only warns
// that the terminal may not send a key after it has waited this long.
const keymapInspectorHintDelay = 3 * time.Second

const (
	keymapInspectorShortHint   = "Tip: Codex can only inspect keys your terminal sends."
	keymapInspectorDelayedHint = "Still waiting? If nothing changes when you press a key, your terminal is not sending that key to Codex. Only received keys can be assigned as shortcuts."
	keymapInspectorUnsupported = "That key is not supported by `tui.keymap`."
)

type keymapInspectorState struct {
	openedAt time.Time
	// tab is the /keymap tab the inspector returns to on Ctrl+C.
	tab    int
	report *keymapInspectorReport
}

type keymapInspectorReport struct {
	Detected  string
	ConfigKey string
	RawEvent  string
	Matches   []codextui.KeymapActionMatch
}

// keymapInspectorTickMsg re-renders the inspector once the delayed
// "terminal is not sending that key" hint becomes due.
type keymapInspectorTickMsg struct{}

func keymapInspectorTickCmd() bubbletea.Cmd {
	return bubbletea.Tick(keymapInspectorHintDelay, func(time.Time) bubbletea.Msg {
		return keymapInspectorTickMsg{}
	})
}

// openKeymapInspector replaces the /keymap picker with Rust's Keypress
// Inspector. The picker's tab is remembered so Ctrl+C returns to it.
func (m *Model) openKeymapInspector() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	tab := 0
	if m.modal != nil && m.modal.keymapPanel != nil {
		tab = m.modal.keymapPanel.tab
	}
	m.replaceModal(&modalState{
		id:              keymapInspectorViewID,
		kind:            ModalKindGeneric,
		inlinePicker:    true,
		keymapInspector: &keymapInspectorState{openedAt: time.Now(), tab: tab},
	})
	m.notice = ""
	return keymapInspectorTickCmd()
}

func (m *Model) closeKeymapInspector() {
	tab := 0
	if m.modal != nil && m.modal.keymapInspector != nil {
		tab = m.modal.keymapInspector.tab
	}
	m.openKeymapPicker("", "")
	if m.modal != nil && m.modal.keymapPanel != nil {
		tabs := m.keymapPanelTabs()
		if tab >= 0 && tab < len(tabs) {
			m.modal.keymapPanel.tab = tab
		}
	}
	m.refreshTranscript()
}

// updateKeymapInspector records every non-Ctrl+C keypress: Rust's view asks for
// raw key capture, so Esc is inspected instead of dismissing the view.
func (m *Model) updateKeymapInspector(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.keymapInspector == nil {
		return nil
	}
	if message.Type == bubbletea.KeyCtrlC && !message.Alt {
		m.closeKeymapInspector()
		return nil
	}
	spec := keySpecFromKeyMsg(message)
	report := &keymapInspectorReport{
		Detected: keymapInspectorDetectedLabel(message),
		RawEvent: keymapInspectorRawEvent(message),
	}
	if spec != "" && codextui.KeymapStorableSpec(spec) {
		report.ConfigKey = spec
	}
	report.Matches = codextui.KeymapActionsForKey(m.keymapPageFilter(), m.keymapConfig, spec)
	m.modal.keymapInspector.report = report
	return nil
}

func keymapInspectorDim(text string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(text)
}

func keymapInspectorCyan(text string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(text)
}

func keymapInspectorBold(text string) string {
	return lipgloss.NewStyle().Bold(true).Render(text)
}

// renderKeymapInspector mirrors KeymapDebugView::lines: the transcript keeps
// the rows above and the view grows upward from the bottom of the pane.
func (m *Model) renderKeymapInspector() string {
	if m == nil || m.modal == nil || m.modal.keymapInspector == nil {
		return ""
	}
	inspector := m.modal.keymapInspector
	width := max(m.width, 1)
	lines := []string{
		keymapInspectorBold("Keypress Inspector"),
		keymapInspectorDim("Press any key to see what Codex receives. Esc is inspected; Ctrl+C closes."),
	}
	hint := keymapInspectorShortHint
	if inspector.report == nil && time.Since(inspector.openedAt) >= keymapInspectorHintDelay {
		hint = keymapInspectorDelayedHint
	}
	lines = append(lines, keymapInspectorWrap(hint, width, "", "")...)
	report := inspector.report
	if report == nil {
		lines = append(lines, "", keymapInspectorCyan("Waiting for a keypress..."))
		return strings.Join(lines, "\n")
	}
	lines = append(lines,
		"",
		keymapInspectorDim("Detected: ")+keymapInspectorCyan(report.Detected),
	)
	if report.ConfigKey != "" {
		lines = append(lines, keymapInspectorDim("Config key: ")+keymapInspectorCyan(report.ConfigKey))
	} else {
		lines = append(lines, keymapInspectorWrap("unsupported - "+keymapInspectorUnsupported, width, "Config key: ", "            ")...)
	}
	lines = append(lines, keymapInspectorWrap(report.RawEvent, width, "Raw event: ", "           ")...)
	lines = append(lines, "", keymapInspectorDim("Assigned actions:"))
	if len(report.Matches) == 0 {
		lines = append(lines, keymapInspectorDim("  none"))
	} else {
		for _, match := range report.Matches {
			text := fmt.Sprintf("%s.%s (%s) - %s [%s]", match.Context, match.Action, match.Label, match.Description, match.Source)
			lines = append(lines, keymapInspectorWrap(text, width, "  - ", "    ")...)
		}
	}
	return strings.Join(lines, "\n")
}

// keymapInspectorWrap mirrors debug.rs::push_wrapped_dim: textwrap over the
// terminal width with an initial and subsequent indent.
func keymapInspectorWrap(text string, width int, initial string, subsequent string) []string {
	if width <= 0 {
		width = 1
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{initial}
	}
	prefix := initial
	lines := []string{}
	var builder strings.Builder
	lineWidth := 0
	start := func() {
		builder.Reset()
		builder.WriteString(prefix)
		lineWidth = codextui.DisplayWidth(prefix)
	}
	start()
	empty := true
	for _, word := range words {
		wordWidth := codextui.DisplayWidth(word)
		if !empty && lineWidth+1+wordWidth > width {
			lines = append(lines, builder.String())
			prefix = subsequent
			start()
			empty = true
		}
		if !empty {
			builder.WriteString(" ")
			lineWidth++
		}
		builder.WriteString(word)
		lineWidth += wordWidth
		empty = false
	}
	lines = append(lines, builder.String())
	return lines
}

// keymapInspectorCodeDebug renders KeyCode's Debug form the way crossterm does
// (`code=Char('k'), modifiers=ctrl, kind=Press`).
func keymapInspectorCodeDebug(message bubbletea.KeyMsg) string {
	key := bubbletea.Key(message)
	if key.Type == bubbletea.KeyRunes {
		if len(key.Runes) == 1 {
			if key.Runes[0] == 0 {
				// Rust's `{:?}` on a NUL char escapes it as `\u{0}`.
				return "Char('\\u{0}')"
			}
			return fmt.Sprintf("Char('%c')", key.Runes[0])
		}
		if len(key.Runes) > 1 {
			return fmt.Sprintf("Char('%c')", key.Runes[0])
		}
		return "Char('?')"
	}
	if letter, ok := keymapInspectorCtrlLetter(key.Type); ok {
		return fmt.Sprintf("Char('%c')", letter)
	}
	switch key.Type {
	case bubbletea.KeyCtrlUnderscore:
		return "Char('/')"
	case bubbletea.KeyCtrlBackslash:
		return "Char('\\\\')"
	case bubbletea.KeyCtrlCloseBracket:
		return "Char(']')"
	case bubbletea.KeyCtrlCaret:
		return "Char('^')"
	case bubbletea.KeyCtrlAt:
		return "Char(' ')"
	case bubbletea.KeySpace:
		return "Char(' ')"
	case bubbletea.KeyEsc:
		return "Esc"
	case bubbletea.KeyEnter:
		return "Enter"
	case bubbletea.KeyTab:
		return "Tab"
	case bubbletea.KeyBackspace:
		return "Backspace"
	case bubbletea.KeyDelete:
		return "Delete"
	case bubbletea.KeyInsert:
		return "Insert"
	case bubbletea.KeyUp, bubbletea.KeyShiftUp, bubbletea.KeyCtrlUp, bubbletea.KeyCtrlShiftUp:
		return "Up"
	case bubbletea.KeyDown, bubbletea.KeyShiftDown, bubbletea.KeyCtrlDown, bubbletea.KeyCtrlShiftDown:
		return "Down"
	case bubbletea.KeyLeft, bubbletea.KeyShiftLeft, bubbletea.KeyCtrlLeft, bubbletea.KeyCtrlShiftLeft:
		return "Left"
	case bubbletea.KeyRight, bubbletea.KeyShiftRight, bubbletea.KeyCtrlRight, bubbletea.KeyCtrlShiftRight:
		return "Right"
	case bubbletea.KeyHome, bubbletea.KeyShiftHome, bubbletea.KeyCtrlHome:
		return "Home"
	case bubbletea.KeyEnd, bubbletea.KeyShiftEnd, bubbletea.KeyCtrlEnd:
		return "End"
	case bubbletea.KeyPgUp, bubbletea.KeyCtrlPgUp:
		return "PageUp"
	case bubbletea.KeyPgDown, bubbletea.KeyCtrlPgDown:
		return "PageDown"
	case bubbletea.KeyShiftTab:
		return "BackTab"
	}
	if number, ok := keymapInspectorFunctionKey(key.Type); ok {
		return fmt.Sprintf("F(%d)", number)
	}
	return strings.ReplaceAll(message.String(), "+", "-")
}

func keymapInspectorCtrlLetter(keyType bubbletea.KeyType) (rune, bool) {
	if keyType < bubbletea.KeyCtrlA || keyType > bubbletea.KeyCtrlZ {
		return 0, false
	}
	// Bubble Tea aliases Ctrl+I to KeyTab and Ctrl+M to KeyEnter; terminals
	// cannot distinguish them, and Rust reports the named key.
	letter := rune('a' + int(keyType-bubbletea.KeyCtrlA))
	if letter == 'i' || letter == 'm' {
		return 0, false
	}
	return letter, true
}

func keymapInspectorFunctionKey(keyType bubbletea.KeyType) (int, bool) {
	// Named keys count downward from KeyRunes, so KeyF1 > KeyF2 > ... > KeyF20.
	if keyType > bubbletea.KeyF1 || keyType < bubbletea.KeyF20 {
		return 0, false
	}
	return int(bubbletea.KeyF1-keyType) + 1, true
}

func keymapInspectorModifiers(message bubbletea.KeyMsg) (ctrl bool, alt bool, shift bool) {
	key := bubbletea.Key(message)
	alt = key.Alt
	if key.Type >= bubbletea.KeyCtrlA && key.Type <= bubbletea.KeyCtrlZ {
		// KeyTab, KeyEnter and KeyEsc share their byte values with control
		// codes; Bubble Tea reports the named key, so they carry no ctrl.
		ctrl = key.Type != bubbletea.KeyTab && key.Type != bubbletea.KeyEnter
	}
	switch key.Type {
	case bubbletea.KeyCtrlAt, bubbletea.KeyCtrlUnderscore, bubbletea.KeyCtrlBackslash,
		bubbletea.KeyCtrlCloseBracket, bubbletea.KeyCtrlCaret:
		ctrl = true
	}
	switch key.Type {
	case bubbletea.KeyCtrlUp, bubbletea.KeyCtrlDown, bubbletea.KeyCtrlLeft, bubbletea.KeyCtrlRight,
		bubbletea.KeyCtrlHome, bubbletea.KeyCtrlEnd, bubbletea.KeyCtrlPgUp, bubbletea.KeyCtrlPgDown:
		ctrl = true
	case bubbletea.KeyShiftUp, bubbletea.KeyShiftDown, bubbletea.KeyShiftLeft, bubbletea.KeyShiftRight,
		bubbletea.KeyShiftHome, bubbletea.KeyShiftEnd, bubbletea.KeyShiftTab:
		shift = true
	case bubbletea.KeyCtrlShiftUp, bubbletea.KeyCtrlShiftDown, bubbletea.KeyCtrlShiftLeft, bubbletea.KeyCtrlShiftRight:
		ctrl = true
		shift = true
	}
	if key.Type == bubbletea.KeyRunes && len(key.Runes) == 1 {
		letter := key.Runes[0]
		if letter >= 'A' && letter <= 'Z' {
			shift = true
		}
	}
	return ctrl, alt, shift
}

// keymapInspectorModifierList is key_modifiers_debug_label: ctrl, alt, shift in
// that order, joined with `|`, or "none".
func keymapInspectorModifierList(message bubbletea.KeyMsg) string {
	ctrl, alt, shift := keymapInspectorModifiers(message)
	parts := []string{}
	if ctrl {
		parts = append(parts, "ctrl")
	}
	if alt {
		parts = append(parts, "alt")
	}
	if shift {
		parts = append(parts, "shift")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "|")
}

func keymapInspectorRawEvent(message bubbletea.KeyMsg) string {
	return fmt.Sprintf("code=%s, modifiers=%s, kind=Press", keymapInspectorCodeDebug(message), keymapInspectorModifierList(message))
}

// keymapInspectorDetectedLabel is KeyBinding::display_label: `ctrl+shift+alt`
// before the normalized key name (arrows use their glyphs).
func keymapInspectorDetectedLabel(message bubbletea.KeyMsg) string {
	key := bubbletea.Key(message)
	if key.Type == bubbletea.KeyRunes && len(key.Runes) == 1 && key.Runes[0] == 0 {
		// key_hint.rs::c0_control_char_to_ctrl_char normalizes a bare NUL to
		// Ctrl+Space, so the detected label is the normalized binding.
		return "ctrl+space"
	}
	ctrl, alt, shift := keymapInspectorModifiers(message)
	label := ""
	if ctrl {
		label += "ctrl+"
	}
	if shift {
		label += "shift+"
	}
	if alt {
		label += "alt+"
	}
	return label + keymapInspectorKeyLabel(message)
}

func keymapInspectorKeyLabel(message bubbletea.KeyMsg) string {
	key := bubbletea.Key(message)
	if key.Type == bubbletea.KeyRunes && len(key.Runes) == 1 {
		letter := key.Runes[0]
		if letter >= 'A' && letter <= 'Z' {
			letter = letter + ('a' - 'A')
		}
		if letter == ' ' {
			return "space"
		}
		return string(letter)
	}
	if letter, ok := keymapInspectorCtrlLetter(key.Type); ok {
		return string(letter)
	}
	switch key.Type {
	case bubbletea.KeyCtrlUnderscore:
		return "/"
	case bubbletea.KeyCtrlBackslash:
		return "\\\\"
	case bubbletea.KeyCtrlCloseBracket:
		return "]"
	case bubbletea.KeyCtrlCaret:
		return "^"
	case bubbletea.KeyCtrlAt, bubbletea.KeySpace:
		return "space"
	case bubbletea.KeyEsc:
		return "esc"
	case bubbletea.KeyEnter:
		return "enter"
	case bubbletea.KeyTab:
		return "tab"
	case bubbletea.KeyBackspace:
		return "backspace"
	case bubbletea.KeyDelete:
		return "delete"
	case bubbletea.KeyInsert:
		return "insert"
	case bubbletea.KeyUp, bubbletea.KeyShiftUp, bubbletea.KeyCtrlUp, bubbletea.KeyCtrlShiftUp:
		return "\u2191"
	case bubbletea.KeyDown, bubbletea.KeyShiftDown, bubbletea.KeyCtrlDown, bubbletea.KeyCtrlShiftDown:
		return "\u2193"
	case bubbletea.KeyLeft, bubbletea.KeyShiftLeft, bubbletea.KeyCtrlLeft, bubbletea.KeyCtrlShiftLeft:
		return "\u2190"
	case bubbletea.KeyRight, bubbletea.KeyShiftRight, bubbletea.KeyCtrlRight, bubbletea.KeyCtrlShiftRight:
		return "\u2192"
	case bubbletea.KeyHome, bubbletea.KeyShiftHome, bubbletea.KeyCtrlHome:
		return "home"
	case bubbletea.KeyEnd, bubbletea.KeyShiftEnd, bubbletea.KeyCtrlEnd:
		return "end"
	case bubbletea.KeyPgUp, bubbletea.KeyCtrlPgUp:
		return "pgup"
	case bubbletea.KeyPgDown, bubbletea.KeyCtrlPgDown:
		return "pgdn"
	case bubbletea.KeyShiftTab:
		return "backtab"
	}
	if number, ok := keymapInspectorFunctionKey(key.Type); ok {
		return fmt.Sprintf("f%d", number)
	}
	return strings.ToLower(message.String())
}
