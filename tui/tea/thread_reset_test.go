package tea

import (
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// seedThreadScopedState fills every transcript-scoped field the thread reset
// must clear (Rust #43994).
func seedThreadScopedState(model *Model) {
	model.toolCalls["call-1"] = &toolCallDisplayState{ID: "call-1", ToolName: "exec"}
	model.mcpToolCalls["mcp-1"] = &mcpToolCallDisplayState{ID: "mcp-1"}
	model.startedThreadIDs["thread-a"] = true
	model.completedThreadIDs["thread-a"] = true
	model.Transcript.lastTurnError = "boom"
	model.Transcript.needsFinalMessageSeparator = true
	model.Transcript.activeAssistantDeltaItemID = "delta-1"
	model.renderedFileChanges["call-1"] = true
	model.retryMessageIndex = 3
	model.retryActivityActive = true
	model.retryActivityMessage = "retrying"
	model.compactionActive = true
	model.compactionID = "compact-1"
	model.toolRequestRuntime = chatwidget.ToolRequestRuntimeState{}
	model.pendingSteers = []pendingSteerSubmission{{ID: "steer-1"}}
	model.rejectedSteers = []queuedSubmission{{Request: SubmitRequest{Prompt: "rejected"}}}
	model.overlayAltScreen = true
	model.overlayTranscript = true
}

func assertThreadScopedStateCleared(t *testing.T, model *Model) {
	t.Helper()
	if len(model.toolCalls) != 0 {
		t.Fatalf("toolCalls = %#v, want cleared", model.toolCalls)
	}
	if len(model.mcpToolCalls) != 0 {
		t.Fatalf("mcpToolCalls = %#v, want cleared", model.mcpToolCalls)
	}
	if len(model.startedThreadIDs) != 0 || len(model.completedThreadIDs) != 0 {
		t.Fatalf("thread id tracking = %#v / %#v", model.startedThreadIDs, model.completedThreadIDs)
	}
	if model.Transcript.lastTurnError != "" ||
		model.Transcript.needsFinalMessageSeparator ||
		model.Transcript.activeAssistantDeltaItemID != "" {
		t.Fatalf("transcript flags = %#v", model.Transcript)
	}
	if len(model.renderedFileChanges) != 0 {
		t.Fatalf("renderedFileChanges = %#v, want cleared", model.renderedFileChanges)
	}
	if model.retryMessageIndex != -1 || model.retryActivityActive || model.retryActivityMessage != "" {
		t.Fatalf("retry state = (%d, %v, %q)", model.retryMessageIndex, model.retryActivityActive, model.retryActivityMessage)
	}
	if model.compactionActive || model.compactionID != "" {
		t.Fatalf("compaction state = (%v, %q)", model.compactionActive, model.compactionID)
	}
	if len(model.pendingSteers) != 0 || len(model.rejectedSteers) != 0 {
		t.Fatalf("steer state = %#v / %#v", model.pendingSteers, model.rejectedSteers)
	}
	if model.overlayAltScreen || model.overlayTranscript || model.overlay != nil {
		t.Fatal("transcript overlay state must be cleared")
	}
}

func newThreadResetModel() *Model {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.State.SetThreadID("thread-a")
	model.State.AddMessage(codextui.RoleAssistant, "thread A content")
	seedThreadScopedState(model)
	return model
}

// TestThreadStartedResetsTranscriptState covers the app-server thread
// replacement path (Rust #43994).
func TestThreadStartedResetsTranscriptState(t *testing.T) {
	model := newThreadResetModel()
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "thread.started", ThreadID: "thread-b"}})
	if model.State.ThreadID != "thread-b" {
		t.Fatalf("thread = %q", model.State.ThreadID)
	}
	assertThreadScopedStateCleared(t, model)
}

// TestResumeResponseResetsTranscriptState covers resuming/forking a session.
func TestResumeResponseResetsTranscriptState(t *testing.T) {
	model := newThreadResetModel()
	model.applyResumeResponse("thread-b", SessionResumeResponse{
		Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "thread B content"}},
	})
	assertThreadScopedStateCleared(t, model)
	joined := modelMessageText(model)
	if joined != "thread B content" {
		t.Fatalf("transcript = %q, want only the resumed thread", joined)
	}
}

// TestAgentSwitchResetsTranscriptState covers switching to a background agent.
func TestAgentSwitchResetsTranscriptState(t *testing.T) {
	model := newThreadResetModel()
	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "thread-b",
		Response: AgentThreadSwitchResponse{
			Entry:    codextui.AgentThreadEntry{ThreadID: "thread-b", AgentNickname: "Scout"},
			Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "agent B content"}},
			Status:   "idle",
		},
	})
	assertThreadScopedStateCleared(t, model)
}

// TestFreshSessionResetsTranscriptState covers /new and /clear.
func TestFreshSessionResetsTranscriptState(t *testing.T) {
	model := newThreadResetModel()
	model.startFreshNamedSession("", "Started a new local thread.")
	assertThreadScopedStateCleared(t, model)
}
