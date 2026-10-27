package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func renderedSelectionModel(t *testing.T, options Options) (*Model, *string) {
	t.Helper()
	var copied string
	options.Width = 100
	options.Height = 24
	options.OnClipboardWrite = func(text string) error {
		copied = text
		return nil
	}
	model := NewModel(codextui.NewState(&codextui.Options{Model: "gpt-5"}), options)
	_ = model.View()
	return model, &copied
}

func findDisplayColumn(line, needle string) int {
	index := strings.Index(line, needle)
	if index < 0 {
		return -1
	}
	return codextui.DisplayWidth(line[:index])
}

func dragFooterSelection(t *testing.T, model *Model, from, to string) bubbletea.Cmd {
	t.Helper()
	region := model.renderedSelection.regions[renderedRegionFooter]
	if region.w <= 0 {
		t.Fatalf("footer region not registered: %#v", region)
	}
	line := region.text[0]
	start := findDisplayColumn(line, from)
	end := findDisplayColumn(line, to)
	if start < 0 || end < 0 {
		t.Fatalf("selection text not found in footer %q", line)
	}
	y := region.y
	_, _ = model.Update(bubbletea.MouseMsg{X: start, Y: y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft})
	_, cmd := model.Update(bubbletea.MouseMsg{X: end, Y: y, Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft})
	return cmd
}

// Rust #52270: footer text can be selected and copied without touching the
// composer draft.
func TestRenderedFooterSelectionCopiesPlainTextLikeRust(t *testing.T) {
	model, copied := renderedSelectionModel(t, Options{})
	model.composer.SetValue("draft stays")
	_ = model.View()

	cmd := dragFooterSelection(t, model, "enter send", "newline")
	runTeaCmd(t, model, cmd)
	if view := model.View(); !strings.Contains(view, "\x1b[7m") {
		t.Fatalf("footer selection was not highlighted:\n%s", utils.StripANSI(view))
	}
	if !strings.HasPrefix(*copied, "enter send") || !strings.Contains(*copied, "ctrl") {
		t.Fatalf("copied footer text = %q", *copied)
	}
	if got := model.composer.Value(); got != "draft stays" {
		t.Fatalf("composer draft = %q, want unchanged", got)
	}
}

// Rust #52270: Unicode text and terminal hyperlink escapes survive selection
// rendering; plain-text copying keeps the visible characters.
func TestRenderedSelectionPreservesUnicodeAndHyperlinksLikeRust(t *testing.T) {
	line := "\x1b]8;;https://example.com\x07café\x1b]8;;\x07世界"
	highlighted := highlightDisplayCells(line, 0, 7)
	if !strings.Contains(highlighted, "\x1b]8;;https://example.com\x07") {
		t.Fatalf("selection dropped the OSC 8 hyperlink: %q", highlighted)
	}
	if got := sliceDisplayCells(utils.StripANSI(line), 0, 6); got != "café世" {
		t.Fatalf("selected Unicode = %q", got)
	}
}

// Rust #52270: a changed footer invalidates the captured region.
func TestRenderedSelectionInvalidatesChangedRegionLikeRust(t *testing.T) {
	model, _ := renderedSelectionModel(t, Options{})
	region := model.renderedSelection.regions[renderedRegionFooter]
	model.Update(bubbletea.MouseMsg{X: 0, Y: region.y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft})
	if !model.renderedSelection.active {
		t.Fatal("selection did not start")
	}
	// Copy/status notices may appear above the footer without relocating the
	// selected cells; a real footer-line change still invalidates them.
	model.escBacktrackHint = true
	_ = model.View()
	if model.renderedSelection.active {
		t.Fatal("selection survived a footer change")
	}
}

// Rust #52270: copy_on_select=never keeps the selection for the existing
// Ctrl+C copy control.
func TestRenderedSelectionKeyboardCopyLikeRust(t *testing.T) {
	model, copied := renderedSelectionModel(t, Options{CopyOnSelect: "never"})
	cmd := dragFooterSelection(t, model, "enter send", "ctrl+j")
	if cmd != nil {
		t.Fatal("copy_on_select=never copied on release")
	}
	if !model.renderedSelection.active {
		t.Fatal("copy_on_select=never dropped the selection")
	}
	_, copyCmd := model.Update(key(bubbletea.KeyCtrlC))
	runTeaCmd(t, model, copyCmd)
	if !strings.Contains(*copied, "enter send") {
		t.Fatalf("Ctrl+C copied %q", *copied)
	}
	if model.renderedSelection.active {
		t.Fatal("Ctrl+C did not clear the selection")
	}
}

