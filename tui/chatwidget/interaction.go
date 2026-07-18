package chatwidget

import "strings"

type InteractionAction string

const (
	InteractionCopyLastResponse InteractionAction = "copy_last_response"
	InteractionToggleTranscript InteractionAction = "toggle_transcript"
	InteractionInterrupt        InteractionAction = "interrupt"
	InteractionRouteBottomPane  InteractionAction = "route_bottom_pane"
	InteractionArmQuitShortcut  InteractionAction = "arm_quit_shortcut"
	InteractionRequestQuit      InteractionAction = "request_quit"
	InteractionRestoreQueued    InteractionAction = "restore_latest_queued_composer"
	InteractionDismissPlanNudge InteractionAction = "dismiss_plan_mode_nudge"
	InteractionCycleMode        InteractionAction = "cycle_collaboration_mode"
	InteractionAttachImage      InteractionAction = "attach_image"
	InteractionWarn             InteractionAction = "warn"
)

type InteractionState struct {
	LastAssistantResponse string
	TranscriptOpen        bool
	TaskRunning           bool
}

type InteractionKey string

const (
	InteractionKeyOther    InteractionKey = "other"
	InteractionKeyCtrlC    InteractionKey = "ctrl+c"
	InteractionKeyCtrlD    InteractionKey = "ctrl+d"
	InteractionKeyCtrlR    InteractionKey = "ctrl+r"
	InteractionKeyCtrlU    InteractionKey = "ctrl+u"
	InteractionKeyCtrlAltV InteractionKey = "ctrl+alt+v"
	InteractionKeyEsc      InteractionKey = "esc"
	InteractionKeyBackTab  InteractionKey = "backtab"
)

type InteractionRoutingState struct {
	Key                            InteractionKey
	BottomPaneHasActiveView        bool
	BottomPaneCancellationHandled  bool
	ActiveViewWillInterrupt        bool
	ModalOrPopupActive             bool
	ReasoningShortcutHandled       bool
	CopyLastResponsePressed        bool
	EditQueuedMessagePressed       bool
	HasQueuedFollowUpMessages      bool
	InterruptTurnPressed           bool
	ReviewMode                     bool
	PendingSteerCount              int
	RejectedSteerCount             int
	TaskRunning                    bool
	VimInsertEscape                bool
	PlanModeNudgeVisible           bool
	PluginsPopupHandled            bool
	CollaborationModesEnabled      bool
	DoublePressQuitShortcutEnabled bool
	QuitShortcutActiveForKey       bool
	ComposerEmpty                  bool
	CurrentModelSupportsImages     bool
}

type InteractionDecision struct {
	Handled                           bool
	Action                            InteractionAction
	RouteToBottomPane                 bool
	ClearQuitShortcut                 bool
	ArmQuitShortcut                   bool
	RequestQuit                       bool
	SubmitInterrupt                   bool
	SubmitPendingSteersAfterInterrupt bool
	PauseActiveGoal                   bool
	CopyLastResponse                  bool
	RestoreLatestQueuedComposer       bool
	DismissPlanModeNudge              bool
	CycleCollaborationMode            bool
	AttachImageFromClipboard          bool
	WarningMessage                    string
}

type ImageAttachDecision struct {
	Attach  bool
	Path    string
	Warning string
	Redraw  bool
}

func (s InteractionState) CopyLastAssistantResponse() (string, bool) {
	text := strings.TrimSpace(s.LastAssistantResponse)
	if text == "" {
		return "", false
	}
	return text, true
}

func (s *InteractionState) ToggleTranscript() bool {
	if s == nil {
		return false
	}
	s.TranscriptOpen = !s.TranscriptOpen
	return s.TranscriptOpen
}

func (s InteractionState) CanInterrupt() bool {
	return s.TaskRunning
}

