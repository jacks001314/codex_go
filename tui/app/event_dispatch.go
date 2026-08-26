package app

import (
	"strings"
	"time"
)

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

// DisconnectInfo carries reconnect and stop guidance for a task owned by a
// persistent app server (Rust #40629).
type DisconnectInfo struct {
	Command  []string
	StopHint string
}

// AppExitInfo summarizes the terminal exit output, distinguishing a
// disconnect from an interrupted turn or a removed thread.
type AppExitInfo struct {
	ThreadID       string
	ExitReason     ExitReason
	ResumeHint     string
	TokenUsageLine string
	Disconnect     *DisconnectInfo
}

// FormatExitMessages renders the exit summary lines.
func (i *AppExitInfo) FormatExitMessages() []string {
	if i == nil {
		return nil
	}
	var lines []string
	if summary := DescribeExitReason(i.ExitReason); summary != "" {
		lines = append(lines, summary)
	}
	if i.ThreadID != "" {
		lines = append(lines, "Thread: "+i.ThreadID)
	}
	if i.ResumeHint != "" {
		lines = append(lines, i.ResumeHint)
	}
	if i.Disconnect != nil {
		if len(i.Disconnect.Command) > 0 {
			lines = append(lines, "Reconnect: "+strings.Join(i.Disconnect.Command, " "))
		}
		if i.Disconnect.StopHint != "" {
			lines = append(lines, "Stop the running turn: "+i.Disconnect.StopHint)
		}
	}
	return lines
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
