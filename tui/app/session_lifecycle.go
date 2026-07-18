package app

import (
	"os"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// Rust parity subset: codex-rs/tui/src/app/session_lifecycle.rs.

type SessionLifecycleState struct {
	ThreadID string
	Started  bool
	Closed   bool
}

type AgentPickerLivenessRead struct {
	ThreadID         string
	Thread           *appserver.Thread
	ReadError        error
	HasReplayChannel bool
}

type AgentPickerLivenessResult struct {
	Available     bool
	Removed       bool
	IsRunning     bool
	IsClosed      bool
	AgentNickname string
	AgentRole     string
}

type AgentThreadSelectionInput struct {
	ThreadID              string
	ActiveThreadID        string
	EntryExists           bool
	EntryIsClosed         bool
	HasThreadEventChannel bool
	LivenessAvailable     bool
	AttachAttempted       bool
	AttachError           error
	LiveAttached          bool
}

type AgentThreadSelectionDecision struct {
	Noop                 bool
	Proceed              bool
	IsReplayOnly         bool
	AttachedReplayOnly   bool
	ErrorMessage         string
	ShouldAttachLive     bool
	NoLongerAvailable    bool
	AlreadyActiveMessage bool
}

type StartupThreadStartedDecision struct {
	PendingCleared            bool
	QueueSubmissions          *bool
	UnsubscribeStaleThreadID  string
	DiscardStaleThreadID      string
	EnqueuePrimaryThread      bool
	MaybeSendQueuedInput      bool
	ErrorMessage              string
	IgnoreUnexpectedCompleted bool
}

type SessionSummaryHint struct {
	UsageLine  string
	ResumeHint string
}

func IsTerminalThreadReadError(err error) bool {
	return errorChainContainsAny(err, "thread not loaded:")
}

func ClosedStateForThreadReadError(err error, existingIsClosed *bool) bool {
	if IsTerminalThreadReadError(err) {
		return true
	}
	return existingIsClosed != nil && *existingIsClosed
}

func CanFallbackFromIncludeTurnsError(err error) bool {
	return errorChainContainsAny(err,
		"includeTurns is unavailable before first user message",
		"ephemeral threads do not support includeTurns",
	)
}

func RefreshAgentPickerThreadLiveness(nav *AgentNavigationState, read AgentPickerLivenessRead) AgentPickerLivenessResult {
	threadID := ""
	if read.ThreadID != "" {
		var ok bool
		threadID, ok = ParseAppServerThreadID(read.ThreadID)
		if !ok {
			return AgentPickerLivenessResult{}
		}
	} else if read.Thread != nil {
		var ok bool
		threadID, ok = ParseAppServerThreadID(read.Thread.ID)
		if !ok {
			return AgentPickerLivenessResult{}
		}
	}
	if nav == nil || threadID == "" {
		return AgentPickerLivenessResult{}
	}

	existing, hasExisting := nav.Get(threadID)
	if read.Thread != nil && read.ReadError == nil {
		nickname := ""
		if read.Thread.AgentNickname != nil {
			nickname = *read.Thread.AgentNickname
		} else if hasExisting {
			nickname = existing.AgentNickname
		}
		role := ""
		if read.Thread.AgentRole != nil {
			role = *read.Thread.AgentRole
		} else if hasExisting {
			role = existing.AgentRole
		}
		isRunning := read.Thread.Status.Type == "active"
		isClosed := read.Thread.Status.Type == "notLoaded"
		nav.Upsert(threadID, nickname, role, isClosed)
		nav.SetRunning(threadID, isRunning)
		return AgentPickerLivenessResult{
			Available:     true,
			IsRunning:     isRunning,
			IsClosed:      isClosed,
			AgentNickname: nickname,
			AgentRole:     role,
		}
	}

	if IsTerminalThreadReadError(read.ReadError) && !read.HasReplayChannel {
		nav.Remove(threadID)
		return AgentPickerLivenessResult{Removed: true}
	}

	var existingClosed *bool
	if hasExisting {
		closed := existing.IsClosed
		existingClosed = &closed
	}
	isClosed := ClosedStateForThreadReadError(read.ReadError, existingClosed)
	nickname := ""
	role := ""
	if hasExisting {
		nickname = existing.AgentNickname
		role = existing.AgentRole
	}
	nav.Upsert(threadID, nickname, role, isClosed)
	nav.SetRunning(threadID, false)
	return AgentPickerLivenessResult{
		Available:     true,
		IsClosed:      isClosed,
		AgentNickname: nickname,
		AgentRole:     role,
	}
}

func ShouldAttachLiveThreadForSelection(hasThreadEventChannel bool, entryExists bool, entryIsClosed bool) bool {
	return !hasThreadEventChannel && (!entryExists || !entryIsClosed)
}

func SelectAgentThreadDecision(input AgentThreadSelectionInput) AgentThreadSelectionDecision {
	threadID, ok := ParseAppServerThreadID(input.ThreadID)
	if !ok {
		return AgentThreadSelectionDecision{}
	}
	activeThreadID, hasActiveThreadID := ParseAppServerThreadID(input.ActiveThreadID)
	if hasActiveThreadID && activeThreadID == threadID {
		return AgentThreadSelectionDecision{Noop: true}
	}
	if !input.LivenessAvailable {
		return AgentThreadSelectionDecision{
			ErrorMessage:      "Agent thread " + threadID + " is no longer available.",
			NoLongerAvailable: true,
		}
	}

	isReplayOnly := input.EntryExists && input.EntryIsClosed
	shouldAttach := ShouldAttachLiveThreadForSelection(input.HasThreadEventChannel, input.EntryExists, input.EntryIsClosed)
	if shouldAttach {
		if input.AttachAttempted && input.AttachError != nil {
			return AgentThreadSelectionDecision{
				ShouldAttachLive: true,
				ErrorMessage:     "Failed to attach to agent thread " + threadID + ": " + input.AttachError.Error(),
			}
		}
		if input.AttachAttempted && !input.LiveAttached {
			isReplayOnly = true
			return AgentThreadSelectionDecision{
				Proceed:            true,
				IsReplayOnly:       true,
				AttachedReplayOnly: true,
				ShouldAttachLive:   true,
			}
		}
		return AgentThreadSelectionDecision{
			Proceed:          true,
			IsReplayOnly:     isReplayOnly,
			ShouldAttachLive: true,
		}
	}

	if !input.HasThreadEventChannel && isReplayOnly {
		return AgentThreadSelectionDecision{
			ErrorMessage:      "Agent thread " + threadID + " is no longer available.",
			NoLongerAvailable: true,
		}
	}
	return AgentThreadSelectionDecision{Proceed: true, IsReplayOnly: isReplayOnly}
}

func ReplayOnlyAgentThreadMessage(threadID string, attachedReplayOnly bool) string {
	parsedThreadID, ok := ParseAppServerThreadID(threadID)
	if !ok {
		return ""
	}
	threadID = parsedThreadID
	if attachedReplayOnly {
		return "Agent thread " + threadID + " could not be resumed live. Replaying saved transcript."
	}
	return "Agent thread " + threadID + " is closed. Replaying saved transcript."
}

func StartupThreadStartedDecisionForResult(pendingStartup bool, startedThreadID string, resultErr error) StartupThreadStartedDecision {
	if !pendingStartup {
		decision := StartupThreadStartedDecision{IgnoreUnexpectedCompleted: true}
		if parsedThreadID, ok := ParseAppServerThreadID(startedThreadID); resultErr == nil && ok {
			startedThreadID = parsedThreadID
			decision.UnsubscribeStaleThreadID = startedThreadID
			decision.DiscardStaleThreadID = startedThreadID
		}
		return decision
	}

	queue := false
	decision := StartupThreadStartedDecision{
		PendingCleared:       true,
		QueueSubmissions:     &queue,
		EnqueuePrimaryThread: resultErr == nil,
		MaybeSendQueuedInput: resultErr == nil,
	}
	if resultErr != nil {
		decision.EnqueuePrimaryThread = false
		decision.MaybeSendQueuedInput = false
		decision.ErrorMessage = "Failed to start a fresh session through the app server: " + resultErr.Error()
	}
	return decision
}

func SessionSummaryForThread(tokenUsage codextui.TokenUsage, threadID string, threadName string, rolloutPath string) *SessionSummaryHint {
	usageLine := ""
	if !tokenUsage.IsZero() {
		usageLine = tokenUsage.String()
	}
	resumeHint := ResumeHintForResumableThread(threadID, threadName, rolloutPath)
	if usageLine == "" && resumeHint == "" {
		return nil
	}
	return &SessionSummaryHint{UsageLine: usageLine, ResumeHint: resumeHint}
}

func ResumeHintForResumableThread(threadID string, threadName string, rolloutPath string) string {
	threadID, ok := ParseAppServerThreadID(threadID)
	if !ok || !RolloutPathIsResumable(rolloutPath) {
		return ""
	}
	if threadName != "" {
		return "codex resume, then select " + threadName + " (" + threadID + ")"
	}
	return "codex resume " + threadID
}

func RolloutPathIsResumable(rolloutPath string) bool {
	if rolloutPath == "" {
		return false
	}
	info, err := os.Stat(rolloutPath)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func ApplyLoadedSubagentBackfill(nav *AgentNavigationState, loaded []LoadedSubagentThread) {
	if nav == nil {
		return
	}
	for _, thread := range loaded {
		threadID, ok := ParseAppServerThreadID(thread.ThreadID)
		if !ok {
			continue
		}
		nav.Upsert(threadID, thread.AgentNickname, thread.AgentRole, false)
		nav.SetAgentPath(threadID, thread.AgentPath)
	}
}

func (s *SessionLifecycleState) MarkStarted(threadID string) {
	if s == nil {
		return
	}
	s.ThreadID = threadID
	s.Started = true
	s.Closed = false
}

func (s *SessionLifecycleState) MarkClosed() {
	if s == nil {
		return
	}
	s.Closed = true
}
