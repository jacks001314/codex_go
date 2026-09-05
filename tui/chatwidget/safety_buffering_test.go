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
	if !noRetry.DismissPrompt || noRetry.Prompt != nil || noRetry.RetryAvailable {
		t.Fatalf("no-thread retry result = %#v", noRetry)
	}

	withRetry := state.ApplyWithContext(SafetyBufferingUpdate{
		TurnID:          "turn-1",
		ShowBufferingUI: true,
		FasterModel:     "fast",
		CanRetry:        true,
	}, SafetyBufferingContext{AgentTurnRunning: true, LastTurnID: "turn-1", ThreadID: "thread-1", EnforceTurn: true})
	if !withRetry.RetryAvailable || withRetry.Prompt == nil || withRetry.DismissPrompt {
		t.Fatalf("with retry result = %#v", withRetry)
	}
	if withRetry.Status.Header != "Working" || withRetry.Status.DetailsMaxLines != 6 {
		t.Fatalf("status = %#v", withRetry.Status)
	}
}

func TestSafetyBufferingResetPrepareAndFailRetryMatchRust(t *testing.T) {
	state := SafetyBufferingState{ActiveTurnID: "turn-1", RetryPromptShown: true, AgentMessageStarted: true}

	reset := state.ResetForTurnStart()
	if !reset.DismissPrompt || !reset.Cleared || state.ActiveTurnID != "" || state.RetryPromptShown || state.AgentMessageStarted {
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
	view := NewSafetyBufferingConfirmationView("gpt-fast")
	if view.ViewID != SafetyBufferingPromptViewID || !strings.Contains(view.Title, "Stop this attempt and retry?") {
		t.Fatalf("confirmation view = %#v", view)
	}
	if len(view.Items) != 2 || view.Items[0].Action != SafetyBufferingActionWait || view.Items[1].Action != SafetyBufferingActionStopRetry {
		t.Fatalf("confirmation items = %#v", view.Items)
	}
}
