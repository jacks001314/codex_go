package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust #62048 adds a US QWERTY tenkeyless diagram to the `?` shortcut guide.
// These tests mirror `shortcut_overlay_tests.rs` and the `shortcut_keyboard_*`
// snapshots: `Tab` (or the first free function key) switches views, the
// diagram highlights unmodified bindings, and narrow or short terminals fall
// back to the list without dropping the `/keymap customize` entry point.

func newShortcutKeyboardModel(t *testing.T, width int, height int, configure func(*codextui.KeymapConfig)) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{Width: width, Height: height})
	if configure != nil {
		config := codextui.NewKeymapConfig()
		configure(config)
		model.keymapConfig = config
	}
	model.Update(keyRunes('?'))
	if !model.shortcutOverlay {
		t.Fatal("? on an empty composer should open the shortcut guide")
	}
	return model
}

func plainShortcutOverlayLines(lines []string) []string {
	plain := make([]string, len(lines))
	for index, line := range lines {
		plain[index] = strings.TrimRight(ansi.Strip(line), " ")
	}
	return plain
}

func assertShortcutOverlayLines(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("line count = %d, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("line %d mismatch\n got: %q\nwant: %q", index, got[index], want[index])
		}
	}
}

func TestShortcutOverlayKeyboardMatchesRustBaseSnapshot(t *testing.T) {
	restore := codextui.SetDefaultTerminalColorsForTest(nil)
	defer restore()
	model := newShortcutKeyboardModel(t, 80, 40, nil)
	model.Update(key(keymsg.KeyTab))
	if !model.shortcutOverlayKeyboard {
		t.Fatal("Tab should switch the guide to the keyboard view")
	}
	rail := strings.Repeat("─", 70)
	blank := "│" + strings.Repeat(" ", 70) + "│"
	want := []string{
		"Keyboard shortcuts  Base  ·  Tab: List",
		"┌" + rail + "┐",
		"│ Esc     F1 F2 F3 F4   F5 F6 F7 F8  F9 F10 F11 F12   PrtSc ScrLk Pau  │",
		blank,
		"├" + rail + "┤",
		"│ `  1  2  3  4  5  6  7  8  9  0  -  =    Bksp       Ins  Home  PgU   │",
		blank,
		"│  Tab  Q  W  E  R  T  Y  U  I  O  P  [  ]    \\       Del   End  PgD   │",
		blank,
		"│  Caps  A  S  D  F  G  H  J  K  L  ;  '   Enter                       │",
		blank,
		"│  Shift  Z  X  C  V  B  N  M  ,  .  /    Shift              ↑         │",
		blank,
		"│ Ctrl Super Alt    Space    Alt  Super Menu Ctrl      ←     ↓    →    │",
		blank,
		"└" + rail + "┘",
		"/keymap customize",
	}
	assertShortcutOverlayLines(t, plainShortcutOverlayLines(model.shortcutOverlayLines(78, -1)), want)
}

func TestShortcutOverlayKeyboardMatchesRustRoomySnapshot(t *testing.T) {
	restore := codextui.SetDefaultTerminalColorsForTest(nil)
	defer restore()
	model := newShortcutKeyboardModel(t, 96, 40, nil)
	model.Update(key(keymsg.KeyTab))
	rail := strings.Repeat("─", 90)
	blank := "│" + strings.Repeat(" ", 90) + "│"
	want := []string{
		"Keyboard shortcuts  Base  ·  Tab: List",
		"┌" + rail + "┐",
		"│  Esc  F1  F2  F3  F4   F5   F6   F7   F8   F9   F10   F11   F12    PrtSc   ScrLk   Pau   │",
		blank,
		"├" + rail + "┤",
		"│  `   1   2   3   4   5   6   7   8   9   0   -   =   Bksp             Ins   Home   PgU   │",
		blank,
		"│  Tab   Q   W   E   R   T   Y   U   I   O   P   [   ]   \\              Del   End   PgD    │",
		blank,
		"│  Caps   A   S   D   F   G   H   J   K   L   ;   '   Enter                                │",
		blank,
		"│  Shift   Z   X   C   V   B   N   M   ,   .   /   Shift                       ↑           │",
		blank,
		"│  Ctrl   Super   Alt     Space     Alt   Super   Menu   Ctrl            ←     ↓     →     │",
		blank,
		"└" + rail + "┘",
		"/keymap customize",
	}
	assertShortcutOverlayLines(t, plainShortcutOverlayLines(model.shortcutOverlayLines(94, -1)), want)
}

// Mirrors Rust's `shortcut_overlay_leaves_remapped_tab_and_function_keys_available`:
// Tab, F1 (vim_search), and the default F2-F4 bindings are claimed, so the guide
// switches on F5.
func TestShortcutOverlayGuideKeySkipsClaimedKeysLikeRust(t *testing.T) {
	model := newShortcutKeyboardModel(t, 80, 40, func(config *codextui.KeymapConfig) {
		if err := config.Set("global", "open_transcript", []string{"tab"}); err != nil {
			t.Fatalf("Set(global.open_transcript): %v", err)
		}
		if err := config.Set("vim_search", "forward", []string{"f1"}); err != nil {
			t.Fatalf("Set(vim_search.forward): %v", err)
		}
		if err := config.Set("composer", "queue", nil); err != nil {
			t.Fatalf("Set(composer.queue): %v", err)
		}
	})
	guideKey, ok := model.shortcutOverlayGuideKeys()
	if !ok || guideKey.spec != "f5" {
		t.Fatalf("guide key = %+v ok=%v, want F5", guideKey, ok)
	}
	model.Update(key(keymsg.KeyTab))
	if model.shortcutOverlayKeyboard {
		t.Fatal("a remapped Tab must reach the composer instead of switching views")
	}
	// Any key that is not the switch key dismisses the guide, so reopening it
	// is part of the interaction.
	if model.overlay != nil {
		model.Update(key(keymsg.KeyEsc))
	}
	model.Update(keyRunes('?'))
	if !model.shortcutOverlay {
		t.Fatal("? should reopen the guide")
	}
	model.Update(key(keymsg.KeyF5))
	if !model.shortcutOverlayKeyboard {
		t.Fatal("F5 should switch the guide to the keyboard view")
	}
	model.Update(key(keymsg.KeyF5))
	if model.shortcutOverlayKeyboard {
		t.Fatal("F5 should switch the guide back to the list")
	}
}

