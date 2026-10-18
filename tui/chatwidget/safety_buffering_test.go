package chatwidget

import (
	"strings"
	"testing"
)

func TestSafetyBufferingContextGatesAndDismissPromptMatchRust(t *testing.T) {
	state := SafetyBufferingState{}
	state.RecordTurn("turn-1")

	ignoredReplay := state.ApplyWithContext(SafetyBufferingUpdate{
		TurnID:          "turn-1",
		ShowBufferingUI: true,
		FasterModel:     "fast",
		CanRetry:        true,
	}, SafetyBufferingContext{Replay: ReplayResumeInitialMessages, AgentTurnRunning: true, LastTurnID: "turn-1", ThreadID: "thread-1", EnforceTurn: true})
	if !ignoredReplay.Ignored || state.ActiveTurnID != "" {
		t.Fatalf("replay ignored=%#v state=%#v", ignoredReplay, state)
	}

	wrongTurn := state.ApplyWithContext(SafetyBufferingUpdate{
		TurnID:          "turn-2",
		ShowBufferingUI: true,
	}, SafetyBufferingContext{AgentTurnRunning: true, LastTurnID: "turn-1", EnforceTurn: true})
	if !wrongTurn.Ignored {
		t.Fatalf("wrong turn result = %#v", wrongTurn)
	}

	noRetry := state.ApplyWithContext(SafetyBufferingUpdate{
		TurnID:          "turn-1",
		ShowBufferingUI: true,
		FasterModel:     "fast",
		CanRetry:        true,
	}, SafetyBufferingContext{AgentTurnRunning: true, LastTurnID: "turn-1", EnforceTurn: true})
	if noRetry.Prompt == nil || noRetry.RetryAvailable || noRetry.Prompt.Title != SafetyBufferingHeader || len(noRetry.Prompt.Items) != 2 {
		t.Fatalf("no-thread retry result = %#v", noRetry)
	}

	withRetry := state.ApplyWithContext(SafetyBufferingUpdate{
		TurnID:          "turn-1",
		ShowBufferingUI: true,
		FasterModel:     "fast",
		CanRetry:        true,
	}, SafetyBufferingContext{AgentTurnRunning: true, LastTurnID: "turn-1", ThreadID: "thread-1", EnforceTurn: true})
	if !withRetry.RetryAvailable || withRetry.Prompt == nil || withRetry.Prompt.Subtitle != SafetyBufferingMessageWithRetry {
		t.Fatalf("with retry result = %#v", withRetry)
	}
	if withRetry.Status.Header != "Working" ||
		!strings.Contains(withRetry.Status.Details, SafetyBufferingHeader) ||
		withRetry.Status.DetailsMaxLines != 6 {
		t.Fatalf("status = %#v", withRetry.Status)
	}
}

func TestSafetyBufferingResetPrepareAndFailRetryMatchRust(t *testing.T) {
	state := SafetyBufferingState{ActiveTurnID: "turn-1", RetryPromptShown: true, AgentMessageStarted: true}

	reset := state.ResetForTurnStart()
	if !reset.DismissPrompt || !reset.Cleared || state.ActiveTurnID != "" || state.RetryPromptShown || state.RetryPromptHadRetry || state.AgentMessageStarted {
		t.Fatalf("reset=%#v state=%#v", reset, state)
	}

	queue := InputQueueState{}
	prepared := state.PrepareRetry(&queue)
	if !prepared.Prepared || !prepared.FinalizeTurn || !prepared.ClearLastRenderedUserText || !queue.UserTurnPendingStart {
		t.Fatalf("prepared=%#v queue=%#v", prepared, queue)
	}

	cancel := CancelEditState{}
	cancel.RecordCancelEditCandidate(NewUserMessage("try again"))
	state.ActiveTurnID = "turn-1"
	failed := state.FailRetry(&queue, &cancel)
	if !failed.Failed || failed.UserTurnPendingStart || queue.UserTurnPendingStart {
		t.Fatalf("failed=%#v queue=%#v", failed, queue)
	}
	if failed.RestoredPrompt == nil || failed.RestoredPrompt.Text != "try again" {
		t.Fatalf("restored prompt = %#v", failed.RestoredPrompt)
	}
	if state.ActiveTurnID != "" || cancel.Prompt != nil || cancel.Eligible {
		t.Fatalf("state/cancel not cleared: state=%#v cancel=%#v", state, cancel)
	}
}

