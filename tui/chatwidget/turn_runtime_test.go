package chatwidget

import (
	"testing"

	historycell "codex_go/tui/history_cell"
)

func TestTurnRuntimeTaskStartedMatchesRustStateReset(t *testing.T) {
	state := TurnRuntimeState{
		InputQueue: InputQueueState{
			UserTurnPendingStart: true,
		},
		SafetyBuffering: SafetyBufferingState{
			ActiveTurnID:        "old",
			RetryPromptShown:    true,
			AgentMessageStarted: true,
		},
		StatusHeader: "Idle",
		Streaming: ChatStreamingState{
			ReasoningBuffer:     "**Thinking**",
			FullReasoningBuffer: "old",
		},
	}

	state.OnTaskStarted("turn-1")

	if !state.Lifecycle.AgentTurnRunning || !state.TaskRunning || !state.Streaming.TaskRunning {
		t.Fatalf("turn did not enter running state: %#v", state)
	}
	if state.InputQueue.UserTurnPendingStart {
		t.Fatalf("pending start flag should clear on task start")
	}
	if state.SafetyBuffering.ActiveTurnID != "" || state.SafetyBuffering.RetryPromptShown || state.SafetyBuffering.AgentMessageStarted {
		t.Fatalf("active safety buffering should reset, got %#v", state.SafetyBuffering)
	}
	if state.SafetyBuffering.SubmittedTurnID != "turn-1" {
		t.Fatalf("submitted safety turn = %q, want turn-1", state.SafetyBuffering.SubmittedTurnID)
	}
	if !state.InterruptHintVisible || state.StatusKind != TurnRuntimeStatusWorking || state.StatusHeader != "Working" {
		t.Fatalf("status not reset to working: visible=%v kind=%q header=%q", state.InterruptHintVisible, state.StatusKind, state.StatusHeader)
	}
	if state.Streaming.ReasoningBuffer != "" || state.Streaming.FullReasoningBuffer != "" {
		t.Fatalf("reasoning buffers should clear: %#v", state.Streaming)
	}
	if state.PetNotificationKind != "running" || state.RequestRedrawCount != 1 {
		t.Fatalf("start side effects = pet %q redraw %d", state.PetNotificationKind, state.RequestRedrawCount)
	}
}

func TestTurnRuntimeTaskStartedPreservesMCPStartupHeader(t *testing.T) {
	state := TurnRuntimeState{
		MCPStartupActive: true,
		StatusHeader:     MCPStartupSingleHeaderPrefix + " server-a",
	}

	state.OnTaskStarted("turn-1")

	if state.StatusHeader != MCPStartupSingleHeaderPrefix+" server-a" {
		t.Fatalf("MCP-owned header overwritten: %q", state.StatusHeader)
	}
}

func TestTurnRuntimeTaskCompleteFollowUpSeparatorAndNotificationMatchRust(t *testing.T) {
	duration := int64(125_000)
	state := TurnRuntimeState{
		HadWorkActivity:            true,
		NeedsFinalMessageSeparator: true,
		InputQueue: InputQueueState{
			QueuedUserMessages: []QueuedUserMessage{
				NewQueuedUserMessage(NewUserMessage("next"), QueuedInputPlain),
			},
			QueuedUserMessageHistoryRecords: []UserMessageHistoryRecord{UserMessageTextHistoryRecord()},
		},
	}
	state.OnTaskStarted("turn-1")
	state.HadWorkActivity = true
	state.NeedsFinalMessageSeparator = true
	state.RuntimeMetrics = historycell.RuntimeMetricsSummary{
		ToolCalls: historycell.RuntimeMetricCountDuration{Count: 2, DurationMS: 1500},
	}

	result := state.OnTaskComplete(TurnCompleteRuntimeParams{
		TurnID:           "turn-1",
		LastAgentMessage: " final answer ",
		DurationMS:       &duration,
	})

	if !result.Completed || !result.FollowUpStarted {
		t.Fatalf("completion result = %#v, want completed follow-up", result)
	}
	if result.NotificationSent {
		t.Fatalf("queued follow-up should suppress agent-turn-complete notification")
	}
	if state.Lifecycle.AgentTurnRunning || state.TaskRunning {
		t.Fatalf("turn should stop running: lifecycle=%#v task=%v", state.Lifecycle, state.TaskRunning)
	}
	if !state.InputQueue.UserTurnPendingStart || state.FollowUpStartedCount != 1 || len(state.InputQueue.QueuedUserMessages) != 0 {
		t.Fatalf("queued follow-up not started/drained: queue=%#v count=%d", state.InputQueue, state.FollowUpStartedCount)
	}
	if state.LastAgentMarkdown != "final answer" || result.NotificationResponse != "final answer" {
		t.Fatalf("last markdown/notification response = %q/%q", state.LastAgentMarkdown, result.NotificationResponse)
	}
	if len(state.FinalMessageSeparators) != 1 || !result.FinalSeparatorAdded || !result.RuntimeMetricsIncluded {
		t.Fatalf("final separator missing: result=%#v separators=%d", result, len(state.FinalMessageSeparators))
	}
	if state.BranchRefreshRequests != 1 || state.GitSummaryRefreshRequests != 1 {
		t.Fatalf("status refresh requests = %d/%d", state.BranchRefreshRequests, state.GitSummaryRefreshRequests)
	}
}

