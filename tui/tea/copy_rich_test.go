package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

func TestCopyWholeResponseUsesRichClipboardWriter(t *testing.T) {
	state := codextui.NewState(nil)
	var richHTML string
	var richText string
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnClipboardWrite: func(text string) error {
			t.Fatalf("plain writer should not run for whole response")
			return nil
		},
		OnClipboardWriteRich: func(html string, text string) error {
			richHTML = html
			richText = text
			return nil
		},
	})
	markdown := "## Heading\n\n```go\npackage main\n```"
	model.copyTargets = chatwidget.CopyTargetsFromMarkdown(markdown)
	model.applyCopyTargetModalOption(chatwidget.CopyTargetWholeID)
	if !strings.Contains(richHTML, "<h2>Heading</h2>") || !strings.Contains(richHTML, "package main") {
		t.Fatalf("rich html = %q", richHTML)
	}
	if richText != markdown {
		t.Fatalf("rich plain text = %q", richText)
	}
}

func TestCopyCodeTargetStaysPlainText(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnClipboardWriteRich: func(html string, text string) error {
			t.Fatalf("rich writer should not run for code target")
			return nil
		},
	})
	model.copyTargets = chatwidget.CopyTargetsFromMarkdown("```go\npackage main\n```")
	var copied string
	model.clipboardWrite = func(text string) error {
		copied = text
		return nil
	}
	model.applyCopyTargetModalOption(model.copyTargets[1].ID)
	if copied != "package main\n" || strings.Contains(copied, "```") {
		t.Fatalf("copied code = %q", copied)
	}
}