func DecideInteractionKey(state InteractionRoutingState) InteractionDecision {
	if state.BottomPaneHasActiveView && !interactionBypassesActiveBottomPane(state.Key) {
		return InteractionDecision{
			Handled:           true,
			Action:            InteractionRouteBottomPane,
			RouteToBottomPane: true,
			PauseActiveGoal:   state.ActiveViewWillInterrupt,
		}
	}

	if state.ReasoningShortcutHandled {
		return InteractionDecision{
			Handled:           true,
			Action:            "reasoning_shortcut",
			ClearQuitShortcut: true,
		}
	}

	if state.CopyLastResponsePressed {
		return InteractionDecision{
			Handled:           true,
			Action:            InteractionCopyLastResponse,
			ClearQuitShortcut: true,
			CopyLastResponse:  true,
		}
	}

	switch state.Key {
	case InteractionKeyCtrlC:
		return decideCtrlC(state)
	case InteractionKeyCtrlAltV:
		return InteractionDecision{Handled: true, Action: InteractionAttachImage, AttachImageFromClipboard: true, ClearQuitShortcut: true}
	}

	clearQuit := state.Key != ""
	if state.Key == InteractionKeyCtrlD {
		if decision, handled := decideCtrlD(state); handled {
			return decision
		}
		clearQuit = true
	}

	if state.EditQueuedMessagePressed && state.HasQueuedFollowUpMessages && !state.ModalOrPopupActive {
		return InteractionDecision{
			Handled:                     true,
			Action:                      InteractionRestoreQueued,
			ClearQuitShortcut:           clearQuit,
			RestoreLatestQueuedComposer: true,
		}
	}

	if state.InterruptTurnPressed &&
		state.ReviewMode &&
		(state.PendingSteerCount > 0 || state.RejectedSteerCount > 0) &&
		state.TaskRunning &&
		!state.ModalOrPopupActive &&
		!state.VimInsertEscape {
		return InteractionDecision{
			Handled:           true,
			Action:            InteractionWarn,
			ClearQuitShortcut: clearQuit,
			WarningMessage:    "Steer messages aren't supported during /review. Press Ctrl+C now to cancel the review.",
		}
	}

	if state.InterruptTurnPressed &&
		state.PendingSteerCount > 0 &&
		state.TaskRunning &&
		!state.ModalOrPopupActive &&
		!state.VimInsertEscape {
		return InteractionDecision{
			Handled:                           true,
			Action:                            InteractionInterrupt,
			ClearQuitShortcut:                 clearQuit,
			SubmitInterrupt:                   true,
			SubmitPendingSteersAfterInterrupt: true,
			PauseActiveGoal:                   true,
		}
	}

	if state.Key == InteractionKeyEsc && state.PlanModeNudgeVisible {
		return InteractionDecision{
			Handled:              true,
			Action:               InteractionDismissPlanNudge,
			ClearQuitShortcut:    clearQuit,
			DismissPlanModeNudge: true,
		}
	}

	if state.PluginsPopupHandled {
		return InteractionDecision{Handled: true, Action: "plugins_popup", ClearQuitShortcut: clearQuit}
	}

	if state.Key == InteractionKeyBackTab &&
		state.CollaborationModesEnabled &&
		!state.TaskRunning &&
		!state.ModalOrPopupActive {
		return InteractionDecision{
			Handled:                true,
			Action:                 InteractionCycleMode,
			ClearQuitShortcut:      clearQuit,
			CycleCollaborationMode: true,
		}
	}

	return InteractionDecision{
		Action:            InteractionRouteBottomPane,
		RouteToBottomPane: true,
		ClearQuitShortcut: clearQuit,
	}
}

func DecideImageAttach(path string, currentModel string, currentModelSupportsImages bool) ImageAttachDecision {
	path = strings.TrimSpace(path)
	if path == "" {
		return ImageAttachDecision{}
	}
	if !currentModelSupportsImages {
		model := strings.TrimSpace(currentModel)
		if model == "" {
			model = "the selected model"
		}
		return ImageAttachDecision{
			Warning: "Model " + model + " does not support image inputs. Remove images or switch models.",
			Redraw:  true,
		}
	}
	return ImageAttachDecision{Attach: true, Path: path, Redraw: true}
}

func decideCtrlC(state InteractionRoutingState) InteractionDecision {
	if state.BottomPaneCancellationHandled {
		return InteractionDecision{
			Handled:           true,
			Action:            InteractionRouteBottomPane,
			ClearQuitShortcut: state.ModalOrPopupActive,
			ArmQuitShortcut:   state.DoublePressQuitShortcutEnabled && !state.ModalOrPopupActive,
			PauseActiveGoal:   state.ActiveViewWillInterrupt,
		}
	}

	cancellableWorkActive := state.TaskRunning || state.ReviewMode
	if !state.DoublePressQuitShortcutEnabled {
		if cancellableWorkActive {
			return InteractionDecision{
				Handled:           true,
				Action:            InteractionInterrupt,
				ClearQuitShortcut: true,
				SubmitInterrupt:   true,
				PauseActiveGoal:   true,
			}
		}
		return InteractionDecision{Handled: true, Action: InteractionRequestQuit, RequestQuit: true}
	}

	if state.QuitShortcutActiveForKey {
		return InteractionDecision{Handled: true, Action: InteractionRequestQuit, ClearQuitShortcut: true, RequestQuit: true}
	}

	return InteractionDecision{
		Handled:         true,
		Action:          InteractionArmQuitShortcut,
		ArmQuitShortcut: true,
		SubmitInterrupt: cancellableWorkActive,
		PauseActiveGoal: cancellableWorkActive,
	}
}

func decideCtrlD(state InteractionRoutingState) (InteractionDecision, bool) {
	if !state.DoublePressQuitShortcutEnabled {
		if !state.ComposerEmpty || state.ModalOrPopupActive {
			return InteractionDecision{}, false
		}
		return InteractionDecision{Handled: true, Action: InteractionRequestQuit, RequestQuit: true}, true
	}

	if state.QuitShortcutActiveForKey {
		return InteractionDecision{Handled: true, Action: InteractionRequestQuit, ClearQuitShortcut: true, RequestQuit: true}, true
	}
	if !state.ComposerEmpty || state.ModalOrPopupActive {
		return InteractionDecision{}, false
	}
	return InteractionDecision{Handled: true, Action: InteractionArmQuitShortcut, ArmQuitShortcut: true}, true
}

func interactionBypassesActiveBottomPane(key InteractionKey) bool {
	return key == InteractionKeyCtrlC || key == InteractionKeyCtrlR || key == InteractionKeyCtrlU
}
