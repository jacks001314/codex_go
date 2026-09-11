package chatwidget

import (
	"strings"
	"testing"
	"time"

	historycell "codex_go/tui/history_cell"
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
	// Rust #43921 trims only a leading `**bold**` span, so an inline span keeps
	// the whole line.
	if state.StatusKind != "thinking" || state.StatusHeader != "thinking **Inspecting files** and more" {
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
	// Rust #43921: the last useful summary is retained through finalization and
	// restore instead of falling back to "Working".
	if state.StatusHeader != "next block" || state.StatusKind != "thinking" {
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

func TestLatestSummaryLineMatchRustCore(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{name: "empty", text: "", ok: false},
		{name: "comments only", text: "<!-- thinking -->\n\n", ok: false},
		{name: "bold header", text: "**Planning the change**", want: "Planning the change", ok: true},
		{name: "bold with suffix", text: "**Planning** the change", want: "Planning the change", ok: true},
		{name: "heading marks", text: "### Step two", want: "Step two", ok: true},
		{name: "last usable line", text: "first line\n\nlast line", want: "last line", ok: true},
		{name: "unterminated bold falls back", text: "**unterminated\nusable line", want: "usable line", ok: true},
		{name: "empty bold skipped", text: "****\nusable", want: "usable", ok: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := LatestSummaryLine(tc.text)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("LatestSummaryLine(%q) = %q %v, want %q %v", tc.text, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestReasoningStatusHeaderRetainsLatestSummary(t *testing.T) {
	state := NewChatStreamingState(40)
	state.TaskRunning = true

	state.OnAgentReasoningDelta("**Step one**")
	if state.StatusHeader != "Step one" || state.StatusKind != "thinking" {
		t.Fatalf("delta status = %q %q", state.StatusKind, state.StatusHeader)
	}
	state.OnAgentReasoningDelta("\n## Step two")
	if state.StatusHeader != "Step two" {
		t.Fatalf("second delta status = %q", state.StatusHeader)
	}
	// A later non-usable line must not clear the heading.
	state.OnAgentReasoningDelta("\n<!-- internal note -->")
	if state.StatusHeader != "Step two" {
		t.Fatalf("comment cleared header = %q", state.StatusHeader)
	}
	// Finalizing keeps the last useful summary through later tool activity.
	state.OnAgentReasoningFinal()
	if state.StatusHeader != "Step two" || state.StatusKind != "thinking" {
		t.Fatalf("final status = %q %q", state.StatusKind, state.StatusHeader)
	}
}

func TestRestoreReasoningStatusHeaderKeepsPrevious(t *testing.T) {
	state := NewChatStreamingState(40)
	state.TaskRunning = true
	state.StatusHeader = "Earlier heading"
	state.StatusKind = "thinking"
	state.RestoreReasoningStatusHeader()
	if state.StatusHeader != "Earlier heading" || state.StatusKind != "thinking" {
		t.Fatalf("restore kept header = %q %q", state.StatusKind, state.StatusHeader)
	}

	empty := NewChatStreamingState(40)
	empty.TaskRunning = true
	empty.RestoreReasoningStatusHeader()
	if empty.StatusHeader != "Working" || empty.StatusKind != "working" {
		t.Fatalf("restore fallback = %q %q", empty.StatusKind, empty.StatusHeader)
	}
}