func TestSafetyBufferingConfirmationViewMatchRust(t *testing.T) {
	view := NewSafetyBufferingConfirmationView("Faster Model")
	if view.ViewID != SafetyBufferingPromptViewID || !strings.Contains(view.Title, "Stop this attempt and retry?") {
		t.Fatalf("confirmation view = %#v", view)
	}
	if len(view.Items) != 2 || view.Items[0].Action != SafetyBufferingActionWait || view.Items[1].Action != SafetyBufferingActionStopRetry {
		t.Fatalf("confirmation items = %#v", view.Items)
	}
	if !strings.Contains(view.Subtitle, "using Faster Model") || strings.Contains(view.Subtitle, "gpt-fast") {
		t.Fatalf("confirmation subtitle = %q", view.Subtitle)
	}
}

func TestSafetyBufferingPromptCopyAndNoRetryViewMatchRust(t *testing.T) {
	retry := NewSafetyBufferingPromptView("Faster Model", true)
	if retry.Title != SafetyBufferingHeader ||
		retry.Subtitle != SafetyBufferingMessageWithRetry ||
		retry.FooterNote != SafetyBufferingFooter {
		t.Fatalf("retry prompt = %#v", retry)
	}
	if len(retry.Items) != 3 ||
		retry.Items[0].Name != "Retry with a faster model" ||
		retry.Items[1].Name != "Dismiss and keep waiting" ||
		retry.Items[2].Name != "Learn more" {
		t.Fatalf("retry items = %#v", retry.Items)
	}

	noRetry := NewSafetyBufferingPromptView("", false)
	if noRetry.Title != SafetyBufferingHeader || noRetry.Subtitle != "" || noRetry.FooterNote != SafetyBufferingFooter {
		t.Fatalf("no-retry prompt = %#v", noRetry)
	}
	if len(noRetry.Items) != 2 ||
		noRetry.Items[0].Name != "Dismiss and keep waiting" ||
		noRetry.Items[1].Name != "Learn more" {
		t.Fatalf("no-retry items = %#v", noRetry.Items)
	}
}

func TestSafetyBufferingIgnoresUpdatesAfterAgentMessageStartsLikeRust(t *testing.T) {
	state := SafetyBufferingState{}
	state.RecordTurn("turn-1")
	first := state.ApplyWithContext(SafetyBufferingUpdate{
		TurnID:          "turn-1",
		ShowBufferingUI: true,
		FasterModel:     "fast",
		CanRetry:        true,
	}, SafetyBufferingContext{AgentTurnRunning: true, LastTurnID: "turn-1", ThreadID: "thread-1", EnforceTurn: true})
	if first.Prompt == nil {
		t.Fatalf("first update = %#v, want prompt", first)
	}
	if !state.MarkAgentMessageStarted() {
		t.Fatal("agent message start was not recorded")
	}
	stale := state.ApplyWithContext(SafetyBufferingUpdate{
		TurnID:          "turn-1",
		ShowBufferingUI: true,
		FasterModel:     "fast",
		CanRetry:        true,
	}, SafetyBufferingContext{AgentTurnRunning: true, LastTurnID: "turn-1", ThreadID: "thread-1", EnforceTurn: true})
	if !stale.Ignored || stale.Prompt != nil {
		t.Fatalf("stale update after response start = %#v", stale)
	}
}

func TestSafetyBufferingNoRetryUpdateStillShowsDismissViewLikeRust(t *testing.T) {
	state := SafetyBufferingState{}
	state.RecordTurn("turn-1")
	result := state.ApplyWithContext(SafetyBufferingUpdate{
		TurnID:          "turn-1",
		ShowBufferingUI: true,
	}, SafetyBufferingContext{AgentTurnRunning: true, LastTurnID: "turn-1", ThreadID: "thread-1", EnforceTurn: true})
	if result.Prompt == nil || result.Prompt.Title != SafetyBufferingHeader || len(result.Prompt.Items) != 2 {
		t.Fatalf("no-retry result = %#v", result)
	}
}
