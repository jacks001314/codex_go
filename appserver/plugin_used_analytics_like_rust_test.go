package appserver

import (
	"context"
	"testing"

	"codex_go/config"
	"codex_go/plugin"
	"codex_go/telemetry"
	"codex_go/turn"
)

type recordingPluginUsedSink struct {
	*recordingTurnEventSink
	events chan telemetry.CodexPluginUsedEventRequest
}

func newRecordingPluginUsedSink() *recordingPluginUsedSink {
	return &recordingPluginUsedSink{
		recordingTurnEventSink: newRecordingTurnEventSink(),
		events:                 make(chan telemetry.CodexPluginUsedEventRequest, 4),
	}
}

func (s *recordingPluginUsedSink) TrackCodexPluginUsedEvent(_ context.Context, event telemetry.CodexPluginUsedEventRequest) {
	if s == nil || s.events == nil {
		return
	}
	s.events <- event
}

// Rust core/src/session/turn.rs tracks one codex_plugin_used event per plugin
// the turn input named explicitly, carrying the capability summary's identity,
// MCP server names, connector ids and the invoking model. The analytics client
// deduplicates per turn and plugin (analytics/src/tests/suite/plugins_tests.rs::
// plugin_used_dedupe_is_keyed_by_turn_and_plugin).
func TestRememberExplicitPluginMentionsTracksPluginUsedLikeRust(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{Values: map[string]any{}}
	params := &turn.TurnStartParams{Input: []turn.TurnUserInput{{
		Type: "mention",
		Name: "sample",
		Path: "plugin://sample@test",
	}}}
	router, sink := pluginUsedTestRouter(t)

	router.rememberExplicitPluginMentions(ctx, "thread-3", "turn-3", "conn-1", params, cfg, "gpt-5")
	event := waitForPluginUsedEvent(t, sink)
	metadata := event.EventParams
	if event.EventType != telemetry.CodexPluginUsedEventType {
		t.Fatalf("event_type = %q", event.EventType)
	}
	if metadata.PluginID == nil || *metadata.PluginID != "sample@test" {
		t.Fatalf("plugin_id = %#v", metadata.PluginID)
	}
	if metadata.PluginName == nil || *metadata.PluginName != "sample" {
		t.Fatalf("plugin_name = %#v", metadata.PluginName)
	}
	if metadata.MarketplaceName == nil || *metadata.MarketplaceName != "test" {
		t.Fatalf("marketplace_name = %#v", metadata.MarketplaceName)
	}
	if metadata.RemotePluginID != nil {
		t.Fatalf("remote_plugin_id = %#v, want null", metadata.RemotePluginID)
	}
	if metadata.HasSkills == nil || !*metadata.HasSkills {
		t.Fatalf("has_skills = %#v", metadata.HasSkills)
	}
	if metadata.MCPServerCount == nil || *metadata.MCPServerCount != 2 {
		t.Fatalf("mcp_server_count = %#v", metadata.MCPServerCount)
	}
	if len(metadata.ConnectorIDs) != 2 || metadata.ConnectorIDs[0] != "calendar" || metadata.ConnectorIDs[1] != "drive" {
		t.Fatalf("connector_ids = %#v", metadata.ConnectorIDs)
	}
	if len(metadata.MCPServerNames) != 2 || metadata.MCPServerNames[0] != "mcp-1" || metadata.MCPServerNames[1] != "mcp-2" {
		t.Fatalf("mcp_server_names = %#v", metadata.MCPServerNames)
	}
	if metadata.ThreadID == nil || *metadata.ThreadID != "thread-3" ||
		metadata.TurnID == nil || *metadata.TurnID != "turn-3" ||
		metadata.ModelSlug == nil || *metadata.ModelSlug != "gpt-5" {
		t.Fatalf("attribution = %#v/%#v/%#v", metadata.ThreadID, metadata.TurnID, metadata.ModelSlug)
	}
	if metadata.TurnTrigger != nil {
		t.Fatalf("turn_trigger = %#v, want absent", metadata.TurnTrigger)
	}

	router.rememberExplicitPluginMentions(ctx, "thread-3", "turn-3", "conn-1", params, cfg, "gpt-5")
	select {
	case duplicate := <-sink.events:
		t.Fatalf("duplicate plugin-used event for the same turn: %#v", duplicate)
	default:
	}

	router.rememberExplicitPluginMentions(ctx, "thread-3", "turn-4", "conn-1", params, cfg, "gpt-5")
	next := waitForPluginUsedEvent(t, sink)
	if next.EventParams.TurnID == nil || *next.EventParams.TurnID != "turn-4" {
		t.Fatalf("second turn turn_id = %#v", next.EventParams.TurnID)
	}
	if next.EventParams.TurnTrigger != nil {
		t.Fatalf("turn_trigger = %#v, want absent", next.EventParams.TurnTrigger)
	}

	// Rust #60018: the retained turn metadata supplies turn_trigger.
	trigger := "user_message"
	router.recordAnalyticsTurnTrigger("thread-3", "turn-5", trigger)
	router.rememberExplicitPluginMentions(ctx, "thread-3", "turn-5", "conn-1", params, cfg, "gpt-5")
	withTrigger := waitForPluginUsedEvent(t, sink)
	if withTrigger.EventParams.TurnTrigger == nil || *withTrigger.EventParams.TurnTrigger != trigger {
		t.Fatalf("turn_trigger = %#v, want %q", withTrigger.EventParams.TurnTrigger, trigger)
	}
}

// A capability whose config name is not a `<plugin>@<marketplace>` key has no
// reportable identity: Rust's telemetry_metadata_for_capability_summary returns
// None and skips the event.
func TestRememberExplicitPluginMentionsSkipsUnparsablePluginIDLikeRust(t *testing.T) {
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		ID: "opaque-plugin", Name: "opaque-plugin", Installed: true, Enabled: true,
	}})
	sink := newRecordingPluginUsedSink()
	router := NewRuntimeRouter(RuntimeServices{Plugins: plugins, Analytics: sink})
	params := &turn.TurnStartParams{Input: []turn.TurnUserInput{{
		Type: "mention",
		Name: "opaque-plugin",
		Path: "plugin://opaque-plugin",
	}}}

	router.rememberExplicitPluginMentions(context.Background(), "thread-3", "turn-3", "conn-1", params, &config.Config{Values: map[string]any{}}, "gpt-5")
	select {
	case event := <-sink.events:
		t.Fatalf("unexpected plugin-used event = %#v", event)
	default:
	}
}

func pluginUsedTestRouter(t *testing.T) (*RuntimeRouter, *recordingPluginUsedSink) {
	t.Helper()
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		ID:              "sample@test",
		Name:            "sample",
		MarketplaceName: "test",
		Installed:       true,
		Enabled:         true,
		HasSkills:       true,
		MCPServers:      []string{"mcp-1", "mcp-2"},
		AppConnectors:   []string{"calendar", "drive"},
	}})
	sink := newRecordingPluginUsedSink()
	return NewRuntimeRouter(RuntimeServices{Plugins: plugins, Analytics: sink}), sink
}

func waitForPluginUsedEvent(t *testing.T, sink *recordingPluginUsedSink) telemetry.CodexPluginUsedEventRequest {
	t.Helper()
	select {
	case event := <-sink.events:
		return event
	default:
		t.Fatal("timed out waiting for codex_plugin_used")
		return telemetry.CodexPluginUsedEventRequest{}
	}
}
