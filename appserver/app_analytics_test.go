package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/telemetry"
	"codex_go/turn"
)

func waitForAppMentionedEvent(t *testing.T, sink *recordingTurnEventSink) telemetry.CodexAppMentionedEventRequest {
	t.Helper()
	select {
	case event := <-sink.appMentioned:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for codex_app_mentioned")
		return telemetry.CodexAppMentionedEventRequest{}
	}
}

func waitForAppUsedEvent(t *testing.T, sink *recordingTurnEventSink) telemetry.CodexAppUsedEventRequest {
	t.Helper()
	select {
	case event := <-sink.appUsed:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for codex_app_used")
		return telemetry.CodexAppUsedEventRequest{}
	}
}

// Mirrors Rust #45716's turn-input tracking: the connectors the input named
// explicitly join the thread's selection - which is what later decides whether an
// app call is explicit or implicit - and each mention is reported.
func TestExplicitAppMentionsPopulateTheSelectionLikeRust(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const threadID = "thread-app-mentions"
	const turnID = "turn-app-mentions"
	params := &turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "check my calendar",
		Input: []turn.TurnUserInput{
			{Type: "mention", Path: "app://connector_calendar"},
			{Type: "mention", Path: "plugin://sample@openai-curated"},
		},
	}
	router.rememberExplicitAppMentions(context.Background(), threadID, turnID, "conn-model-attribution", params, &config.Config{Values: map[string]any{}}, "gpt-5")

	event := waitForAppMentionedEvent(t, sink).EventParams
	if event.ConnectorID == nil || *event.ConnectorID != "connector_calendar" {
		t.Fatalf("mentioned connector = %#v", event.ConnectorID)
	}
	if event.InvokeType == nil || *event.InvokeType != telemetry.InvocationTypeExplicit {
		t.Fatalf("mention invoke type = %#v", event.InvokeType)
	}
	if event.ThreadID == nil || *event.ThreadID != threadID || event.TurnID == nil || *event.TurnID != turnID {
		t.Fatalf("mention identity = %#v", event)
	}
	if event.ModelSlug == nil || *event.ModelSlug != "gpt-5" {
		t.Fatalf("mention model = %#v", event.ModelSlug)
	}
	// The plugin mention is not an app mention.
	select {
	case extra := <-sink.appMentioned:
		t.Fatalf("unexpected second mention event: %#v", extra)
	default:
	}

	selection := router.sessionStateForThread(threadID).MergeConnectorSelection()
	if len(selection) != 1 || selection[0] != "connector_calendar" {
		t.Fatalf("connector selection = %#v", selection)
	}
}

// Mirrors Rust's `maybe_track_codex_app_used` with its dedup and `invoke_type`:
// one event per turn and connector (the first keeps its classification), an
// explicitly selected connector is explicit, and a call without a connector is
// always reported.
func TestCodexAppUsedEventFollowsRustDedupAndSelection(t *testing.T) {
	router, sink := newModelAttributionRouter(t)
	const threadID = "thread-app-used"
	const turnID = "turn-app-used"
	classification := telemetry.ElicitationTypeAuthOrLink

	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID, "conn-model-attribution", "connector_calendar", "Calendar", "gpt-5", &classification)
	first := waitForAppUsedEvent(t, sink).EventParams
	if first.ConnectorID == nil || *first.ConnectorID != "connector_calendar" || first.AppName == nil || *first.AppName != "Calendar" {
		t.Fatalf("app identity = %#v", first)
	}
	if first.InvokeType == nil || *first.InvokeType != telemetry.InvocationTypeImplicit {
		t.Fatalf("unselected connector invoke type = %#v", first.InvokeType)
	}
	if first.ElicitationType == nil || *first.ElicitationType != telemetry.ElicitationTypeAuthOrLink {
		t.Fatalf("classification = %#v", first.ElicitationType)
	}
	if first.ModelSlug == nil || *first.ModelSlug != "gpt-5" {
		t.Fatalf("model = %#v", first.ModelSlug)
	}

	// The same turn and connector is deduplicated, another turn is not, and a
	// call without a connector is always reported.
	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID, "conn-model-attribution", "connector_calendar", "Calendar", "gpt-5", &classification)
	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID, "conn-model-attribution", "connector_drive", "Drive", "gpt-5", nil)
	drive := waitForAppUsedEvent(t, sink).EventParams
	if drive.ConnectorID == nil || *drive.ConnectorID != "connector_drive" || drive.ElicitationType != nil {
		t.Fatalf("second connector = %#v", drive)
	}
	router.emitCodexAppUsedEvent(context.Background(), threadID, turnID, "conn-model-attribution", "", "", "gpt-5", nil)
	unbound := waitForAppUsedEvent(t, sink).EventParams
	if unbound.ConnectorID != nil {
		t.Fatalf("unbound call = %#v", unbound)
	}
	router.emitCodexAppUsedEvent(context.Background(), threadID, "turn-other", "conn-model-attribution", "connector_calendar", "Calendar", "gpt-5", nil)
	other := waitForAppUsedEvent(t, sink).EventParams
	if other.TurnID == nil || *other.TurnID != "turn-other" {
		t.Fatalf("other turn = %#v", other)
	}
	select {
	case extra := <-sink.appUsed:
		t.Fatalf("an extra app-used event was emitted: %#v", extra)
	default:
	}

	// A connector the turn named explicitly is reported as explicit.
	router.sessionStateForThread(threadID).MergeConnectorSelection("connector_drive")
	router.emitCodexAppUsedEvent(context.Background(), threadID, "turn-explicit", "conn-model-attribution", "connector_drive", "Drive", "gpt-5", nil)
	explicit := waitForAppUsedEvent(t, sink).EventParams
	if explicit.InvokeType == nil || *explicit.InvokeType != telemetry.InvocationTypeExplicit {
		t.Fatalf("explicit invoke type = %#v", explicit.InvokeType)
	}
}