func TestTurnRuntimeTaskCompleteNotificationSuppressedByActiveGoal(t *testing.T) {
	state := TurnRuntimeState{ActiveGoalContinuing: true}
	state.OnTaskStarted("turn-1")

	result := state.OnTaskComplete(TurnCompleteRuntimeParams{TurnID: "turn-1", LastAgentMessage: "done"})

	if result.NotificationSent || len(state.Notifications) != 0 {
		t.Fatalf("active goal continuation should suppress notification: result=%#v notifications=%#v", result, state.Notifications)
	}
}

func TestTurnRuntimePlanImplementationPromptGateAndContextLabel(t *testing.T) {
	contextWindow := int64(100_000)
	state := TurnRuntimeState{
		CollaborationModesEnabled: true,
		PlanModeActive:            true,
		DefaultModeAvailable:      true,
		SawPlanItemThisTurn:       true,
		LatestProposedPlan:        "# Plan",
		ContextWindowSize:         &contextWindow,
		LastTokenUsage: StatusTokenUsage{
			TotalTokens: 56_000,
		},
	}

	if !state.MaybePromptPlanImplementation() {
		t.Fatalf("plan implementation prompt should open")
	}
	if state.PlanImplementationPrompt == nil || state.PlanImplementationPrompt.ViewID != PlanImplementationViewID {
		t.Fatalf("missing plan prompt: %#v", state.PlanImplementationPrompt)
	}
	if got := state.PlanImplementationPrompt.Items[1].Description; got != "Fresh thread. Context: 50% used." {
		t.Fatalf("clear-context description = %q", got)
	}
	if len(state.Notifications) != 1 || state.Notifications[0].Kind != TurnRuntimeNotificationPlanModePrompt {
		t.Fatalf("plan prompt notification = %#v", state.Notifications)
	}

	blocked := state
	blocked.PlanImplementationPrompt = nil
	blocked.Notifications = nil
	blocked.RateLimitSwitchPrompt = RateLimitSwitchPromptPending
	if blocked.MaybePromptPlanImplementation() {
		t.Fatalf("pending rate-limit switch prompt should block plan implementation prompt")
	}
}

func TestTurnRuntimePlanImplementationContextUsageTokenFallback(t *testing.T) {
	used := int64(12_340)
	state := TurnRuntimeState{ContextUsedTokens: &used}

	label, ok := state.PlanImplementationContextUsageLabel()

	if !ok || label != "12.3K used" {
		t.Fatalf("context label = %q ok=%v, want compact token label", label, ok)
	}
}

