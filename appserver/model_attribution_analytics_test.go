package appserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/plugin"
	"codex_go/session"
	"codex_go/telemetry"
	"codex_go/turn"
)

// newModelAttributionRouter builds a router whose analytics sink records every
// event family these tests assert, with one initialized client connection.
func newModelAttributionRouter(t *testing.T) (*RuntimeRouter, *recordingTurnEventSink) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`model = "gpt-5"`), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := newRecordingTurnEventSink()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:          NewRouter(store),
		Config:                config.NewConfigService(home),
		Turns:                 turn.NewTurnService(),
		ThreadStatus:          NewThreadStatusManager(),
		Analytics:             sink,
		AnalyticsRPCTransport: telemetry.AppServerRPCTransportInProcess,
	})
	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex-tui", Version: "1.2.3"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	})
	initialize.ConnectionID = "conn-model-attribution"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	return router, sink
}

// Mirrors Rust #45445's reducer rule: a command execution is attributed to the
// step that invoked it, and a repeated start notification keeps the first model
// context, so a model switch while the command runs cannot retarget the event.
func TestCommandExecutionAnalyticsUsesTheInvokingModelLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	threadID := "thread-model-attribution"
	turnID := "turn-model-attribution"
	itemID := "cmd-model-attribution"

	router.rememberToolItemModelContext(threadID, turnID, itemID, modelInvocationContext{ModelSlug: "invoking-model", ReasoningEffort: "max"})
	// A later start notification (or a swapped step model) must not replace it.
	router.rememberToolItemModelContext(threadID, turnID, itemID, modelInvocationContext{ModelSlug: "later-model", ReasoningEffort: "low"})

	item := &ThreadItem{
		ID:        itemID,
		Type:      "commandExecution",
		CallID:    itemID,
		CreatedAt: 125000,
		Data: map[string]any{
			"status":                 string(CommandExecutionCompleted),
			"startedAtMs":            int64(123000),
			"completedAtMs":          int64(125000),
			"command":                "echo hi",
			"commandActions":         []any{},
			"aggregatedOutput":       "hi",
			"commandSource":          "agent",
			"durationMs":             int64(1900),
			"exitCode":               int64(0),
			"commandExecutionSource": "agent",
		},
	}
	// The completion-time config is a different model: the invoking model wins.
	router.emitCommandExecutionAnalyticsEvent(context.Background(), "conn-model-attribution", threadID, turnID, item, &appTurnRunConfig{
		Model:           "later-model",
		ReasoningEffort: "low",
	})
	event := waitForCommandExecutionAnalyticsEvent(t, sink, turnID)
	params := event.EventParams
	if params.ModelSlug == nil || *params.ModelSlug != "invoking-model" {
		t.Fatalf("model_slug = %#v, want the invoking model", params.ModelSlug)
	}
	if params.ReasoningEffort == nil || *params.ReasoningEffort != "max" {
		t.Fatalf("reasoning_effort = %#v, want max", params.ReasoningEffort)
	}

	// A command whose start was not observed falls back to the completion-time
	// resolved settings rather than reporting nothing.
	unobserved := *item
	unobserved.ID = "cmd-unobserved"
	unobserved.CallID = "cmd-unobserved"
	router.emitCommandExecutionAnalyticsEvent(context.Background(), "conn-model-attribution", threadID, turnID, &unobserved, &appTurnRunConfig{
		Model:           "later-model",
		ReasoningEffort: "low",
	})
	fallback := waitForCommandExecutionAnalyticsEvent(t, sink, turnID)
	if fallback.EventParams.ModelSlug == nil || *fallback.EventParams.ModelSlug != "later-model" {
		t.Fatalf("fallback model_slug = %#v", fallback.EventParams.ModelSlug)
	}
}

// Mirrors Rust #45445's plugin measurement half: the batch carries the model that
// invoked the measured command, and the emitted event repeats it.
func TestPluginMeasurementEventCarriesTheInvokingModelLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	threadID := "thread-plugin-measurement"
	turnID := "turn-plugin-measurement"
	batch := plugin.PluginMeasurementBatch{
		PluginID:        "sample@openai-curated",
		ExecutionID:     "execution-1",
		Operation:       "security_scan",
		ModelSlug:       "invoking-model",
		ReasoningEffort: "max",
		Rows: []plugin.PluginMeasurementRow{{
			MeasurementName: "issues_found",
			NumberValue:     3,
		}},
	}
	router.emitPluginMeasurements(context.Background(), threadID, turnID, batch)

	select {
	case input := <-sink.pluginMeasurements:
		if input.ThreadID != threadID || input.TurnID != turnID || input.PluginID != "sample@openai-curated" || input.Operation != "security_scan" {
			t.Fatalf("plugin measurement input = %#v", input)
		}
		if input.ModelSlug == nil || *input.ModelSlug != "invoking-model" {
			t.Fatalf("model_slug = %#v, want the invoking model", input.ModelSlug)
		}
		if input.ReasoningEffort == nil || *input.ReasoningEffort != "max" {
			t.Fatalf("reasoning_effort = %#v, want max", input.ReasoningEffort)
		}
	default:
		t.Fatal("no plugin measurement event was recorded")
	}

	// A batch without a captured context reports absent labels.
	router.emitPluginMeasurements(context.Background(), threadID, turnID, plugin.PluginMeasurementBatch{
		PluginID:    "sample@openai-curated",
		ExecutionID: "execution-2",
		Operation:   "security_scan",
		Rows: []plugin.PluginMeasurementRow{{
			MeasurementName: "issues_found",
			NumberValue:     1,
		}},
	})
	select {
	case input := <-sink.pluginMeasurements:
		if input.ModelSlug != nil || input.ReasoningEffort != nil {
			t.Fatalf("unlabelled measurement = %#v", input)
		}
	default:
		t.Fatal("no second plugin measurement event was recorded")
	}
}
