package tea

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

type csiSequenceMsg string

func (m csiSequenceMsg) String() string { return string(m) }

func stubClipboardShortcut(t *testing.T, text string, readErr error) {
	t.Helper()
	previousText, previousImage := readClipboardTextForPaste, readClipboardImageForPaste
	readClipboardTextForPaste = func() (string, error) { return text, readErr }
	readClipboardImageForPaste = func() (string, error) { return "", errors.New("no image on the clipboard") }
	t.Cleanup(func() {
		readClipboardTextForPaste, readClipboardImageForPaste = previousText, previousImage
	})
}

func clipboardShortcutModel(t *testing.T) *Model {
	t.Helper()
	return NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
}

// Rust #53224: Ctrl+V falls back to clipboard text when the clipboard holds no
// image, including multiline Unicode text.
func TestCtrlVPastesClipboardTextLikeRust(t *testing.T) {
	stubClipboardShortcut(t, "hello\nwörld", nil)
	model := clipboardShortcutModel(t)

	model.Update(key(bubbletea.KeyCtrlV))

	if got := model.composerText(); got != "hello\nwörld" {
		t.Fatalf("composer after Ctrl+V = %q", got)
	}
	if strings.Contains(model.notice, "Paste failed") {
		t.Fatalf("text fallback reported a paste failure: %q", model.notice)
	}
}

// Rust #53224: explicit paste fills an active composer selection.
func TestCtrlVPasteReplacesComposerSelectionLikeRust(t *testing.T) {
	stubClipboardShortcut(t, "gamma", nil)
	model := clipboardShortcutModel(t)
	model.composer.SetValue("alpha beta")
	model.composerSelection = composerTextSelection{active: true, anchorRune: 0, focusRune: 5}

	model.Update(key(bubbletea.KeyCtrlV))

	if got := model.composerText(); got != "gamma beta" {
		t.Fatalf("composer after pasting into selection = %q", got)
	}
	if model.composerSelection.active {
		t.Fatal("paste left the composer selection active")
	}
}

// Rust #53224: explicit paste feeds the active history search query.
func TestCtrlVPasteFeedsHistorySearchLikeRust(t *testing.T) {
	stubClipboardShortcut(t, "git", nil)
	model := clipboardShortcutModel(t)
	model.inputHistory = []string{"git status", "git log"}
	model.Update(key(bubbletea.KeyCtrlR))
	if model.historySearch == nil || !model.historySearch.Active {
		t.Fatalf("Ctrl+R did not open history search: %#v", model.historySearch)
	}

	model.Update(key(bubbletea.KeyCtrlV))

	if model.historySearch == nil || model.historySearch.Query != "git" {
		t.Fatalf("history search query after Ctrl+V = %#v", model.historySearch)
	}
}

// Rust #53224: on Windows, Shift+Insert shares the explicit paste shortcut.
func TestWindowsShiftInsertPastesLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Shift+Insert paste shortcut is Windows-only")
	}
	stubClipboardShortcut(t, "shift insert", nil)
	model := clipboardShortcutModel(t)

	model.Update(csiSequenceMsg(windowsShiftInsertCSI))

	if got := model.composerText(); got != "shift insert" {
		t.Fatalf("composer after Shift+Insert = %q", got)
	}
}

// Rust #53224: clipboard access errors stay distinct from an empty clipboard.
func TestCtrlVPasteFailureNoticeLikeRust(t *testing.T) {
	stubClipboardShortcut(t, "", errors.New("clipboard unavailable"))
	model := clipboardShortcutModel(t)

	model.Update(key(bubbletea.KeyCtrlV))

	if model.notice != "Paste failed: clipboard unavailable" {
		t.Fatalf("paste failure notice = %q", model.notice)
	}
}