func TestRenderedSelectionStationaryClickAndInlineModeLikeRust(t *testing.T) {
	model, copied := renderedSelectionModel(t, Options{})
	region := model.renderedSelection.regions[renderedRegionFooter]
	_, cmd := model.Update(bubbletea.MouseMsg{X: 0, Y: region.y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft})
	_, cmd = model.Update(bubbletea.MouseMsg{X: 0, Y: region.y, Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft})
	if cmd != nil || *copied != "" {
		t.Fatalf("stationary click copied %q", *copied)
	}
	if model.renderedSelection.active {
		t.Fatal("stationary click left a selection active")
	}

	inlineOff := false
	inline, inlineCopied := renderedSelectionModel(t, Options{FullscreenTranscript: &inlineOff})
	if inline.renderedSelection.regions[renderedRegionFooter].w != 0 {
		t.Fatal("inline mode registered an app-owned footer selection")
	}
	_ = inlineCopied
}

// Rust rendered_selection::handle_mouse clears the painted selection on
// ScrollUp/ScrollDown and returns None, so the wheel keeps scrolling the
// transcript instead of being swallowed by the stale highlight.
func TestRenderedSelectionWheelClearsAndScrollsLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	for i := 0; i < 40; i++ {
		state.AddMessage(codextui.RoleSystem, "event\nmore detail")
	}
	model := NewModel(state, Options{Width: 100, Height: 12, CopyOnSelect: "never"})
	_ = model.View()
	region := model.renderedSelection.regions[renderedRegionFooter]
	if region.w <= 0 {
		t.Fatalf("footer region not registered: %#v", region)
	}
	line := region.text[0]
	start := findDisplayColumn(line, "enter send")
	end := findDisplayColumn(line, "newline")
	if start < 0 || end < 0 {
		t.Fatalf("footer text not found in %q", line)
	}
	_, _ = model.Update(bubbletea.MouseMsg{X: start, Y: region.y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft})
	_, _ = model.Update(bubbletea.MouseMsg{X: end, Y: region.y, Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft})
	if !model.renderedSelection.active {
		t.Fatal("footer drag did not leave a selection active")
	}

	before := model.TranscriptYOffset()
	_, _ = model.Update(bubbletea.MouseMsg{Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonWheelUp})
	if model.renderedSelection.active {
		t.Fatal("wheel left the rendered selection active")
	}
	if got, want := model.TranscriptYOffset(), before-1; got != want {
		t.Fatalf("wheel transcript offset = %d, want %d", got, want)
	}
}

func TestDefaultTranscriptOwnershipMatchesRust(t *testing.T) {
	fullscreenOn := true
	fullscreenOff := false
	cases := []struct {
		name       string
		options    Options
		ownsScreen bool
	}{
		{name: "default owns the screen", options: Options{}, ownsScreen: true},
		{name: "auto owns the screen", options: Options{AlternateScreenMode: "auto"}, ownsScreen: true},
		{name: "always", options: Options{AlternateScreenMode: "always"}, ownsScreen: true},
		{name: "never stays inline", options: Options{AlternateScreenMode: "never"}},
		{name: "fullscreen transcript disabled", options: Options{AlternateScreenMode: "auto", FullscreenTranscript: &fullscreenOff}},
		{name: "fullscreen transcript enabled", options: Options{AlternateScreenMode: "auto", FullscreenTranscript: &fullscreenOn}, ownsScreen: true},
		{name: "no-alt-screen wins", options: Options{AlternateScreenMode: "always", NoAltScreen: true}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.options.usesOwnedScreen(); got != testCase.ownsScreen {
				t.Fatalf("usesOwnedScreen = %v, want %v", got, testCase.ownsScreen)
			}
		})
	}
}
