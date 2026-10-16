package appserver

import (
	"context"
	"testing"
)

// Rust never derives a tool item completion time from the item timestamp:
// completed_at_ms comes from the completion notification
// (analytics/src/reducer.rs:2079-2093) and started_at_ms from the matching
// ItemStarted notification. The Go app-server item path keeps both on the item
// data (appTimingMetadata writes Unix milliseconds, unifiedExecThreadItem does
// the same), but an item without timing metadata still carries
// ThreadItem.CreatedAt, which BuildThreadItem fills through unixOrZero -- unix
// SECONDS. The fallback that fills completedAtMS from CreatedAt therefore has
// to convert seconds to milliseconds; writing raw seconds into a *MS field
// understates the timestamp by 1000x.
const analyticsFallbackCreatedAtSeconds = int64(1_700_000_000)

const analyticsFallbackCompletedAtMS = uint64(analyticsFallbackCreatedAtSeconds) * 1000

func TestCommandExecutionAnalyticsFallbackUsesMillisecondsLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		threadID = "thread-analytics-unit-fallback"
		turnID   = "turn-analytics-unit-fallback"
	)

	// A legacy/rollout item: no timing metadata at all, seconds-based CreatedAt.
	legacy := &ThreadItem{
		ID:        "cmd-unit-fallback",
		Type:      "commandExecution",
		CallID:    "cmd-unit-fallback",
		CreatedAt: analyticsFallbackCreatedAtSeconds,
		Data: map[string]any{
			"status":         string(CommandExecutionCompleted),
			"command":        "echo hi",
			"commandActions": []any{},
		},
	}
	router.emitCommandExecutionAnalyticsEvent(context.Background(), "conn-model-attribution", threadID, turnID, legacy, &appTurnRunConfig{})
	params := waitForCommandExecutionAnalyticsEvent(t, sink, turnID).EventParams
	if params.CompletedAtMS != analyticsFallbackCompletedAtMS {
		t.Fatalf("completedAtMs = %d, want %d (the seconds CreatedAt fallback must be converted to milliseconds)", params.CompletedAtMS, analyticsFallbackCompletedAtMS)
	}
	if params.StartedAtMS != 0 {
		t.Fatalf("startedAtMs = %d, want 0 (the item carries no start metadata)", params.StartedAtMS)
	}
	if params.DurationMS == nil || *params.DurationMS != analyticsFallbackCompletedAtMS {
		t.Fatalf("durationMs = %#v, want %d", params.DurationMS, analyticsFallbackCompletedAtMS)
	}

	// An item that already carries completedAtMs keeps its exact value: the
	// fallback must not rescale milliseconds.
	timed := &ThreadItem{
		ID:        "cmd-unit-explicit",
		Type:      "commandExecution",
		CallID:    "cmd-unit-explicit",
		CreatedAt: analyticsFallbackCreatedAtSeconds,
		Data: map[string]any{
			"status":         string(CommandExecutionCompleted),
			"startedAtMs":    int64(1_700_000_000_500),
			"completedAtMs":  int64(1_700_000_001_250),
			"command":        "echo hi",
			"commandActions": []any{},
		},
	}
	router.emitCommandExecutionAnalyticsEvent(context.Background(), "conn-model-attribution", threadID, turnID, timed, &appTurnRunConfig{})
	timedParams := waitForCommandExecutionAnalyticsEvent(t, sink, turnID).EventParams
	if timedParams.StartedAtMS != 1_700_000_000_500 || timedParams.CompletedAtMS != 1_700_000_001_250 {
		t.Fatalf("explicit timing = started:%d completed:%d, want 1700000000500/1700000001250", timedParams.StartedAtMS, timedParams.CompletedAtMS)
	}
}

// The same fallback is duplicated in the file-change, MCP tool call, dynamic
// tool call and shared tool-item emitters; pin all of them.
func TestToolItemAnalyticsFallbacksUseMillisecondsLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const (
		threadID = "thread-analytics-unit-fallback-shared"
		turnID   = "turn-analytics-unit-fallback-shared"
	)
	ctx := context.Background()

	fileChange := &ThreadItem{ID: "patch-unit-fallback", Type: "fileChange", CreatedAt: analyticsFallbackCreatedAtSeconds, Data: map[string]any{"status": string(PatchApplyCompleted)}}
	router.emitFileChangeAnalyticsEvent(ctx, "conn-model-attribution", threadID, turnID, fileChange, &appTurnRunConfig{})
	if got := waitForFileChangeAnalyticsEvent(t, sink, turnID).EventParams.CompletedAtMS; got != analyticsFallbackCompletedAtMS {
		t.Fatalf("file change completedAtMs = %d, want %d", got, analyticsFallbackCompletedAtMS)
	}

	mcpCall := &ThreadItem{ID: "mcp-unit-fallback", Type: "mcpToolCall", CreatedAt: analyticsFallbackCreatedAtSeconds, Data: map[string]any{"status": "completed", "server": "docs", "tool": "lookup"}}
	router.emitMCPToolCallAnalyticsEvent(ctx, "conn-model-attribution", threadID, turnID, mcpCall, &appTurnRunConfig{})
	if got := waitForMCPToolCallAnalyticsEvent(t, sink, turnID).EventParams.CompletedAtMS; got != analyticsFallbackCompletedAtMS {
		t.Fatalf("mcp tool call completedAtMs = %d, want %d", got, analyticsFallbackCompletedAtMS)
	}

	dynamic := &ThreadItem{ID: "dyn-unit-fallback", Type: "dynamicToolCall", CreatedAt: analyticsFallbackCreatedAtSeconds, Data: map[string]any{"status": "completed", "tool": "lookup"}}
	router.emitDynamicToolCallAnalyticsEvent(ctx, "conn-model-attribution", threadID, turnID, dynamic, &appTurnRunConfig{})
	if got := waitForDynamicToolCallAnalyticsEvent(t, sink, turnID).EventParams.CompletedAtMS; got != analyticsFallbackCompletedAtMS {
		t.Fatalf("dynamic tool call completedAtMs = %d, want %d", got, analyticsFallbackCompletedAtMS)
	}

	search := &ThreadItem{ID: "search-unit-fallback", Type: "webSearch", CreatedAt: analyticsFallbackCreatedAtSeconds, Data: map[string]any{}}
	router.emitWebSearchAnalyticsEvent(ctx, "conn-model-attribution", threadID, turnID, search, &appTurnRunConfig{})
	if got := waitForWebSearchAnalyticsEvent(t, sink, turnID).EventParams.CompletedAtMS; got != analyticsFallbackCompletedAtMS {
		t.Fatalf("web search completedAtMs = %d, want %d", got, analyticsFallbackCompletedAtMS)
	}
}
