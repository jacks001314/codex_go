package tea

import (
	"runtime"

	bubbletea "github.com/charmbracelet/bubbletea"

	bottompane "codex_go/tui/bottom_pane"
)

// readClipboardTextForPaste reads clipboard text for an explicit paste
// shortcut. The default reuses the right-click paste reader so WSL goes
// through the framed helper process; tests replace it with a stub.
var readClipboardTextForPaste = func() (string, error) {
	return defaultClipboardRead()()
}

// readClipboardImageForPaste captures a clipboard image for an explicit paste
// shortcut. Tests replace it with a stub.
var readClipboardImageForPaste = pasteImageFromClipboard

// windowsShiftInsertCSI is Bubble Tea v1's undecoded Shift+Insert sequence
// (ESC[2;2~), mirroring handleCtrlInsertCopy's Ctrl+Insert mapping.
const windowsShiftInsertCSI = "?CSI[50 59 50 126]?"

// pasteClipboardShortcut mirrors Rust #53224's explicit paste shortcut: an
// image wins, text is read when the clipboard has no image, and clipboard
// access errors stay distinct from an image-less clipboard.
func (m *Model) pasteClipboardShortcut() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.clipboardBusy {
		m.notice = "Copy already in progress; try image paste after it finishes"
		return nil
	}
	// Rust #51411: legacy terminals deliver key auto-repeat as new presses, so a
	// repeated image-paste press inside the sliding window is ignored (and the
	// window refreshed) to avoid a burst of duplicate pastes during a slow paste.
	if m.noteImagePastePress(m.currentTime()) {
		return nil
	}
	if path, err := readClipboardImageForPaste(); err == nil {
		m.attachments = append(m.attachments, bottompane.ComposerAttachment{Kind: bottompane.AttachmentImage, Path: path})
		m.notice = "Attached image " + path
		return nil
	}
	text, err := readClipboardTextForPaste()
	if err != nil {
		m.notice = "Paste failed: " + err.Error()
		return nil
	}
	m.applyKeyboardPasteText(text)
	return nil
}

// applyKeyboardPasteText routes explicit paste text to the editable surface
// that owns input: the history search query, a composer selection, or the
// composer itself (Rust #53224).
func (m *Model) applyKeyboardPasteText(text string) {
	if m == nil || text == "" {
		return
	}
	if m.historySearch != nil && m.historySearch.Active && m.modal == nil {
		draft, _ := m.historySearch.AppendPastedQuery(text)
		m.showHistorySearchDraft(draft)
		m.clearComposerPasteWindow()
		return
	}
	if m.composerSelection.active {
		if m.replaceComposerSelection(bottompane.NormalizePastedText(text)) {
			m.extendComposerPasteWindow(m.currentTime())
			m.refreshSlashPopup()
		}
		return
	}
	m.insertComposerPaste(text)
	m.extendComposerPasteWindow(m.currentTime())
	m.refreshSlashPopup()
}

// handleShiftInsertPaste maps Shift+Insert (ESC[2;2~) to the explicit paste
// shortcut on Windows (Rust #53224).
func (m *Model) handleShiftInsertPaste(message bubbletea.Msg) (bool, bubbletea.Cmd) {
	if m == nil || runtime.GOOS != "windows" {
		return false, nil
	}
	stringer, ok := message.(interface{ String() string })
	if !ok || stringer.String() != windowsShiftInsertCSI {
		return false, nil
	}
	if m.modal != nil || m.overlay != nil {
		return true, nil
	}
	return true, m.pasteClipboardShortcut()
}
