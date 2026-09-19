package appserver

import (
	"context"
	"fmt"
	"testing"

	"codex_go/apps"
	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/telemetry"
	"codex_go/tool"
)

// Mirrors Rust #45649's pending MCP elicitation classifications: the first
// classification for a call wins, consuming it reports it once, the queue is
// bounded with oldest-first eviction, and closing a thread drops only its own
// entries.
func TestMCPToolCallElicitationQueueLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})

	router.rememberMCPToolCallElicitation("thread-1", "turn-1", "call-1", telemetry.ElicitationTypeAuthOrLink)
	// A repeated classification for the same call is ignored (first wins).
	router.rememberMCPToolCallElicitation("thread-1", "turn-1", "call-1", telemetry.ElicitationTypeApproval)
	if got := router.takeMCPToolCallElicitation("thread-1", "turn-1", "call-1"); got == nil || *got != telemetry.ElicitationTypeAuthOrLink {
		t.Fatalf("first classification = %#v", got)
	}
	// It is consumed on the first take, exactly like the item completion does.
	if got := router.takeMCPToolCallElicitation("thread-1", "turn-1", "call-1"); got != nil {
		t.Fatalf("the classification was not consumed: %#v", got)
	}
	// An ordinary call never has a classification.
	if got := router.takeMCPToolCallElicitation("thread-1", "turn-1", "call-plain"); got != nil {
		t.Fatalf("ordinary call classification = %#v", got)
	}
	// A classification belongs to one thread, turn and call id.
	router.rememberMCPToolCallElicitation("thread-1", "turn-1", "call-2", telemetry.ElicitationTypeApproval)
	for _, probe := range [][3]string{
		{"thread-2", "turn-1", "call-2"},
		{"thread-1", "turn-2", "call-2"},
		{"thread-1", "turn-1", "call-3"},
	} {
		if got := router.takeMCPToolCallElicitation(probe[0], probe[1], probe[2]); got != nil {
			t.Fatalf("classification leaked to %v: %#v", probe, got)
		}
	}

	// The queue is bounded and evicts the oldest entry.
	for index := 0; index < maxPendingMCPElicitations+1; index++ {
		router.rememberMCPToolCallElicitation("thread-2", "turn-1", fmt.Sprintf("call-%d", index), telemetry.ElicitationTypeApproval)
	}
	if got := router.takeMCPToolCallElicitation("thread-2", "turn-1", "call-0"); got != nil {
		t.Fatalf("the oldest classification should have been evicted: %#v", got)
	}
	if got := router.takeMCPToolCallElicitation("thread-2", "turn-1", fmt.Sprintf("call-%d", maxPendingMCPElicitations)); got == nil {
		t.Fatal("the newest classification should still be queued")
	}

	// Closing a thread drops only that thread's classifications.
	router.rememberMCPToolCallElicitation("thread-3", "turn-1", "call-a", telemetry.ElicitationTypeApproval)
	router.rememberMCPToolCallElicitation("thread-4", "turn-1", "call-b", telemetry.ElicitationTypeApproval)
	router.forgetThreadMCPToolCallElicitations("thread-3")
	if got := router.takeMCPToolCallElicitation("thread-3", "turn-1", "call-a"); got != nil {
		t.Fatalf("closed thread classification = %#v", got)
	}
	if got := router.takeMCPToolCallElicitation("thread-4", "turn-1", "call-b"); got == nil {
		t.Fatal("a sibling thread's classification must survive")
	}
}

