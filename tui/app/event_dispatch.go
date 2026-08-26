package app

import "time"

const ShutdownFirstExitTimeout = 2 * time.Second

type DispatchResult struct {
	Handled bool
	Error   string
}

type ExitMode string

const (
	ExitModeShutdownFirst ExitMode = "shutdown_first"
	ExitModeImmediate     ExitMode = "immediate"
)

type ExitReason string

const (
	ExitReasonUserRequested ExitReason = "user_requested"
	ExitReasonFatal         ExitReason = "fatal"
	ExitReasonTurnInterrupted ExitReason = "turn_interrupted"
	ExitReasonThreadRemoved   ExitReason = "thread_removed"
)

type AppRunControlKind string

const (
	AppRunControlContinue AppRunControlKind = "continue"
	AppRunControlExit     AppRunControlKind = "exit"
)

type AppRunControl struct {
	Kind   AppRunControlKind
	Reason ExitReason
}

// DescribeExitReason returns the terminal summary line for an exit reason,
// distinguishing a disconnected app server from an interrupted turn or a
// removed thread (Rust #40629).
func DescribeExitReason(reason ExitReason) string {
	switch reason {
	case ExitReasonTurnInterrupted:
		return "The active turn was interrupted"
	case ExitReasonThreadRemoved:
		return "The thread was removed"
	case ExitReasonFatal:
		return "Disconnected from the app server"
	default:
		return "Session ended"
	}
}

type ExitModeDecision struct {
	PendingShutdownExitThreadID string
	ShouldShutdownCurrentThread bool
	ShutdownTimeout             time.Duration
	Control                     AppRunControl
}

func HandleExitModeDecision(mode ExitMode, activeThreadID string, chatWidgetThreadID string) ExitModeDecision {
	decision := ExitModeDecision{
		Control: AppRunControl{Kind: AppRunControlExit, Reason: ExitReasonUserRequested},
	}
	if mode != ExitModeShutdownFirst {
		return decision
	}
	threadID := firstNonEmptyDispatch(activeThreadID, chatWidgetThreadID)
	if threadID != "" {
		decision.PendingShutdownExitThreadID = threadID
		decision.ShouldShutdownCurrentThread = true
		decision.ShutdownTimeout = ShutdownFirstExitTimeout
	}
	return decision
}

type CurrentThreadMutationAction string

const (
	CurrentThreadMutationArchive CurrentThreadMutationAction = "archive"
	CurrentThreadMutationDelete  CurrentThreadMutationAction = "delete"
)

type CurrentThreadMutationDecision struct {
	ThreadID string
	Allowed  bool
	Message  string
}

func CurrentThreadMutationPreflight(action CurrentThreadMutationAction, activeThreadID string, chatWidgetThreadID string, sideThreads map[string]bool) CurrentThreadMutationDecision {
	threadID := firstNonEmptyDispatch(activeThreadID, chatWidgetThreadID)
	if threadID == "" {
		return CurrentThreadMutationDecision{Message: "A thread must start before it can be " + currentThreadMutationPastTense(action) + "."}
	}
	if sideThreads != nil && sideThreads[threadID] {
		return CurrentThreadMutationDecision{ThreadID: threadID, Message: "'/" + string(action) + "' is unavailable in side conversations. Press Ctrl+C to return to the main thread first."}
	}
	return CurrentThreadMutationDecision{ThreadID: threadID, Allowed: true}
}

func currentThreadMutationPastTense(action CurrentThreadMutationAction) string {
	switch action {
	case CurrentThreadMutationDelete:
		return "deleted"
	default:
		return "archived"
	}
}

func firstNonEmptyDispatch(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
