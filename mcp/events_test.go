package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPEventStreamNotificationParsesRawPayload(t *testing.T) {
	stream := &MCPEventStream{raw: json.RawMessage(`{"method":"notifications/event","params":{"name":"fileChanged"}}`)}
	notification := stream.Notification()
	if notification == nil {
		t.Fatal("Notification() = nil, want parsed notification")
	}
	if notification.Method != "notifications/event" {
		t.Fatalf("method = %q", notification.Method)
	}
	var params map[string]any
	if err := json.Unmarshal(notification.Params, &params); err != nil {
		t.Fatalf("params unmarshal error = %v", err)
	}
	if params["name"] != "fileChanged" {
		t.Fatalf("params = %+v", params)
	}
}

func TestMCPEventStreamNotificationEmpty(t *testing.T) {
	if stream := (&MCPEventStream{}).Notification(); stream != nil {
		t.Fatalf("empty stream Notification() = %+v, want nil", stream)
	}
	if stream := (&MCPEventStream{raw: json.RawMessage("null")}).Notification(); stream != nil {
		t.Fatalf("null stream Notification() = %+v, want nil", stream)
	}
}

func TestMCPEventListResultUnmarshal(t *testing.T) {
	body := `{"events":[{"name":"fileChanged","description":"File changed","delivery":["http"],"inputSchema":{},"payloadSchema":{}}]}`
	var result mcpEventListResult
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if len(result.Events) != 1 || result.Events[0].Name != "fileChanged" || len(result.Events[0].Delivery) != 1 {
		t.Fatalf("events = %+v", result.Events)
	}
}

func TestMCPEventStreamRequiresServerAndName(t *testing.T) {
	service := NewMCPService(nil)
	if _, err := service.OpenMCPEventStream("", "event", nil); err == nil {
		t.Fatal("empty server accepted")
	}
	if _, err := service.OpenMCPEventStream("server", "", nil); err == nil {
		t.Fatal("empty event name accepted")
	}
	if _, err := service.ListMCPEvents(""); err == nil {
		t.Fatal("empty server for list accepted")
	}
	if _, err := service.ListMCPEvents("unknown"); err == nil || !strings.Contains(err.Error(), "unknown MCP server") {
		t.Fatalf("ListMCPEvents(unknown) err = %v, want unknown server", err)
	}
}
