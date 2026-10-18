package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/tool"
	"codex_go/turn"
)

func TestToolItemAnalyticsEventsCarryRootTurnIDLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-root-tools"
		turnID       = "turn-root-tools"
		rootTurnID   = "root-ancestor"
	)
	runConfig := &appTurnRunConfig{RootTurnID: rootTurnID}
	ctx := context.Background()

	command := &ThreadItem{
		ID:     "cmd-root",
		Type:   "commandExecution",
		CallID: "cmd-root",
		Data:   map[string]any{"status": string(CommandExecutionCompleted), "command": "echo root"},
	}
	router.emitCommandExecutionAnalyticsEvent(ctx, connectionID, threadID, turnID, command, runConfig)
	assertToolEventRoot(t, waitForCommandExecutionAnalyticsEvent(t, sink, turnID).EventParams.RootTurnID, rootTurnID)

	fileChange := &ThreadItem{
		ID:   "patch-root",
		Type: "fileChange",
		Data: map[string]any{"status": string(PatchApplyCompleted)},
	}
	router.emitFileChangeAnalyticsEvent(ctx, connectionID, threadID, turnID, fileChange, runConfig)
	assertToolEventRoot(t, waitForFileChangeAnalyticsEvent(t, sink, turnID).EventParams.RootTurnID, rootTurnID)

	mcpCall := &ThreadItem{
		ID:   "mcp-root",
		Type: "mcpToolCall",
		Data: map[string]any{"status": "completed", "server": "docs", "tool": "lookup"},
	}
	router.emitMCPToolCallAnalyticsEvent(ctx, connectionID, threadID, turnID, mcpCall, runConfig)
	assertToolEventRoot(t, waitForMCPToolCallAnalyticsEvent(t, sink, turnID).EventParams.RootTurnID, rootTurnID)

	dynamicCall := &ThreadItem{
		ID:   "dynamic-root",
		Type: "dynamicToolCall",
		Data: map[string]any{"status": "completed", "tool": "lookup"},
	}
	router.emitDynamicToolCallAnalyticsEvent(ctx, connectionID, threadID, turnID, dynamicCall, runConfig)
	assertToolEventRoot(t, waitForDynamicToolCallAnalyticsEvent(t, sink, turnID).EventParams.RootTurnID, rootTurnID)

	webSearch := &ThreadItem{ID: "search-root", Type: "webSearch", Data: map[string]any{}}
	router.emitWebSearchAnalyticsEvent(ctx, connectionID, threadID, turnID, webSearch, runConfig)
	assertToolEventRoot(t, waitForWebSearchAnalyticsEvent(t, sink, turnID).EventParams.RootTurnID, rootTurnID)

	imageGeneration := &ThreadItem{
		ID:     "image-root",
		Type:   "imageGeneration",
		Status: "completed",
		Data:   map[string]any{"status": "completed"},
	}
	router.emitImageGenerationAnalyticsEvent(ctx, connectionID, threadID, turnID, imageGeneration, runConfig)
	assertToolEventRoot(t, waitForImageGenerationAnalyticsEvent(t, sink, turnID).EventParams.RootTurnID, rootTurnID)
}

