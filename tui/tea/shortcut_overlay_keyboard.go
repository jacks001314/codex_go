package tea

import (
	"strconv"
	"strings"
	"unicode/utf8"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/shortcut_overlay/keyboard.rs and the
// `key_position`/`keyboard_fits_width` helpers in shortcut_overlay.rs. The
// `?` guide can switch from the grouped action list to a US QWERTY tenkeyless
// diagram that highlights the keys with unmodified bindings; keys outside the
// diagram stay discoverable in the list.

const (
	shortcutKeyboardRows       = 6
	shortcutKeyboardNavStart   = 52
	shortcutKeyboardInnerWidth = shortcutKeyboardNavStart + 16
	// shortcutKeyboardWidth is the narrowest guide width that can paint the
	// diagram (Rust `keyboard::WIDTH`).
	shortcutKeyboardWidth = shortcutKeyboardInnerWidth + 4
	// shortcutKeyboardRoomyWidth switches keycaps to Rust's roomier widths.
	shortcutKeyboardRoomyWidth = 92
)

// shortcutKeyboardUnshifted and shortcutKeyboardShifted are Rust's US-layout
// tables: `unshift` maps a shifted character back to the base cap so a binding
// such as `?` still points at the `/` key.
const (
	shortcutKeyboardUnshifted = "`1234567890-=[]\\;',./"
	shortcutKeyboardShifted   = "~!@#$%^&*()_+{}|:\"<>?"
)

const (
	shortcutModCtrl = 1 << iota
	shortcutModAlt
	shortcutModShift
	shortcutModSuper
)

// shortcutOverlayKeyPosition is Rust's `key_position`: the key cap a binding
// points at plus the modifiers that keep it off the base layer.
type shortcutOverlayKeyPosition struct {
	code      string
	modifiers int
}

// shortcutOverlayKeyPositionFromStroke resolves the first stroke of a canonical
// binding spec (for example `ctrl-t`, `shift-tab`, or `page-up`).
func shortcutOverlayKeyPositionFromStroke(stroke string) (shortcutOverlayKeyPosition, bool) {
	parts := strings.Split(strings.TrimSpace(stroke), "-")
	base := make([]string, 0, len(parts))
	modifiers := 0
	for _, part := range parts {
		switch part {
		case "ctrl", "control":
			modifiers |= shortcutModCtrl
		case "alt", "option":
			modifiers |= shortcutModAlt
		case "shift":
			modifiers |= shortcutModShift
		case "super", "cmd", "command":
			modifiers |= shortcutModSuper
		default:
			base = append(base, part)
		}
	}
	name := strings.Join(base, "-")
	if name == "" {
		return shortcutOverlayKeyPosition{}, false
	}
	position := func(code string) (shortcutOverlayKeyPosition, bool) {
		return shortcutOverlayKeyPosition{code: code, modifiers: modifiers}, true
	}
	switch name {
	case "esc", "escape":
		return position("esc")
	case "tab":
		return position("tab")
	case "backtab":
		return shortcutOverlayKeyPosition{code: "tab", modifiers: modifiers | shortcutModShift}, true
	case "enter", "return":
		return position("enter")
	case "space":
		return position(shortcutKeyboardCharCode(' '))
	case "backspace":
		return position("backspace")
	case "delete":
		return position("delete")
	case "insert":
		return position("insert")
	case "home":
		return position("home")
	case "end":
		return position("end")
	case "pageup", "pgup", "page-up":
		return position("pgup")
	case "pagedown", "pgdn", "page-down":
		return position("pgdn")
	case "up", "down", "left", "right":
		return position(name)
	}
	if number := strings.TrimPrefix(name, "f"); number != name {
		if value, err := strconv.Atoi(number); err == nil && value >= 1 && value <= 24 {
			return position("f" + strconv.Itoa(value))
		}
	}
	runes := []rune(name)
	if len(runes) == 1 {
		if mapped, ok := shortcutKeyboardUnshift(runes[0]); ok {
			return shortcutOverlayKeyPosition{code: shortcutKeyboardCharCode(mapped), modifiers: modifiers | shortcutModShift}, true
		}
		return position(shortcutKeyboardCharCode(runes[0]))
	}
	return shortcutOverlayKeyPosition{}, false
}

func shortcutKeyboardCharCode(ch rune) string {
	return "char:" + string(ch)
}

func shortcutKeyboardUnshift(ch rune) (rune, bool) {
	index := strings.IndexRune(shortcutKeyboardShifted, ch)
	if index < 0 {
		return 0, false
	}
	return []rune(shortcutKeyboardUnshifted)[index], true
}

// shortcutOverlayKeyPosition resolves one binding spec, following a `leader`
// chord to the prefix key it actually presses.
func (m *Model) shortcutOverlayKeyPosition(spec string) (shortcutOverlayKeyPosition, bool) {
	stroke := strings.TrimSpace(spec)
	if fields := strings.Fields(stroke); len(fields) > 0 {
		stroke = fields[0]
	}
	if stroke == "leader" {
		var config *codextui.KeymapConfig
		if m != nil {
			config = m.keymapConfig
		}
		if leaders, err := codextui.LeaderKeybindings(config); err == nil && len(leaders) > 0 {
			stroke = leaders[0]
		}
	}
	if stroke == "" {
		return shortcutOverlayKeyPosition{}, false
	}
	return shortcutOverlayKeyPositionFromStroke(stroke)
}

type shortcutKeyboardGeometry struct {
	roomy bool
}

func shortcutKeyboardGeometryForWidth(width int) shortcutKeyboardGeometry {
	return shortcutKeyboardGeometry{roomy: width >= shortcutKeyboardRoomyWidth}
}

func (g shortcutKeyboardGeometry) innerWidth() int {
	if g.roomy {
		return shortcutKeyboardRoomyWidth - 4
	}
	return shortcutKeyboardInnerWidth
}

func (g shortcutKeyboardGeometry) navStart() int {
	if g.roomy {
		return 69
	}
	return shortcutKeyboardNavStart
}

type shortcutKeyboardCap struct {
	code  string
	label string
	width int
}

func shortcutKeyboardKey(code string, label string) shortcutKeyboardCap {
	return shortcutKeyboardCap{code: code, label: label, width: utf8.RuneCountInString(label) + 2}
}

func shortcutKeyboardCapWidth(cap shortcutKeyboardCap, roomy bool) int {
	if !roomy {
		return cap.width
	}
	switch cap.code {
	case shortcutKeyboardCharCode(' '):
		return 12
	case "f1", "f2", "f3", "f4":
		return utf8.RuneCountInString(cap.label) + 2
	case "up", "down", "left", "right":
		return 6
	}
	return utf8.RuneCountInString(cap.label) + 3
}

// shortcutKeyboardCenter mirrors Rust's `{label:^width$}`: extra padding goes
// to the right when the difference is odd.
func shortcutKeyboardCenter(label string, width int) string {
	length := utf8.RuneCountInString(label)
	if length >= width {
		return label
	}
	left := (width - length) / 2
	return strings.Repeat(" ", left) + label + strings.Repeat(" ", width-length-left)
}

type shortcutKeyboardSpan struct {
	text  string
	style codextui.StyleSpec
}

func shortcutKeyboardStyled(text string, style codextui.StyleSpec, level codextui.StdoutColorLevel) string {
	if text == "" {
		return ""
	}
	sgr := codextui.SGRForStyleQuiet(style, level)
	if sgr == "" {
		return text
	}
	return sgr + text + "\x1b[0m"
}

// shortcutOverlayKeyboardStyles resolves Rust's keyboard_styles: a shaded panel,
// a border foreground readable on that panel, a keycap fill, and the selection
// fill that marks bound keys. A terminal without a usable palette falls back to
// the default colours with the selection style for bound keys.
func shortcutOverlayKeyboardStyles(level codextui.StdoutColorLevel) [4]codextui.StyleSpec {
	fallback := [4]codextui.StyleSpec{{}, {}, {}, codextui.SelectionStyleFor(nil, level)}
	background, backgroundKnown := codextui.DefaultBackgroundRGB()
	foreground, foregroundKnown := codextui.DefaultForegroundRGB()
	if !backgroundKnown || !foregroundKnown {
		return fallback
	}
	panelRGB := codextui.BlendRGB(foreground, background, 0.07)
	panelFill := codextui.BestColorForLevel(panelRGB, level)
	if panelFill.RGB == nil && panelFill.Index == nil {
		return fallback
	}
	var capFill, boundFill codextui.TerminalColor
	if panelFill.Index != nil {
		// Distinct xterm swatches keep palette reduction from merging the
		// surfaces (Rust's Ansi256 branch).
		panelTone := codextui.RGB{R: 48, G: 48, B: 48}
		alternateTone := codextui.RGB{R: 58, G: 58, B: 58}
		capTone := codextui.RGB{R: 78, G: 78, B: 78}
		boundTone := codextui.RGB{R: 0, G: 95, B: 135}
		if codextui.IsLight(background) {
			panelTone = codextui.RGB{R: 228, G: 228, B: 228}
			alternateTone = codextui.RGB{R: 218, G: 218, B: 218}
			capTone = codextui.RGB{R: 198, G: 198, B: 198}
			boundTone = codextui.RGB{R: 175, G: 215, B: 255}
		}
		panelFill = codextui.BestColorForLevel(panelTone, level)
		if codextui.SameTerminalColor(panelFill, codextui.BestColorForLevel(background, level)) {
			panelFill = codextui.BestColorForLevel(alternateTone, level)
		}
		capFill = codextui.BestColorForLevel(capTone, level)
		boundFill = codextui.BestColorForLevel(boundTone, level)
	} else {
		capFill = codextui.BestColorForLevel(codextui.BlendRGB(foreground, background, 0.22), level)
		boundFill = codextui.BestColorForLevel(codextui.BlendRGB(codextui.RGB{R: 35, G: 100, B: 140}, panelRGB, 0.63), level)
	}
	panel := codextui.StyleSpec{Background: panelFill}
	panelSurface, panelResolved := codextui.TerminalColorRGB(panelFill)
	if !panelResolved {
		return fallback
	}
	framePreferred := codextui.BlendRGB(foreground, background, 0.60)
	frame := codextui.StyleSpec{
		Foreground: codextui.ReadableColorOn(codextui.TerminalColor{RGB: &framePreferred}, &panelSurface, level),
		Background: panelFill,
	}
	capPreferred := codextui.TerminalColor{RGB: &foreground}
	capSurface, capResolved := codextui.TerminalColorRGB(capFill)
	if !capResolved {
		return fallback
	}
	capStyle := codextui.StyleSpec{
		Foreground: codextui.ReadableColorOn(capPreferred, &capSurface, level),
		Background: capFill,
	}
	bound := codextui.SelectionStyleFor(&background, level)
	if !codextui.SameTerminalColor(boundFill, panelFill) && !codextui.SameTerminalColor(boundFill, capFill) {
		if boundSurface, ok := codextui.TerminalColorRGB(boundFill); ok {
			preferred := codextui.RGB{R: 190, G: 228, B: 255}
			bound = codextui.StyleSpec{
				Foreground: codextui.ReadableColorOn(codextui.TerminalColor{RGB: &preferred}, &boundSurface, level),
				Background: boundFill,
				Bold:       true,
			}
		}
	}
	return [4]codextui.StyleSpec{panel, frame, capStyle, bound}
}

func shortcutKeyboardCapSpan(cap shortcutKeyboardCap, actions map[string]bool, styles [4]codextui.StyleSpec, roomy bool) shortcutKeyboardSpan {
	style := styles[2]
	if actions != nil {
		if actions[cap.code] {
			style = styles[3]
			// Painting a key removes it from the set, so whatever is left
			// still needs the list (Rust's `actions.remove` in `cap`).
			delete(actions, cap.code)
		}
	}
	width := shortcutKeyboardCapWidth(cap, roomy)
	return shortcutKeyboardSpan{text: shortcutKeyboardCenter(cap.label, width-1) + " ", style: style}
}

func shortcutKeyboardFramed(spans []shortcutKeyboardSpan, contentWidth int, geometry shortcutKeyboardGeometry, styles [4]codextui.StyleSpec, level codextui.StdoutColorLevel) string {
	var builder strings.Builder
	builder.WriteString(shortcutKeyboardStyled("│ ", styles[1], level))
	for _, span := range spans {
		builder.WriteString(shortcutKeyboardStyled(span.text, span.style, level))
	}
	builder.WriteString(shortcutKeyboardStyled(strings.Repeat(" ", max(geometry.innerWidth()-contentWidth, 0)), styles[0], level))
	builder.WriteString(shortcutKeyboardStyled(" │", styles[1], level))
	return builder.String()
}

// shortcutKeyboardLines paints the six-row tenkeyless diagram. Bound keys are
// removed from actions so the caller can tell whether the list still carries
// keys the diagram cannot show.
func shortcutKeyboardLines(actions map[string]bool, width int, level codextui.StdoutColorLevel) []string {
	geometry := shortcutKeyboardGeometryForWidth(width)
	styles := shortcutOverlayKeyboardStyles(level)
	rail := strings.Repeat("─", geometry.innerWidth()+2)
	lines := []string{shortcutKeyboardStyled("┌"+rail+"┐", styles[1], level)}
	for index := 0; index < shortcutKeyboardRows; index++ {
		row := []shortcutKeyboardSpan{}
		rowWidth := 0
		addCap := func(cap shortcutKeyboardCap) {
			span := shortcutKeyboardCapSpan(cap, actions, styles, geometry.roomy)
			row = append(row, span)
			rowWidth += utf8.RuneCountInString(span.text)
		}
		addText := func(text string) {
			row = append(row, shortcutKeyboardSpan{text: text, style: styles[0]})
			rowWidth += utf8.RuneCountInString(text)
		}
		left := []shortcutKeyboardCap{}
		letters := ""
		right := []shortcutKeyboardCap{}
		switch index {
		case 0:
			addCap(shortcutKeyboardKey("esc", "Esc"))
			for _, group := range []struct {
				first int
				gap   int
			}{{1, 3}, {5, 2}, {9, 1}} {
				if !geometry.roomy {
					addText(strings.Repeat(" ", group.gap))
				}
				for number := group.first; number < group.first+4; number++ {
					label := "F" + strconv.Itoa(number)
					addCap(shortcutKeyboardCap{code: "f" + strconv.Itoa(number), label: label, width: len(label) + 1})
				}
			}
		case 1:
			letters = "`1234567890-="
			right = []shortcutKeyboardCap{{code: "backspace", label: "Bksp", width: 9}}
		case 2:
			left = []shortcutKeyboardCap{{code: "tab", label: "Tab", width: 6}}
			letters = `qwertyuiop[]\`
		case 3:
			left = []shortcutKeyboardCap{{code: "caps", label: "Caps", width: 7}}
			letters = "asdfghjkl;'"
			right = []shortcutKeyboardCap{{code: "enter", label: "Enter", width: 8}}
		case 4:
			left = []shortcutKeyboardCap{{code: "shift", label: "Shift", width: 8}}
			letters = "zxcvbnm,./"
			right = []shortcutKeyboardCap{{code: "shift", label: "Shift", width: 10}}
		case 5:
			left = []shortcutKeyboardCap{
				{code: "ctrl", label: "Ctrl", width: 5},
				{code: "super", label: "Super", width: 6},
				shortcutKeyboardKey("alt", "Alt"),
				{code: shortcutKeyboardCharCode(' '), label: "Space", width: 11},
				shortcutKeyboardKey("alt", "Alt"),
				{code: "super", label: "Super", width: 6},
				{code: "menu", label: "Menu", width: 5},
				{code: "ctrl", label: "Ctrl", width: 5},
			}
		}
		for _, cap := range left {
			addCap(cap)
		}
		for _, letter := range letters {
			width := 3
			if letter == '\\' {
				width = 6
			}
			addCap(shortcutKeyboardCap{
				code:  shortcutKeyboardCharCode(letter),
				label: strings.ToUpper(string(letter)),
				width: width,
			})
		}
		for _, cap := range right {
			addCap(cap)
		}
		nav := []shortcutKeyboardCap{}
		switch index {
		case 0:
			nav = []shortcutKeyboardCap{{code: "prtsc", label: "PrtSc", width: 6}, {code: "scroll", label: "ScrLk", width: 6}, {code: "pause", label: "Pau", width: 4}}
		case 1:
			nav = []shortcutKeyboardCap{shortcutKeyboardKey("insert", "Ins"), shortcutKeyboardKey("home", "Home"), shortcutKeyboardKey("pgup", "PgU")}
		case 2:
			nav = []shortcutKeyboardCap{shortcutKeyboardKey("delete", "Del"), {code: "end", label: "End", width: 6}, shortcutKeyboardKey("pgdn", "PgD")}
		case 4:
			nav = []shortcutKeyboardCap{{code: "up", label: "↑", width: 6}}
		case 5:
			nav = []shortcutKeyboardCap{{code: "left", label: "←", width: 5}, {code: "down", label: "↓", width: 6}, {code: "right", label: "→", width: 5}}
		}
		if len(nav) > 0 {
			navStart := geometry.navStart()
			switch {
			case geometry.roomy && index == 0:
				navStart = 66
			case index == 4:
				navStart += 5
				if geometry.roomy {
					navStart++
				}
			}
			if gap := navStart - rowWidth; gap > 0 {
				addText(strings.Repeat(" ", gap))
			}
			for _, cap := range nav {
				addCap(cap)
			}
		}
		lines = append(lines, shortcutKeyboardFramed(row, rowWidth, geometry, styles, level))
		lines = append(lines, shortcutKeyboardFramed(nil, 0, geometry, styles, level))
		if index == 0 {
			lines = append(lines, shortcutKeyboardStyled("├"+rail+"┤", styles[1], level))
		}
	}
	lines = append(lines, shortcutKeyboardStyled("└"+rail+"┘", styles[1], level))
	return lines
}

// shortcutOverlayKeyboardFitsWidth mirrors Rust's `keyboard_fits_width`.
func shortcutOverlayKeyboardFitsWidth(width int) bool {
	return width >= shortcutKeyboardWidth
}

// shortcutOverlayGuideKey is the first free key the guide offers for switching
// views (Rust's `ShortcutGuideKeys::from_keymap`).
type shortcutOverlayGuideKey struct {
	spec  string
	label string
}

// shortcutOverlayConflictContexts are the contexts whose handlers can consume
// the next composer key (Rust's `context == Pager || context.overlaps(Composer)`).
var shortcutOverlayConflictContexts = map[string]bool{
	"pager":           true,
	"composer":        true,
	"global":          true,
	"chat":            true,
	"editor":          true,
	"vim_normal":      true,
	"vim_operator":    true,
	"vim_search":      true,
	"vim_text_object": true,
}

var shortcutOverlayGuideCandidates = []shortcutOverlayGuideKey{
	{spec: "tab", label: "Tab"},
	{spec: "f1", label: "F1"},
	{spec: "f2", label: "F2"},
	{spec: "f3", label: "F3"},
	{spec: "f4", label: "F4"},
	{spec: "f5", label: "F5"},
	{spec: "f6", label: "F6"},
	{spec: "f7", label: "F7"},
	{spec: "f8", label: "F8"},
	{spec: "f9", label: "F9"},
	{spec: "f10", label: "F10"},
	{spec: "f11", label: "F11"},
	{spec: "f12", label: "F12"},
}

// shortcutOverlayGuideKeys picks Tab or the first function key that no pager,
// composer, or main-surface action claims and that no chord uses as a prefix.
func (m *Model) shortcutOverlayGuideKeys() (shortcutOverlayGuideKey, bool) {
	var config *codextui.KeymapConfig
	if m != nil {
		config = m.keymapConfig
	}
	leaders, _ := codextui.LeaderKeybindings(config)
	leaderPrefix := ""
	if len(leaders) > 0 {
		leaderPrefix = leaders[0]
	}
	actions := codextui.KeymapActions(codextui.KeymapActionFilter{FastModeEnabled: true})
	for _, candidate := range shortcutOverlayGuideCandidates {
		conflict := false
		for _, action := range actions {
			if !shortcutOverlayConflictContexts[action.Context] {
				continue
			}
			// The guide opens on an empty draft, so queue has nothing to send.
			if action.Context == "composer" && action.Action == "queue" {
				continue
			}
			bindings, _, _ := codextui.ResolvedKeymapBindings(config, action.Context, action.Action)
			for _, binding := range bindings {
				fields := strings.Fields(binding)
				if len(fields) == 0 {
					continue
				}
				if fields[0] == candidate.spec {
					conflict = true
					break
				}
				if len(fields) > 1 {
					prefix := fields[0]
					if prefix == "leader" {
						prefix = leaderPrefix
					}
					if prefix == candidate.spec {
						conflict = true
						break
					}
				}
			}
			if conflict {
				break
			}
		}
		if !conflict {
			return candidate, true
		}
	}
	return shortcutOverlayGuideKey{}, false
}
