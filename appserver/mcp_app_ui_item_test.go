package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/mcp"
	"codex_go/tool"
	"codex_go/turn"
)

// TestMCPAppUIMetadataReachesTheToolCallItemAndWire mirrors Rust #45805: the
// invoked descriptor's widget presentation and the trusted Codex Apps connector
// context are captured on the tool call, persist into the thread item, and are
// rendered as `mcpAppUi` (camelCase) on the v2 item.
func TestMCPAppUIMetadataReachesTheToolCallItemAndWire(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "call-calendar",
			ToolName: tool.NamespacedName("mcp__codex_apps__calendar", "create_event"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"title":"standup"}`},
		},
		Output: &tool.Output{
			CallID:   "call-calendar",
			ToolName: tool.NamespacedName("mcp__codex_apps__calendar", "create_event"),
			Success:  true,
			Body:     "created",
			Data: map[string]any{
				"mcpToolCall":    true,
				"server":         "codex_apps__calendar",
				"tool":           "create_event",
				"connector_id":   "calendar",
				"connector_name": "Calendar",
				"link_id":        "link_calendar",
				"action_name":    "create_event",
				"mcp_app_ui":     mcp.McpAppUI{ResourceURI: "ui://widgets/calendar", PreferredModelDisplayMode: mcp.McpAppDisplayModeFullscreen},
			},
			CompletedAt: now,
		},
		StartedAt:  now,
		FinishedAt: now,
	}

	item, ok := sessionItemForAppToolCall("turn-1", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolCall() did not produce an item")
	}
	if appUI, ok := item.Data["mcpAppUi"].(mcp.McpAppUI); !ok || appUI.ResourceURI != "ui://widgets/calendar" {
		t.Fatalf("item mcpAppUi = %#v", item.Data["mcpAppUi"])
	}
	if item.Data["mcpAppResourceUri"] != "ui://widgets/calendar" {
		t.Fatalf("legacy mcpAppResourceUri = %#v", item.Data["mcpAppResourceUri"])
	}
	context, ok := item.Data["appContext"].(map[string]any)
	if !ok {
		t.Fatalf("appContext = %#v", item.Data["appContext"])
	}
	if context["connectorId"] != "calendar" || context["linkId"] != "link_calendar" ||
		context["appName"] != "Calendar" || context["actionName"] != "create_event" ||
		context["resourceUri"] != "ui://widgets/calendar" {
		t.Fatalf("appContext = %#v", context)
	}

	threadItem := BuildThreadItem(item)
	if threadItem.Type == "" {
		t.Fatalf("BuildThreadItem() = %#v", threadItem)
	}
	encoded, err := json.Marshal(&threadItem)
	if err != nil {
		t.Fatalf("Marshal(thread item) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(thread item) error = %v", err)
	}
	appUI, ok := decoded["mcpAppUi"].(map[string]any)
	if !ok {
		t.Fatalf("wire mcpAppUi = %#v (item = %s)", decoded["mcpAppUi"], encoded)
	}
	if appUI["resourceUri"] != "ui://widgets/calendar" || appUI["preferredModelDisplayMode"] != "fullscreen" {
		t.Fatalf("wire mcpAppUi = %#v", appUI)
	}
	if decoded["mcpAppResourceUri"] != "ui://widgets/calendar" {
		t.Fatalf("wire mcpAppResourceUri = %#v", decoded["mcpAppResourceUri"])
	}
}

// TestMCPAppUIStaysNullWithoutDescriptorWidgets keeps Rust's fallback: tools
// that declare no widget (or older history) report a null presentation so
// clients keep using catalog discovery.
func TestMCPAppUIStaysNullWithoutDescriptorWidgets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "call-plain",
			ToolName: tool.NamespacedName("mcp__plain", "echo"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
		},
		Output: &tool.Output{
			CallID:      "call-plain",
			ToolName:    tool.NamespacedName("mcp__plain", "echo"),
			Success:     true,
			Body:        "ok",
			Data:        map[string]any{"mcpToolCall": true, "server": "plain", "tool": "echo"},
			CompletedAt: now,
		},
		StartedAt:  now,
		FinishedAt: now,
	}
	item, ok := sessionItemForAppToolCall("turn-1", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolCall() did not produce an item")
	}
	if item.Data["mcpAppUi"] != nil || item.Data["appContext"] != nil {
		t.Fatalf("unexpected app metadata: %#v", item.Data)
	}
	threadItem := BuildThreadItem(item)
	encoded, err := json.Marshal(&threadItem)
	if err != nil {
		t.Fatalf("Marshal(thread item) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal(thread item) error = %v", err)
	}
	if decoded["mcpAppUi"] != nil {
		t.Fatalf("wire mcpAppUi = %#v, want null", decoded["mcpAppUi"])
	}
}
