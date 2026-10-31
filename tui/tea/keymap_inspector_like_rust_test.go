package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// openKeymapInspectorForTest drives the real /keymap flow: submit the command,
// switch to the Debug tab and confirm it, exactly like the Rust picker.
func openKeymapInspectorForTest(t *testing.T) *Model {
	t.Helper()
	model := NewModel(nil, Options{})
	typeText(t, model, "/keymap")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.keymapPanel == nil {
		t.Fatalf("/keymap modal = %#v", model.modal)
	}
	model.modal.keymapPanel.tab = len(model.keymapPanelTabs()) - 1
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.keymapInspector == nil {
		t.Fatalf("Debug tab Enter should open the inspector, modal = %#v", model.modal)
	}
	return model
}

func keymapInspectorReportAfter(t *testing.T, model *Model, message bubbletea.KeyMsg) *keymapInspectorReport {
	t.Helper()
	model.Update(message)
	if model.modal == nil || model.modal.keymapInspector == nil {
		t.Fatalf("inspector closed after %q", message.String())
	}
	report := model.modal.keymapInspector.report
	if report == nil {
		t.Fatalf("inspector recorded no report for %q", message.String())
	}
	return report
}

func keymapInspectorMatchSummaries(matches []codextui.KeymapActionMatch) []string {
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.Context+"."+match.Action+"|"+match.Source)
	}
	return out
}

func keymapInspectorRequireMatches(t *testing.T, label string, report *keymapInspectorReport, want []string) {
	t.Helper()
	got := keymapInspectorMatchSummaries(report.Matches)
	if len(got) != len(want) {
		t.Fatalf("%s matches = %#v, want %#v", label, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s matches = %#v, want %#v", label, got, want)
		}
	}
}

// TestKeymapInspectorKeypressReportsLikeRust locks the reports the Rust 0.166
// Keypress Inspector shows (out/kmdbg-{k,ctrlk,esc,A,left}-go.txt).
func TestKeymapInspectorKeypressReportsLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		message  bubbletea.KeyMsg
		detected string
		config   string
		raw      string
		matches  []string
	}{
		{
			name:     "lowercase k",
			message:  keyRunes('k'),
			detected: "k",
			config:   "k",
			raw:      "code=Char('k'), modifiers=none, kind=Press",
			matches: []string{
				"vim_normal.move_up|Default",
				"vim_operator.motion_up|Default",
				"pager.scroll_up|Default",
				"list.move_up|Default",
			},
		},
		{
			name:     "ctrl+k",
			message:  key(bubbletea.KeyCtrlK),
			detected: "ctrl+k",
			config:   "ctrl-k",
			raw:      "code=Char('k'), modifiers=ctrl, kind=Press",
			matches: []string{
				"editor.kill_line_end|Default",
				"list.move_up|Default",
			},
		},
		{
			name:     "esc is inspected",
			message:  key(bubbletea.KeyEsc),
			detected: "esc",
			config:   "esc",
			raw:      "code=Esc, modifiers=none, kind=Press",
			matches: []string{
				"chat.interrupt_turn|Default",
				"vim_normal.cancel_operator|Default",
				"vim_operator.cancel|Default",
				"vim_text_object.cancel|Default",
				"list.cancel|Default",
				"approval.decline|Default",
			},
		},
		{
			name:     "shift+a",
			message:  keyRunes('A'),
			detected: "shift+a",
			config:   "shift-a",
			raw:      "code=Char('A'), modifiers=shift, kind=Press",
			matches: []string{
				"vim_normal.append_line_end|Default",
			},
		},
		{
			name:     "left arrow",
			message:  key(bubbletea.KeyLeft),
			detected: "\u2190",
			config:   "left",
			raw:      "code=Left, modifiers=none, kind=Press",
			matches: []string{
				"editor.move_left|Default",
				"vim_normal.move_left|Default",
				"list.move_left|Default",
			},
		},
		{
			name:     "space",
			message:  key(bubbletea.KeySpace),
			detected: "space",
			config:   "space",
			raw:      "code=Char(' '), modifiers=none, kind=Press",
			matches: []string{
				"pager.page_down|Default",
			},
		},
		{
			name:     "f1",
			message:  key(bubbletea.KeyF1),
			detected: "f1",
			config:   "f1",
			raw:      "code=F(1), modifiers=none, kind=Press",
			matches:  nil,
		},
		{
			name:     "home",
			message:  key(bubbletea.KeyHome),
			detected: "home",
			config:   "home",
			raw:      "code=Home, modifiers=none, kind=Press",
			matches: []string{
				"editor.move_line_start|Default",
				"pager.jump_top|Default",
				"list.jump_top|Default",
			},
		},
		{
			name:     "page down",
			message:  key(bubbletea.KeyPgDown),
			detected: "pgdn",
			config:   "page-down",
			raw:      "code=PageDown, modifiers=none, kind=Press",
			matches: []string{
				"pager.page_down|Default",
				"list.page_down|Default",
			},
		},
		{
			name:     "backspace",
			message:  key(bubbletea.KeyBackspace),
			detected: "backspace",
			config:   "backspace",
			raw:      "code=Backspace, modifiers=none, kind=Press",
			matches: []string{
				"editor.delete_backward|Default",
				"agents.delete|Default",
			},
		},
		{
			name:     "tab",
			message:  key(bubbletea.KeyTab),
			detected: "tab",
			config:   "tab",
			raw:      "code=Tab, modifiers=none, kind=Press",
			matches: []string{
				"composer.queue|Default",
			},
		},
		{
			name:     "bare NUL normalizes to ctrl+space",
			message:  bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{0}}),
			detected: "ctrl+space",
			config:   "ctrl-space",
			raw:      "code=Char('\\u{0}'), modifiers=none, kind=Press",
			matches:  nil,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			model := openKeymapInspectorForTest(t)
			report := keymapInspectorReportAfter(t, model, testCase.message)
			if report.Detected != testCase.detected {
				t.Fatalf("Detected = %q, want %q", report.Detected, testCase.detected)
			}
			if report.ConfigKey != testCase.config {
				t.Fatalf("Config key = %q, want %q", report.ConfigKey, testCase.config)
			}
			if report.RawEvent != testCase.raw {
				t.Fatalf("Raw event = %q, want %q", report.RawEvent, testCase.raw)
			}
			keymapInspectorRequireMatches(t, testCase.name, report, testCase.matches)
		})
	}
}

