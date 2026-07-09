package chatwidget

import "strings"

type InputFlowState struct {
	SessionConfigured            bool
	TaskRunning                  bool
	PlanStreamingInTUI           bool
	OnlyUserShellCommandsRunning bool
	Queue                        InputQueueState
}

type InputFlowAction string

const (
	InputFlowSubmitNow       InputFlowAction = "submit_now"
	InputFlowQueue           InputFlowAction = "queue"
	InputFlowRejectEmpty     InputFlowAction = "reject_empty"
	InputFlowRestoreComposer InputFlowAction = "restore_composer"
)

func (s InputFlowState) Decide(message UserMessage, options SubmissionOptions) InputFlowAction {
	if !message.HasContent() {
		return InputFlowRejectEmpty
	}
	if !s.SessionConfigured || !options.SessionConfigured {
		return InputFlowQueue
	}
	if (len(message.LocalImages) > 0 || len(message.RemoteImageURLs) > 0) && !options.CurrentModelHasImages {
		return InputFlowRestoreComposer
	}
	if s.PlanStreamingInTUI || s.Queue.SuppressQueueAutosend || s.TaskRunning {
		return InputFlowQueue
	}
	if s.OnlyUserShellCommandsRunning && !strings.HasPrefix(message.Text, "!") {
		return InputFlowQueue
	}
	return InputFlowSubmitNow
}

func (s InputFlowState) HasPendingInput() bool {
	return s.Queue.HasQueuedFollowUpMessages() || len(s.Queue.PendingSteers) > 0 || s.Queue.UserTurnPendingStart
}