// Mirrors Rust #45649/#45716's event fields: an MCP tool call reports the
// connector it resolved to and the classification its producer queued, while an
// ordinary call reports null for both.
func TestMCPToolCallAnalyticsReportsElicitationClassificationLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const threadID = "thread-mcp-elicitation"
	const turnID = "turn-mcp-elicitation"

	item := func(callID string) *ThreadItem {
		return &ThreadItem{
			ID:        callID,
			Type:      "mcpToolCall",
			CallID:    callID,
			CreatedAt: 125000,
			Data: map[string]any{
				"status":         string(CommandExecutionCompleted),
				"startedAtMs":    int64(123000),
				"completedAtMs":  int64(125000),
				"durationMs":     int64(1900),
				"server":         "codex_apps",
				"tool":           "calendar_list_events",
				"connector_id":   "connector_calendar",
				"connector_name": "Calendar",
				"pluginId":       "sample@openai-curated",
				"mcpToolCall":    true,
				"callId":         callID,
			},
		}
	}

	// The trusted connector auth failure was classified before the item completed.
	router.rememberMCPToolCallElicitation(threadID, turnID, "call-auth", telemetry.ElicitationTypeAuthOrLink)
	router.emitMCPToolCallAnalyticsEvent(context.Background(), "conn-model-attribution", threadID, turnID, item("call-auth"), &appTurnRunConfig{})
	classified := waitForMCPToolCallAnalyticsEvent(t, sink, turnID).EventParams
	if classified.ConnectorID == nil || *classified.ConnectorID != "connector_calendar" {
		t.Fatalf("connector id = %#v", classified.ConnectorID)
	}
	if classified.ElicitationType == nil || *classified.ElicitationType != telemetry.ElicitationTypeAuthOrLink {
		t.Fatalf("elicitation type = %#v", classified.ElicitationType)
	}
	// Rust #45716: the same classification reaches the app-usage event.
	appUsed := waitForAppUsedEvent(t, sink).EventParams
	if appUsed.ConnectorID == nil || *appUsed.ConnectorID != "connector_calendar" ||
		appUsed.AppName == nil || *appUsed.AppName != "Calendar" {
		t.Fatalf("app-used identity = %#v", appUsed)
	}
	if appUsed.ElicitationType == nil || *appUsed.ElicitationType != telemetry.ElicitationTypeAuthOrLink {
		t.Fatalf("app-used classification = %#v", appUsed.ElicitationType)
	}

	// An ordinary call reports null for both, and the classification cannot be
	// reused by another call.
	router.emitMCPToolCallAnalyticsEvent(context.Background(), "conn-model-attribution", threadID, turnID, item("call-plain"), &appTurnRunConfig{})
	plain := waitForMCPToolCallAnalyticsEvent(t, sink, turnID).EventParams
	if plain.ItemID != "call-plain" || plain.ConnectorID == nil || *plain.ConnectorID != "connector_calendar" {
		t.Fatalf("plain call = %#v", plain)
	}
	if plain.ElicitationType != nil {
		t.Fatalf("plain call elicitation type = %#v", plain.ElicitationType)
	}
}

// Mirrors Rust #45716's approval classification: a denied, aborted or failed
// approval queues `approval` for the call, while an approved call queues nothing.
func TestMCPToolCallApprovalClassificationLikeRust(t *testing.T) {
	sessionKey := mcp.MCPToolApprovalKey{Server: "docs", Tool: "search"}
	run := func(t *testing.T, action MCPElicitationAction) *RuntimeRouter {
		t.Helper()
		router := NewRuntimeRouter(RuntimeServices{})
		t.Cleanup(func() { _ = router.Close() })
		router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
			router.requireServerRequests().Resolve(OK(request.ID, &MCPElicitationRequestResponse{Action: action}))
		}))
		handler := &appserverMCPToolApprovalHandler{
			router:                    router,
			responder:                 func(context.Context, *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) { return nil, nil },
			threadID:                  "thread-approval",
			turnID:                    "turn-approval",
			approvalPolicy:            sandbox.ApprovalOnRequest,
			persistentApprovalAllowed: true,
			elicitationEnabled:        true,
			allowUserInteraction:      true,
		}
		outcome, err := handler.ApproveMCPToolCall(context.Background(), &mcp.MCPToolApprovalRequest{
			Server:       "docs",
			Tool:         "search",
			ApprovalMode: apps.AppToolApprovalAuto,
			CallID:       "call-approval",
			SessionKey:   &sessionKey,
		})
		if err != nil {
			t.Fatalf("ApproveMCPToolCall() error = %v", err)
		}
		if outcome.Decision != expectedApprovalDecision(action) {
			t.Fatalf("decision = %v", outcome.Decision)
		}
		return router
	}

	for _, testCase := range []struct {
		action MCPElicitationAction
		denied bool
	}{
		{action: MCPElicitationActionDecline, denied: true},
		{action: MCPElicitationActionCancel, denied: true},
		{action: MCPElicitationActionAccept, denied: false},
	} {
		router := run(t, testCase.action)
		classification := router.takeMCPToolCallElicitation("thread-approval", "turn-approval", "call-approval")
		if testCase.denied {
			if classification == nil || *classification != telemetry.ElicitationTypeApproval {
				t.Fatalf("%s classification = %#v", testCase.action, classification)
			}
			continue
		}
		if classification != nil {
			t.Fatalf("%s must not classify the call: %#v", testCase.action, classification)
		}
	}
}

func expectedApprovalDecision(action MCPElicitationAction) mcp.MCPToolApprovalDecision {
	switch action {
	case MCPElicitationActionAccept:
		return mcp.MCPToolApprovalApprove
	case MCPElicitationActionDecline:
		return mcp.MCPToolApprovalReject
	default:
		return mcp.MCPToolApprovalDeny
	}
}