// TestKeymapInspectorUnsupportedKeyLikeRust covers key_parts_to_config_key_spec
// rejecting Insert (out/kmdbgx-insert-go.txt): the inspector reports the raw
// event, renders the `tui.keymap` unsupported hint instead of a config key, and
// still lists the actions the key presses.
func TestKeymapInspectorUnsupportedKeyLikeRust(t *testing.T) {
	model := openKeymapInspectorForTest(t)
	model.width = 120
	report := keymapInspectorReportAfter(t, model, key(bubbletea.KeyInsert))
	if report.Detected != "insert" || report.ConfigKey != "" {
		t.Fatalf("Insert report = %q / %q", report.Detected, report.ConfigKey)
	}
	if report.RawEvent != "code=Insert, modifiers=none, kind=Press" {
		t.Fatalf("Insert raw event = %q", report.RawEvent)
	}
	keymapInspectorRequireMatches(t, "insert", report, []string{"vim_normal.enter_insert|Default"})
	want := strings.Join([]string{
		"Keypress Inspector",
		"Press any key to see what Codex receives. Esc is inspected; Ctrl+C closes.",
		"Tip: Codex can only inspect keys your terminal sends.",
		"",
		"Detected: insert",
		"Config key: unsupported - That key is not supported by `tui.keymap`.",
		"Raw event: code=Insert, modifiers=none, kind=Press",
		"",
		"Assigned actions:",
		"  - vim_normal.enter_insert (Enter Insert) - Enter insert mode at the cursor. [Default]",
	}, "\n")
	if view := model.renderKeymapInspector(); view != want {
		t.Fatalf("insert view = \n%s\nwant:\n%s", view, want)
	}

	f1 := openKeymapInspectorForTest(t)
	f1.width = 120
	report = keymapInspectorReportAfter(t, f1, key(bubbletea.KeyF1))
	keymapInspectorRequireMatches(t, "f1", report, nil)
	if view := f1.renderKeymapInspector(); !strings.HasSuffix(view, "Assigned actions:\n  none") {
		t.Fatalf("f1 view should render no matches:\n%s", view)
	}
}

// TestKeymapInspectorDelayedHintLikeRust matches the 120-column layout of
// out/kmdbg-open-go.txt: the short tip until MISSING_KEY_HINT_DELAY passes,
// then the wrapped "terminal is not sending that key" warning.
func TestKeymapInspectorDelayedHintLikeRust(t *testing.T) {
	model := openKeymapInspectorForTest(t)
	model.width = 120
	state := model.modal.keymapInspector

	state.openedAt = time.Now()
	view := model.renderKeymapInspector()
	if !strings.Contains(view, "Tip: Codex can only inspect keys your terminal sends.") {
		t.Fatalf("short hint missing:\n%s", view)
	}
	if strings.Contains(view, "Still waiting?") {
		t.Fatalf("delayed hint shown before the 3s delay:\n%s", view)
	}

	state.openedAt = time.Now().Add(-keymapInspectorHintDelay - time.Second)
	view = model.renderKeymapInspector()
	want := "Still waiting? If nothing changes when you press a key, your terminal is not sending that key to Codex. Only received\nkeys can be assigned as shortcuts."
	if !strings.Contains(view, want) {
		t.Fatalf("delayed hint = \n%s\nwant wrapped lines:\n%s", view, want)
	}

	model.Update(keymapInspectorTickMsg{})
	if model.modal == nil || model.modal.keymapInspector == nil {
		t.Fatalf("tick closed the inspector")
	}
}

// TestKeymapInspectorCtrlCClosesToDebugTabLikeRust checks that Ctrl+C returns
// to /keymap on the Debug tab, as out/kmdbg-close-go.txt shows.
func TestKeymapInspectorCtrlCClosesToDebugTabLikeRust(t *testing.T) {
	model := openKeymapInspectorForTest(t)
	model.Update(key(bubbletea.KeyCtrlC))
	if model.modal == nil || model.modal.id != chatwidget.KeymapPickerViewID || model.modal.keymapPanel == nil {
		t.Fatalf("Ctrl+C modal = %#v", model.modal)
	}
	if model.modal.keymapInspector != nil {
		t.Fatalf("Ctrl+C left the inspector attached")
	}
	want := len(model.keymapPanelTabs()) - 1
	if model.modal.keymapPanel.tab != want {
		t.Fatalf("returned tab = %d, want Debug tab %d", model.modal.keymapPanel.tab, want)
	}
}