func TestShortcutOverlayKeyboardFallsBackWhenNarrowLikeRust(t *testing.T) {
	model := newShortcutKeyboardModel(t, 70, 40, nil)
	model.Update(key(keymsg.KeyTab))
	if !model.shortcutOverlayKeyboard {
		t.Fatal("Tab should still set the keyboard view")
	}
	lines := plainShortcutOverlayLines(model.shortcutOverlayLines(68, -1))
	found := strings.Join(lines, "\n")
	if !strings.Contains(lines[0], "Keyboard unavailable") || !strings.Contains(lines[0], "Tab: List") {
		t.Fatalf("narrow heading should advertise the fallback: %q", lines[0])
	}
	if !strings.Contains(found, "Paste image") || !strings.Contains(found, "Change mode") {
		t.Fatalf("narrow terminals should keep the grouped list:\n%s", found)
	}
}

// Mirrors Rust's `shortcut_keyboard_keeps_diagram_for_keys_outside_us_layout`:
// a binding the diagram cannot paint leaves the "More keys in list" pointer and
// stays discoverable when the guide switches back.
func TestShortcutOverlayKeyboardKeepsOffDiagramKeysLikeRust(t *testing.T) {
	model := newShortcutKeyboardModel(t, 80, 40, func(config *codextui.KeymapConfig) {
		if err := config.Set("chat", "toggle_voice", []string{"f13"}); err != nil {
			t.Fatalf("Set(chat.toggle_voice): %v", err)
		}
	})
	model.Update(key(keymsg.KeyTab))
	lines := plainShortcutOverlayLines(model.shortcutOverlayLines(78, -1))
	if !strings.Contains(lines[0], "· More keys in list") {
		t.Fatalf("off-diagram binding should point at the list: %q", lines[0])
	}
	if strings.Contains(strings.Join(lines, "\n"), "F13") {
		t.Fatalf("the diagram should not paint a cap for f13:\n%s", strings.Join(lines, "\n"))
	}
	model.Update(key(keymsg.KeyTab))
	list := strings.Join(plainShortcutOverlayLines(model.shortcutOverlayLines(78, -1)), "\n")
	if !strings.Contains(list, "f13") || !strings.Contains(list, "Voice") {
		t.Fatalf("switching back should keep the off-diagram binding visible:\n%s", list)
	}
}

func TestShortcutOverlayKeyboardFallsBackWhenShortLikeRust(t *testing.T) {
	model := newShortcutKeyboardModel(t, 80, 40, nil)
	model.Update(key(keymsg.KeyTab))
	lines := plainShortcutOverlayLines(model.shortcutOverlayLines(78, 12))
	found := strings.Join(lines, "\n")
	if strings.Contains(found, "┌") {
		t.Fatalf("a 12-row budget should fall back to the list:\n%s", found)
	}
	if !strings.Contains(found, "Paste image") || !strings.Contains(found, "Change mode") {
		t.Fatalf("short terminals should keep the grouped list:\n%s", found)
	}
}

func TestShortcutOverlayKeyboardMarksBoundKeysLikeRust(t *testing.T) {
	restore := codextui.SetDefaultTerminalColorsForTest(nil)
	defer restore()
	model := newShortcutKeyboardModel(t, 80, 40, nil)
	model.Update(key(keymsg.KeyTab))
	lines := model.shortcutOverlayLines(78, -1)
	esc := ""
	for _, line := range lines {
		if strings.Contains(ansi.Strip(line), "│ Esc ") {
			esc = line
			break
		}
	}
	if esc == "" {
		t.Fatal("diagram should paint the Esc cap")
	}
	// Without a terminal palette Rust falls back to the selection style for
	// bound keys (bold + reversed) and leaves every other cap unstyled.
	if !strings.Contains(esc, "\x1b[1;7mEsc") {
		t.Fatalf("Esc should carry the selection fill: %q", esc)
	}
	if strings.Contains(esc, "\x1b[1;7mF1") {
		t.Fatalf("F1 has no binding in the guide and must stay unselected: %q", esc)
	}
}

func TestShortcutOverlayKeyboardResizeHintKeepsFooterLikeRust(t *testing.T) {
	model := newShortcutKeyboardModel(t, 80, 40, nil)
	model.Update(key(keymsg.KeyTab))
	model.height = model.shortcutOverlaySiblingRows() + 6
	rendered := plainShortcutOverlayLines(strings.Split(model.renderShortcutOverlay(), "\n"))
	if len(rendered) != 6 {
		t.Fatalf("clipped guide rows = %d, want 6:\n%s", len(rendered), strings.Join(rendered, "\n"))
	}
	if !strings.Contains(rendered[len(rendered)-2], "resize to see all") {
		t.Fatalf("clipped guide should hint at resizing: %q", rendered)
	}
	if !strings.Contains(rendered[len(rendered)-1], "/keymap customize") {
		t.Fatalf("clipped guide should keep the customize footer: %q", rendered)
	}
}
