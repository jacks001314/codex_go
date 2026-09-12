package tea

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
)

// TranscriptExportFunc renders the active conversation as Markdown for /export
// (Rust transcript_export.rs). A nil hook leaves /export unavailable.
type TranscriptExportFunc func(threadID string) (string, error)

const transcriptExportFailedPrefix = "Export failed: "

// applyExportCommand implements Rust's /export dispatch: `/export <path>` writes
// directly, while a bare `/export` opens the destination picker.
func (m *Model) applyExportCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if destination := strings.TrimSpace(args); destination != "" {
		return m.runTranscriptExport(destination)
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewTranscriptExportView())
	return nil
}

// applyTranscriptExportModalOption runs the destination chosen in the /export
// picker.
func (m *Model) applyTranscriptExportModalOption(optionID string) bubbletea.Cmd {
	switch strings.TrimSpace(optionID) {
	case chatwidget.TranscriptExportOptionClipboard:
		return m.runTranscriptExport("")
	case chatwidget.TranscriptExportOptionFile:
		m.openTranscriptExportFilePrompt()
		return nil
	default:
		return nil
	}
}

// openTranscriptExportFilePrompt asks for the export filename, defaulting to
// codex-session-<thread>.md (Rust show_transcript_export_file_prompt).
func (m *Model) openTranscriptExportFilePrompt() {
	if m == nil {
		return
	}
	filename := "codex-session.md"
	if threadID := strings.TrimSpace(m.currentThreadID()); threadID != "" {
		filename = "codex-session-" + threadID + ".md"
	}
	prompt := bottompane.NewCustomPromptView("Save conversation", "Type a filename and press Enter", filename, "")
	prompt.SetVimEnabled(m.vimMode)
	m.modal = &modalState{
		id:           chatwidget.TranscriptExportPickerViewID + "-file",
		kind:         ModalKindGeneric,
		customPrompt: prompt,
		customPromptSubmit: func(name string) bubbletea.Cmd {
			return m.runTranscriptExport(name)
		},
	}
	m.notice = ""
}

// runTranscriptExport renders the transcript and delivers it to the clipboard
// (empty destination) or a new file (Rust export_transcript).
func (m *Model) runTranscriptExport(destination string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onExportTranscript == nil {
		m.reportTranscriptExportError(errors.New("export is unavailable"))
		return nil
	}
	markdown, err := m.onExportTranscript(m.currentThreadID())
	if err != nil {
		m.reportTranscriptExportError(err)
		return nil
	}
	if strings.TrimSpace(destination) == "" {
		if m.clipboardWrite == nil {
			m.reportTranscriptExportError(errors.New("clipboard is unavailable"))
			return nil
		}
		if err := m.clipboardWrite(markdown); err != nil {
			m.reportTranscriptExportError(err)
			return nil
		}
		m.addInfoHistoryMessage("Copied conversation to clipboard")
		m.refreshTranscript()
		return nil
	}
	written, err := writeTranscriptFile(m.exportBaseDir(), destination, markdown)
	if err != nil {
		m.reportTranscriptExportError(err)
		return nil
	}
	m.addInfoHistoryMessage("Saved conversation to " + written)
	m.refreshTranscript()
	return nil
}

func (m *Model) reportTranscriptExportError(err error) {
	if m == nil || err == nil {
		return
	}
	message := transcriptExportFailedPrefix + err.Error()
	m.notice = message
	m.addErrorHistoryMessage(message)
	m.refreshTranscript()
}

// exportBaseDir resolves a relative export path against the session cwd, like
// Rust's export_transcript cwd choice.
func (m *Model) exportBaseDir() string {
	if m == nil {
		return ""
	}
	if cwd := strings.TrimSpace(m.sessionCWD); cwd != "" {
		return cwd
	}
	if m.State != nil {
		return strings.TrimSpace(m.State.CWD)
	}
	return ""
}

// writeTranscriptFile mirrors Rust write_transcript: `~` expands to the home
// directory, relative paths resolve against the session cwd, and the destination
// is created exclusively so an existing conversation is never clobbered.
func writeTranscriptFile(baseDir string, requested string, markdown string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", errors.New("a filename is required")
	}
	path := requested
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("could not determine the home directory")
		}
		rest := strings.TrimPrefix(path, "~")
		rest = strings.TrimPrefix(rest, string(filepath.Separator))
		rest = strings.TrimPrefix(rest, "/")
		path = filepath.Join(home, rest)
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("could not create %s: %w", path, err)
	}
	if _, err := file.WriteString(markdown); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("could not write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("could not write %s: %w", path, err)
	}
	return path, nil
}
