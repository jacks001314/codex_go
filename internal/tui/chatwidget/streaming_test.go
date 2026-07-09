package chatwidget

import (
	"strings"
	"testing"
	"time"

	historycell "codex_go/internal/tui/history_cell"
)

func TestChatStreamingAnswerTailCommitAndFinalizeMatchRustCore(t *testing.T) {
	state := NewChatStreamingState(40)
	now := time.Unix(100, 0)

	state.OnAgentMessageDelta("Before table.\n")
	if state.MessageDeltaCount != 1 || state.VisibleTurnActivity != 1 {
		t.Fatalf("activity counts = %#v", state)
	}
	if state.StreamController == nil || state.StreamController.QueuedLines() != 1 {
		t.Fatalf("stream controller queued = %#v", state.StreamController)
	}

	state.RunCommitTick(now)
	if len(state.History) != 1 {
		t.Fatalf("history after commit = %d", len(state.History))
	}
	if _, ok := state.History[0].(historycell.AgentMessageCell); !ok {
		t.Fatalf("history[0] = %#v", state.History[0])
	}

	state.OnAgentMessageDelta("| A | B |\n")
	if state.ActiveTail.Kind != ChatStreamTailAnswer {
		t.Fatalf("tail should show held-back table header: %#v", state.ActiveTail)
	}

	state.FinalizeCompletedAssistantMessage("")
	if state.StreamController != nil || state.ActiveTail.Kind != ChatStreamTailNone {
		t.Fatalf("stream should finalize: controller=%#v tail=%#v", state.StreamController, state.ActiveTail)
	}
	if state.FinalizedAnswerSource != "Before table.\n| A | B |\n" {
		t.Fatalf("finalized source = %q", state.FinalizedAnswerSource)
	}
	if len(state.History) != 2 {
		t.Fatalf("history after finalize = %d", len(state.History))
	}
	if state.CommitAnimationStops == 0 || state.UsageInsertionRequests == 0 {
		t.Fatalf("stop/usage counters = %d/%d", state.CommitAnimationStops, state.UsageInsertionRequests)
	}
}

func TestChatStreamingPlanDeltaCompletionAndRestoreMatchRustCore(t *testing.T) {
	state := NewChatStreamingState(60)
	state.PlanMode = true
	state.TaskRunning = true

	state.OnPlanDelta("| A | B |\n")
	if state.PlanDeltaCount != 1 || !state.PlanItemActive || state.ActiveTail.Kind != ChatStreamTailPlan {
		t.Fatalf("plan state after delta = %#v", state)
	}

	state.OnPlanItemCompleted("")
	if state.PlanStreamController != nil || state.PlanItemActive || !state.SawPlanItemThisTurn {
		t.Fatalf("plan completion state = %#v", state)
	}
	if state.LatestProposedPlan != "| A | B |" {
		t.Fatalf("latest plan = %q", state.LatestProposedPlan)
	}
	if !state.StatusIndicatorVisible || state.PendingStatusRestore {
		t.Fatalf("status restore visible=%v pending=%v", state.StatusIndicatorVisible, state.PendingStatusRestore)
	}
	if state.UsageInsertionRequests == 0 {
		t.Fatalf("usage insertion requests = %d", state.UsageInsertionRequests)
	}
}

func TestChatStreamingReasoningHeadersAndFinalMatchRustCore(t *testing.T) {
	state := NewChatStreamingState(80)
	state.TaskRunning = true

	state.OnReasoningDelta("thinking **Inspecting files** and more")
	if state.StatusKind != "thinking" || state.StatusHeader != "Inspecting files" {
		t.Fatalf("status = %q %q", state.StatusKind, state.StatusHeader)
	}

	state.OnReasoningSectionBreak()
	if state.ReasoningBuffer != "" || !strings.Contains(state.FullReasoningBuffer, "Inspecting files") {
		t.Fatalf("section break buffers = %q / %q", state.ReasoningBuffer, state.FullReasoningBuffer)
	}
	state.OnReasoningDelta("next block")
	state.OnAgentReasoningFinal()
	if state.ReasoningBuffer != "" || state.FullReasoningBuffer != "" || len(state.History) != 1 {
		t.Fatalf("final reasoning state = %#v", state)
	}
	if _, ok := state.History[0].(historycell.ReasoningSummaryCell); !ok {
		t.Fatalf("reasoning history = %#v", state.History[0])
	}

	state.RestoreReasoningStatusHeader()
	if state.StatusHeader != "Working" || state.StatusKind != "working" {
		t.Fatalf("restored status = %q %q", state.StatusKind, state.StatusHeader)
	}
}

func TestExtractFirstBoldMatchRustCore(t *testing.T) {
	if got, ok := ExtractFirstBold("pre **Header** tail"); !ok || got != "Header" {
		t.Fatalf("ExtractFirstBold() = %q %v", got, ok)
	}
	if _, ok := ExtractFirstBold("pre **** tail"); ok {
		t.Fatal("empty bold header should be ignored")
	}
	if _, ok := ExtractFirstBold("pre **unterminated"); ok {
		t.Fatal("unterminated bold should be ignored")
	}
}
