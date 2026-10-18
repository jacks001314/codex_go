package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/review"
	codextui "codex_go/tui"
	"codex_go/tui/bottom_pane"
)

func TestReviewPickerOpensDuringMCPStartupLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	state.SetStatus("idle")
	model := NewModel(state, Options{Width: 100, Height: 30})
	model.mcpStartupActive = true
	model.composer.SetValue("/review")

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})

	if model.modal == nil || model.modal.kind != ModalKindReview {
		t.Fatalf("review picker = %#v", model.modal)
	}
	if got := model.composerText(); got != "" {
		t.Fatalf("composer after accepted review = %q, want empty", got)
	}
}

func TestInlineReviewSubmitsDuringMCPStartupLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	state.SetStatus("idle")
	var started review.StartParams
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnStartReviewCommand: func(params review.StartParams) bubbletea.Cmd {
			started = params
			return nil
		},
	})
	model.mcpStartupActive = true
	model.composer.SetValue("/review check regressions")

	_, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	runTeaCmd(t, model, cmd)

	if started.ThreadID != "thread-review" || started.Target.Type != "custom" ||
		started.Target.Instructions != "check regressions" {
		t.Fatalf("start review params = %#v", started)
	}
	if got := model.composerText(); got != "" {
		t.Fatalf("composer after inline review = %q, want empty", got)
	}
}

func TestReviewBlockedDuringForegroundWorkPreservesDraftLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 100, Height: 30})
	draft := "/review check regressions"
	model.composer.SetValue(draft)
	model.attachments = []bottompane.ComposerAttachment{{
		Kind: bottompane.AttachmentRemoteImage,
		URL:  "https://example.com/image.png",
	}}

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})

	if got := model.composerText(); got != draft {
		t.Fatalf("composer after blocked review = %q, want %q", got, draft)
	}
	if len(model.attachments) != 1 || model.attachments[0].URL != "https://example.com/image.png" {
		t.Fatalf("attachments after blocked review = %#v", model.attachments)
	}
	if model.modal != nil || model.reviewState.IsReviewMode {
		t.Fatalf("blocked review opened UI: modal=%#v review=%v", model.modal, model.reviewState.IsReviewMode)
	}
	if model.notice != "'/review' is disabled while a task is in progress." {
		t.Fatalf("notice = %q", model.notice)
	}
	if view := model.View(); !strings.Contains(view, "'/review' is disabled while a task is in progress.") {
		t.Fatalf("blocked review error missing:\n%s", view)
	}
}

func TestReviewBlockedByQueuedFollowUpPreservesDraftLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 100, Height: 30})
	model.queued = append(model.queued, queuedSubmission{Request: SubmitRequest{Prompt: "queued follow-up"}})
	model.composer.SetValue("/review")

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})

	if got := model.composerText(); got != "/review" {
		t.Fatalf("composer after queued review block = %q", got)
	}
	if len(model.queued) != 1 {
		t.Fatalf("queued follow-up changed: %#v", model.queued)
	}
}
