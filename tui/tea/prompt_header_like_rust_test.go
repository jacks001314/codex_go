package tea

import (
	"fmt"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/history_cell"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

// promptHeaderTestModel builds a transcript with two prompted turns whose
// answers overflow the overlay so the reader can sit inside an answer.
func promptHeaderTestModel(t *testing.T, width int, height int) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-prompt-header")
	for index := 0; index < 2; index++ {
		state.AddMessage(codextui.RoleUser, fmt.Sprintf("question-%d", index))
		for row := 0; row < 12; row++ {
			state.AddMessage(codextui.RoleAssistant, fmt.Sprintf("answer-%d row-%d", index, row))
		}
	}
	model := NewModel(state, Options{Width: width, Height: height})
	model.refreshTranscript()
	_ = model.viewString()
	return model
}

func openPromptHeaderOverlay(t *testing.T, model *Model) {
	t.Helper()
	model.Update(key(keymsg.KeyCtrlT))
	if model.overlay == nil || !model.overlayTranscript {
		t.Fatal("ctrl+t did not open the transcript overlay")
	}
}

// overlayContentRow returns the content row holding want.
func overlayContentRow(t *testing.T, model *Model, want string) int {
	t.Helper()
	for index, line := range strings.Split(model.overlay.Content(), "\n") {
		if strings.Contains(utils.StripANSI(line), want) {
			return index
		}
	}
	t.Fatalf("overlay content missing %q", want)
	return 0
}

// TestOverlayPinnedPromptHeaderTracksReadingLikeRust covers Rust
// transcript_view::prompt_header: reading inside an answer keeps that turn's
// prompt on the row above the content, a visible prompt supplies its own
// context, and the header never joins the content.
func TestOverlayPinnedPromptHeaderTracksReadingLikeRust(t *testing.T) {
	model := promptHeaderTestModel(t, 60, 12)
	openPromptHeaderOverlay(t, model)
	content := model.overlay.Content()

	model.overlay.SetYOffset(overlayContentRow(t, model, "answer-1 row-5"))
	view := utils.StripANSI(model.viewString())
	if header := utils.StripANSI(model.overlay.PinnedHeader()); !strings.Contains(header, "question-1") {
		t.Fatalf("pinned header = %q, want question-1\n%s", header, view)
	}
	if model.overlay.Content() != content {
		t.Fatal("pinned header changed the transcript content")
	}
	rows := strings.Split(view, "\n")
	if !strings.Contains(rows[1], "question-1") {
		t.Fatalf("header is not the row under the title: %q", rows[1])
	}
	if !strings.Contains(rows[2], "answer-1 row-5") {
		t.Fatalf("content did not start under the header: %q", rows[2])
	}

	model.overlay.SetYOffset(overlayContentRow(t, model, "question-1"))
	_ = model.viewString()
	if header := model.overlay.PinnedHeader(); header != "" {
		t.Fatalf("header = %q, want none while the prompt is visible", utils.StripANSI(header))
	}

	model.overlay.SetYOffset(overlayContentRow(t, model, "answer-0 row-3"))
	_ = model.viewString()
	if header := utils.StripANSI(model.overlay.PinnedHeader()); !strings.Contains(header, "question-0") {
		t.Fatalf("pinned header = %q, want question-0", header)
	}
}

// TestOverlayPinnedPromptHeaderYieldsLikeRust covers the gates in Rust
// prompt_header::line: no preceding prompt, a narrow view, and a body shorter
// than four rows leave the row to the content.
func TestOverlayPinnedPromptHeaderYieldsLikeRust(t *testing.T) {
	answersOnly := codextui.NewState(nil)
	answersOnly.SetThreadID("thread-answers-only")
	for row := 0; row < 20; row++ {
		answersOnly.AddMessage(codextui.RoleAssistant, fmt.Sprintf("answer row-%d", row))
	}
	model := NewModel(answersOnly, Options{Width: 60, Height: 12})
	model.refreshTranscript()
	_ = model.viewString()
	openPromptHeaderOverlay(t, model)
	model.overlay.SetYOffset(overlayContentRow(t, model, "answer row-5"))
	if header := model.overlay.PinnedHeader(); header != "" {
		t.Fatalf("header = %q, want none without a preceding prompt", utils.StripANSI(header))
	}

	narrow := promptHeaderTestModel(t, pinnedPromptHeaderMinWidth-1, 12)
	openPromptHeaderOverlay(t, narrow)
	narrow.overlay.SetYOffset(overlayContentRow(t, narrow, "answer-1 row-5"))
	if header := narrow.overlay.PinnedHeader(); header != "" {
		t.Fatalf("header = %q, want none in a narrow view", utils.StripANSI(header))
	}

	short := promptHeaderTestModel(t, 60, pinnedPromptHeaderMinBody+2)
	openPromptHeaderOverlay(t, short)
	short.overlay.SetYOffset(overlayContentRow(t, short, "answer-1 row-5"))
	if header := short.overlay.PinnedHeader(); header != "" {
		t.Fatalf("header = %q, want none on a short screen", utils.StripANSI(header))
	}
}

// TestPinnedPromptHeaderLineSanitizesAndTruncatesLikeRust covers the chat
// cell half of Rust prompt_header::line: control sequences never reach the
// terminal, whitespace collapses, and the row fits the display width.
func TestPinnedPromptHeaderLineSanitizesAndTruncatesLikeRust(t *testing.T) {
	line := historycell.PinnedPromptHeaderLine("\x1b[31m界 café\r\nnext question", false, 18)
	if strings.Contains(line, "\x1b[31m") {
		t.Fatalf("header kept a user CSI sequence: %q", line)
	}
	if width := codextui.DisplayWidth(strings.TrimRight(utils.StripANSI(line), " ")); width > 17 {
		t.Fatalf("header width = %d, want at most the one-cell margin", width)
	}
	plain := utils.StripANSI(line)
	if !strings.Contains(plain, "界 café next") || !strings.Contains(plain, "…") {
		t.Fatalf("header text = %q, want collapsed and truncated prompt text", plain)
	}
	if attachments := utils.StripANSI(historycell.PinnedPromptHeaderLine(" \t ", false, 20)); !strings.Contains(attachments, "[attachments]") {
		t.Fatalf("empty prompt header = %q, want [attachments]", attachments)
	}
}
