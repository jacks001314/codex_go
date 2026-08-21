package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPEventStreamNotificationParsesRawPayload(t *testing.T) {
	notification := decodeMCPEventStreamNotification(json.RawMessage(`{"method":"notifications/event","params":{"name":"fileChanged"}}`))
	if notification == nil {
		t.Fatal("decode = nil, want parsed notification")
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
	if notification := decodeMCPEventStreamNotification(nil); notification != nil {
		t.Fatalf("empty decode = %+v, want nil", notification)
	}
	if notification := decodeMCPEventStreamNotification(json.RawMessage("null")); notification != nil {
		t.Fatalf("null decode = %+v, want nil", notification)
	}
}

func TestMCPEventStreamCloseCancelsTransportAndIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := newMCPEventStream(ctx, cancel)
	stream.Close()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("stream Close did not cancel the transport context")
	}
	stream.Close()
	if stream.Done() == nil {
		t.Fatal("stream done channel is nil")
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
