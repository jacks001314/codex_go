package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

func exportTestModel(t *testing.T, markdown string, clipboard func(string) error) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	options := Options{
		Width:             80,
		Height:            24,
		DisablePasteBurst: true,
		OnExportTranscript: func(threadID string) (string, error) {
			if threadID != "thread-1" {
				t.Fatalf("export threadID = %q, want thread-1", threadID)
			}
			return markdown, nil
		},
	}
	if clipboard != nil {
		options.OnClipboardWrite = clipboard
	}
	return NewModel(state, options)
}

func TestModelExportCommandWritesMarkdownFile(t *testing.T) {
	dir := t.TempDir()
	model := exportTestModel(t, "# Codex conversation\n\n## User\n\nhello\n", nil)
	model.sessionCWD = dir

	typeText(t, model, "/export out.md")
	model.Update(key(bubbletea.KeyEnter))

	data, err := os.ReadFile(filepath.Join(dir, "out.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v (notice=%q)", err, model.notice)
	}
	if string(data) != "# Codex conversation\n\n## User\n\nhello\n" {
		t.Fatalf("exported markdown = %q", string(data))
	}
	if !strings.Contains(modelMessageText(model), "Saved conversation to "+filepath.Join(dir, "out.md")) {
		t.Fatalf("missing saved notice:\n%s", modelMessageText(model))
	}
}

func TestModelExportCommandExistingFileFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.md"), []byte("keep me"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	model := exportTestModel(t, "# Codex conversation\n\n## User\n\nhello\n", nil)
	model.sessionCWD = dir

	typeText(t, model, "/export out.md")
	model.Update(key(bubbletea.KeyEnter))

	if !strings.Contains(model.notice, "Export failed:") {
		t.Fatalf("notice = %q, want an export failure", model.notice)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.md"))
	if err != nil || string(data) != "keep me" {
		t.Fatalf("existing file was modified: %q err=%v", string(data), err)
	}
}

func TestModelExportCommandWithoutArgsOpensPicker(t *testing.T) {
	model := exportTestModel(t, "# Codex conversation\n\n## User\n\nhello\n", nil)

	typeText(t, model, "/export")
	model.Update(key(bubbletea.KeyEnter))

	if model.modal == nil || model.modal.id != chatwidget.TranscriptExportPickerViewID {
		t.Fatalf("modal = %#v, want the export picker", model.modal)
	}
	if len(model.modal.options) != 2 {
		t.Fatalf("picker options = %#v", model.modal.options)
	}
}

func TestModelExportClipboardOptionCopiesMarkdown(t *testing.T) {
	var copied string
	model := exportTestModel(t, "# Codex conversation\n\n## User\n\nhello\n", func(text string) error {
		copied = text
		return nil
	})

	if cmd := model.applyTranscriptExportModalOption(chatwidget.TranscriptExportOptionClipboard); cmd != nil {
		t.Fatal("clipboard export should not return a command")
	}
	if copied != "# Codex conversation\n\n## User\n\nhello\n" {
		t.Fatalf("clipboard = %q", copied)
	}
	if !strings.Contains(modelMessageText(model), "Copied conversation to clipboard") {
		t.Fatalf("missing copied notice:\n%s", modelMessageText(model))
	}
}

func TestModelExportUnavailableWithoutCallback(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24, DisablePasteBurst: true})
	model.sessionCWD = t.TempDir()
	typeText(t, model, "/export out.md")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.notice, "Export failed:") {
		t.Fatalf("notice = %q, want an export failure", model.notice)
	}
}
