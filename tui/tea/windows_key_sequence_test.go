//go:build windows

package tea

import (
	"runtime"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// windowsMappedShiftEnterMessage mirrors Bubble Tea's unexported
// unknownCSISequenceMsg for the sequence Windows Terminal emits for Shift+Enter.
type windowsMappedShiftEnterMessage struct{}

func (windowsMappedShiftEnterMessage) String() string {
	return "?CSI[49 51 59 50 117]?"
}

type windowsMappedUnknownMessage struct{}

func (windowsMappedUnknownMessage) String() string {
	return "?CSI[57 57 57 117]?"
}

func TestWindowsMappedShiftEnterInsertsNewline(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Terminal mapping only applies on Windows")
	}
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24, DisablePasteBurst: true, WindowsTerminalMappedInput: true})
	typeText(t, model, "first line")
	updated, _ := model.Update(windowsMappedShiftEnterMessage{})
	model = updated.(*Model)
	typeText(t, model, "second line")
	if got := model.composer.Value(); got != "first line\nsecond line" {
		t.Fatalf("composer = %q, want two lines", got)
	}
	if strings.Contains(model.View(), "first linesecond line") {
		t.Fatalf("Shift+Enter was swallowed:\n%s", model.View())
	}

	model = NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24, DisablePasteBurst: true, WindowsTerminalMappedInput: true})
	typeText(t, model, "first line")
	for _, key := range windowsShiftEnterKeys() {
		updated, _ := model.Update(key)
		model = updated.(*Model)
	}
	typeText(t, model, "second line")
	if got := model.composer.Value(); got != "first line\nsecond line" {
		t.Fatalf("single-key Windows sequence composer = %q", got)
	}
}

func TestWindowsMappedShiftEnterIgnoresOtherCSIAndModals(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Terminal mapping only applies on Windows")
	}
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24, DisablePasteBurst: true, WindowsTerminalMappedInput: true})
	typeText(t, model, "text")
	updated, _ := model.Update(windowsMappedUnknownMessage{})
	model = updated.(*Model)
	if got := model.composer.Value(); got != "text" {
		t.Fatalf("unknown CSI changed composer: %q", got)
	}

	// A mismatching prefix is replayed through normal key handling.
	for _, key := range []bubbletea.KeyMsg{
		{Type: bubbletea.KeyEsc},
		{Type: bubbletea.KeyRunes, Runes: []rune{'x'}},
	} {
		updated, _ = model.Update(key)
		model = updated.(*Model)
	}
	if got := model.composer.Value(); got != "textx" {
		t.Fatalf("mismatched sequence was not replayed: %q", got)
	}
	if len(model.windowsKeySequence) != 0 {
		t.Fatalf("mismatched sequence remained buffered: %#v", model.windowsKeySequence)
	}

	model.openSelectionViewModal(ModalKindGeneric, chatwidget.SelectionView{
		Title: "Choice",
		Items: []chatwidget.SelectionItem{{ID: "one", Name: "one"}},
	})
	updated, _ = model.Update(windowsMappedShiftEnterMessage{})
	model = updated.(*Model)
	if got := model.composer.Value(); got != "textx" {
		t.Fatalf("Shift+Enter bypassed modal and changed composer: %q", got)
	}
}

func TestWindowsMappedShiftEnterDoesNotDelayOtherTerminals(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Terminal mapping only applies on Windows")
	}
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24, DisablePasteBurst: true})
	model.escBacktrackHint = false
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	model = updated.(*Model)
	if len(model.windowsKeySequence) != 0 {
		t.Fatalf("non-Windows-Terminal Esc was buffered: %#v", model.windowsKeySequence)
	}
}

var _ bubbletea.Msg = windowsMappedShiftEnterMessage{}