func TestTurnRuntimeHandleNonRetryErrorClassifiesRustBranches(t *testing.T) {
	t.Run("rejected steer", func(t *testing.T) {
		state := TurnRuntimeState{
			InputQueue: InputQueueState{
				PendingSteers: []PendingSteer{{UserMessage: NewUserMessage("steer"), HistoryRecord: UserMessageTextHistoryRecord()}},
			},
		}
		state.OnTaskStarted("turn-1")

		outcome := state.HandleNonRetryError("not steerable", &TurnRuntimeCodexErrorInfo{Kind: TurnRuntimeCodexErrorActiveTurnNotSteerable})

		if outcome != TurnRuntimeErrorRejectedSteer || len(state.InputQueue.PendingSteers) != 0 || len(state.InputQueue.RejectedSteersQueue) != 1 {
			t.Fatalf("rejected steer outcome=%q queue=%#v", outcome, state.InputQueue)
		}
		if !state.Lifecycle.AgentTurnRunning {
			t.Fatalf("active-turn-not-steerable should not finalize the current turn")
		}
	})

	t.Run("safety access block json", func(t *testing.T) {
		state := TurnRuntimeState{InputQueue: InputQueueState{SubmitPendingSteersAfterInterrupt: true}}
		state.OnTaskStarted("turn-1")
		message := `{"error":{"message":"` + SafetyAccessBlockPrefix + ` Extra detail."}}`

		outcome := state.HandleNonRetryError(message, nil)

		if outcome != TurnRuntimeErrorSafetyAccessBlock || state.Lifecycle.AgentTurnRunning {
			t.Fatalf("safety outcome=%q running=%v", outcome, state.Lifecycle.AgentTurnRunning)
		}
		if state.InputQueue.SubmitPendingSteersAfterInterrupt {
			t.Fatalf("submit pending steers flag should clear")
		}
		if len(state.HistoryEvents) == 0 || state.HistoryEvents[len(state.HistoryEvents)-1].Kind != TurnRuntimeHistorySafetyAccessBlock {
			t.Fatalf("history events = %#v", state.HistoryEvents)
		}
	})

	t.Run("cyber policy", func(t *testing.T) {
		state := TurnRuntimeState{}
		state.OnTaskStarted("turn-1")

		outcome := state.HandleNonRetryError("blocked", &TurnRuntimeCodexErrorInfo{Kind: TurnRuntimeCodexErrorCyberPolicy})

		if outcome != TurnRuntimeErrorCyberPolicy || state.LastErrorOutcome != TurnRuntimeErrorCyberPolicy {
			t.Fatalf("cyber policy outcome=%q last=%q", outcome, state.LastErrorOutcome)
		}
		if len(state.HistoryEvents) == 0 || state.HistoryEvents[len(state.HistoryEvents)-1].Kind != TurnRuntimeHistoryCyberPolicy {
			t.Fatalf("history events = %#v", state.HistoryEvents)
		}
	})

	t.Run("server overloaded default message", func(t *testing.T) {
		state := TurnRuntimeState{}
		state.OnTaskStarted("turn-1")

		outcome := state.HandleNonRetryError("", &TurnRuntimeCodexErrorInfo{Kind: TurnRuntimeCodexErrorServerOverloaded})

		if outcome != TurnRuntimeErrorServerOverloaded || state.LastErrorMessage != "Codex is currently experiencing high load." {
			t.Fatalf("overloaded outcome=%q message=%q", outcome, state.LastErrorMessage)
		}
	})

	t.Run("usage limit maps member credits to member usage prompt", func(t *testing.T) {
		state := TurnRuntimeState{RateLimitReachedType: TurnWorkspaceMemberCreditsDepleted}
		state.OnTaskStarted("turn-1")

		outcome := state.HandleNonRetryError("limit hit", &TurnRuntimeCodexErrorInfo{Kind: TurnRuntimeCodexErrorUsageLimitExceeded})

		if outcome != TurnRuntimeErrorRateLimit || state.RateLimitReachedType != TurnWorkspaceMemberUsageLimitReached {
			t.Fatalf("rate-limit outcome=%q reached=%q", outcome, state.RateLimitReachedType)
		}
		if state.WorkspaceOwnerNudge == nil || *state.WorkspaceOwnerNudge != AddCreditsNudgeUsageLimit {
			t.Fatalf("workspace owner nudge = %#v", state.WorkspaceOwnerNudge)
		}
	})
}

func TestTurnRuntimeInterruptedTurnMessageMatchesRust(t *testing.T) {
	state := TurnRuntimeState{}

	if got := state.InterruptedTurnMessage(TurnAbortBudgetLimited); got != "Goal budget reached - the turn was stopped." {
		t.Fatalf("budget message = %q", got)
	}
	if got := state.InterruptedTurnMessage(TurnAbortInterrupted); got != "Conversation interrupted - tell the model what to do differently. Something went wrong? Hit `/feedback` to report the issue." {
		t.Fatalf("interrupt message = %q", got)
	}
}
