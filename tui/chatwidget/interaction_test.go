package chatwidget

import "testing"

func TestInteractionCtrlCQuitShortcutAndInterruptMatchRust(t *testing.T) {
	decision := DecideInteractionKey(InteractionRoutingState{
		Key:                            InteractionKeyCtrlC,
		TaskRunning:                    true,
		DoublePressQuitShortcutEnabled: true,
	})
	if !decision.Handled || decision.Action != InteractionArmQuitShortcut || !decision.ArmQuitShortcut || !decision.SubmitInterrupt || !decision.PauseActiveGoal {
		t.Fatalf("first ctrl-c decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{
		Key:                            InteractionKeyCtrlC,
		TaskRunning:                    true,
		DoublePressQuitShortcutEnabled: true,
		QuitShortcutActiveForKey:       true,
	})
	if !decision.RequestQuit || !decision.ClearQuitShortcut || decision.SubmitInterrupt {
		t.Fatalf("second ctrl-c decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{
		Key:                            InteractionKeyCtrlC,
		ModalOrPopupActive:             true,
		BottomPaneCancellationHandled:  true,
		ActiveViewWillInterrupt:        true,
		DoublePressQuitShortcutEnabled: true,
	})
	if !decision.Handled || !decision.ClearQuitShortcut || decision.ArmQuitShortcut || !decision.PauseActiveGoal {
		t.Fatalf("modal ctrl-c cancellation decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{Key: InteractionKeyCtrlC})
	if !decision.RequestQuit {
		t.Fatalf("ctrl-c without double press/work should quit: %#v", decision)
	}
}

func TestInteractionCtrlDComposerGateMatchesRust(t *testing.T) {
	decision := DecideInteractionKey(InteractionRoutingState{
		Key:                            InteractionKeyCtrlD,
		ComposerEmpty:                  true,
		DoublePressQuitShortcutEnabled: true,
	})
	if !decision.Handled || !decision.ArmQuitShortcut {
		t.Fatalf("ctrl-d empty composer decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{
		Key:                            InteractionKeyCtrlD,
		ComposerEmpty:                  true,
		DoublePressQuitShortcutEnabled: true,
		QuitShortcutActiveForKey:       true,
	})
	if !decision.RequestQuit || !decision.ClearQuitShortcut {
		t.Fatalf("ctrl-d second press decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{
		Key:                            InteractionKeyCtrlD,
		ComposerEmpty:                  false,
		DoublePressQuitShortcutEnabled: true,
	})
	if decision.Handled || !decision.RouteToBottomPane || !decision.ClearQuitShortcut {
		t.Fatalf("ctrl-d non-empty composer should fall through: %#v", decision)
	}
}

func TestInteractionPendingSteersAndReviewWarningMatchRust(t *testing.T) {
	decision := DecideInteractionKey(InteractionRoutingState{
		Key:                  InteractionKeyEsc,
		InterruptTurnPressed: true,
		ReviewMode:           true,
		TaskRunning:          true,
		PendingSteerCount:    1,
	})
	if !decision.Handled || decision.Action != InteractionWarn || decision.WarningMessage == "" || decision.SubmitInterrupt {
		t.Fatalf("review steer warning decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{
		Key:                  InteractionKeyEsc,
		InterruptTurnPressed: true,
		TaskRunning:          true,
		PendingSteerCount:    1,
	})
	if !decision.Handled || !decision.SubmitInterrupt || !decision.SubmitPendingSteersAfterInterrupt || !decision.PauseActiveGoal {
		t.Fatalf("pending steer interrupt decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{
		Key:                  InteractionKeyEsc,
		InterruptTurnPressed: true,
		TaskRunning:          true,
		PendingSteerCount:    1,
		VimInsertEscape:      true,
	})
	if decision.Handled || !decision.RouteToBottomPane {
		t.Fatalf("vim escape should not interrupt: %#v", decision)
	}
}

func TestInteractionRoutingBranchesMatchRustOrder(t *testing.T) {
	decision := DecideInteractionKey(InteractionRoutingState{
		Key:                     InteractionKeyOther,
		BottomPaneHasActiveView: true,
		ActiveViewWillInterrupt: true,
		CopyLastResponsePressed: true,
	})
	if !decision.RouteToBottomPane || !decision.PauseActiveGoal || decision.CopyLastResponse {
		t.Fatalf("active bottom pane should receive ordinary keys first: %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{CopyLastResponsePressed: true})
	if !decision.CopyLastResponse || !decision.ClearQuitShortcut {
		t.Fatalf("copy binding decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{
		EditQueuedMessagePressed:  true,
		HasQueuedFollowUpMessages: true,
	})
	if !decision.RestoreLatestQueuedComposer {
		t.Fatalf("edit queued decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{Key: InteractionKeyEsc, PlanModeNudgeVisible: true})
	if !decision.DismissPlanModeNudge {
		t.Fatalf("esc plan nudge decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{
		Key:                       InteractionKeyBackTab,
		CollaborationModesEnabled: true,
	})
	if !decision.CycleCollaborationMode {
		t.Fatalf("backtab collaboration decision = %#v", decision)
	}

	decision = DecideInteractionKey(InteractionRoutingState{Key: InteractionKeyCtrlAltV})
	if !decision.AttachImageFromClipboard || !decision.ClearQuitShortcut {
		t.Fatalf("ctrl-alt-v decision = %#v", decision)
	}
}

func TestInteractionImageAttachGateMatchesRust(t *testing.T) {
	decision := DecideImageAttach(" image.png ", "gpt-text", false)
	if decision.Attach || decision.Path != "" || !decision.Redraw || decision.Warning != "Model gpt-text does not support image inputs. Remove images or switch models." {
		t.Fatalf("unsupported image decision = %#v", decision)
	}

	decision = DecideImageAttach(" image.png ", "gpt-vision", true)
	if !decision.Attach || decision.Path != "image.png" || !decision.Redraw || decision.Warning != "" {
		t.Fatalf("supported image decision = %#v", decision)
	}

	decision = DecideImageAttach(" ", "gpt-vision", true)
	if decision.Attach || decision.Redraw || decision.Warning != "" {
		t.Fatalf("empty image decision = %#v", decision)
	}
}