func TestAnalyticsRootTurnIDReadsLiveTurnMetadataLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-live-root"
		turnID       = "turn-live-root"
		rootTurnID   = "root-ancestor"
	)
	params := &turn.TurnStartParams{ThreadID: threadID}
	if err := router.threads.RegisterTurn(threadID, turnID, nil, time.Now().UTC().UnixMilli(), params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	runConfig := &appTurnRunConfig{SessionID: threadID, RootTurnID: rootTurnID}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, connectionID, runConfig)

	item := &ThreadItem{
		ID:     "cmd-before-invalidation",
		Type:   "commandExecution",
		CallID: "cmd-before-invalidation",
		Data:   map[string]any{"status": string(CommandExecutionCompleted), "command": "echo root"},
	}
	staleConfig := &appTurnRunConfig{RootTurnID: "stale-root"}
	router.emitCommandExecutionAnalyticsEvent(context.Background(), connectionID, threadID, turnID, item, staleConfig)
	assertToolEventRoot(t, waitForCommandExecutionAnalyticsEvent(t, sink, turnID).EventParams.RootTurnID, rootTurnID)

	router.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
		active.AnalyticsRootTurnID = nil
		active.AnalyticsRootTurnIDCaptured = true
	})

	item.ID = "cmd-after-invalidation"
	item.CallID = item.ID
	router.emitCommandExecutionAnalyticsEvent(context.Background(), connectionID, threadID, turnID, item, staleConfig)
	assertToolEventRoot(t, waitForCommandExecutionAnalyticsEvent(t, sink, turnID).EventParams.RootTurnID, "")

	startedAt := time.Now().UTC()
	record := &turn.TurnRecord{ID: turnID, ThreadID: threadID, StartedAt: startedAt.Unix()}
	router.emitCodexTurnAnalyticsEvent(
		context.Background(),
		connectionID,
		params,
		record,
		runConfig,
		nil,
		TurnStatusCompleted,
		startedAt,
		startedAt.Add(time.Second),
		1000,
		0,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	assertToolEventRoot(t, waitForCodexTurnEvent(t, sink, turnID).EventParams.RootTurnID, "")

	if _, ok := router.threads.ConsumeTurn(threadID, turnID, false); !ok {
		t.Fatal("ConsumeTurn() did not find the active turn")
	}
	background := &ThreadItem{
		ID:     "cmd-background",
		Type:   "commandExecution",
		CallID: "cmd-background",
		Data:   map[string]any{"status": string(CommandExecutionCompleted), "command": "echo background"},
	}
	backgroundConfig := &appTurnRunConfig{RootTurnID: "root-background"}
	router.emitCommandExecutionAnalyticsEvent(context.Background(), connectionID, threadID, turnID, background, backgroundConfig)
	assertToolEventRoot(t, waitForCommandExecutionAnalyticsEvent(t, sink, turnID).EventParams.RootTurnID, "root-background")
}

func TestQueuedToolEventDropsInvalidatedRootLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		connectionID = "conn-model-attribution"
		threadID     = "thread-queued-root"
		turnID       = "turn-queued-root"
		rootTurnID   = "root-queued"
	)
	params := &turn.TurnStartParams{ThreadID: threadID}
	if err := router.threads.RegisterTurn(threadID, turnID, nil, time.Now().UTC().UnixMilli(), params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, connectionID, &appTurnRunConfig{RootTurnID: rootTurnID})

	router.rememberCodeModeCell(threadID, turnID, "cell-1", "exec-1")
	router.rememberSampledToolCalls(threadID, turnID, "response-1", []string{"exec-1"})
	router.emitCodeModeToolCallEvent(threadID, turnID, tool.CodeModeCallObservation{
		Kind:          tool.CodeModeCallCompleted,
		CellID:        "cell-1",
		CallID:        "exec-1",
		ToolName:      tool.CodeModeExecToolName,
		StartedAtMS:   1000,
		CompletedAtMS: 1010,
		Status:        tool.CodeModeCallStatusCompleted,
	})
	select {
	case event := <-sink.dynamicToolCall:
		t.Fatalf("correlated event emitted before a later response: %#v", event)
	default:
	}

	router.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
		active.AnalyticsRootTurnID = nil
		active.AnalyticsRootTurnIDCaptured = true
	})
	router.rememberSampledToolCalls(threadID, turnID, "response-2", nil)

	event := waitForDynamicToolCallAnalyticsEvent(t, sink, turnID)
	assertToolEventRoot(t, event.EventParams.RootTurnID, "")
	if event.EventParams.SubsequentResponseID == nil || *event.EventParams.SubsequentResponseID != "response-2" {
		t.Fatalf("subsequent response = %#v, want response-2", event.EventParams.SubsequentResponseID)
	}
}

func assertToolEventRoot(t *testing.T, got *string, want string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Fatalf("root_turn_id = %q, want null", *got)
		}
		return
	}
	if got == nil || *got != want {
		t.Fatalf("root_turn_id = %#v, want %q", got, want)
	}
}
