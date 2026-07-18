package appserver

import (
	"context"
	"testing"

	"codex_go/mcp"
)

func TestAppserverMCPProgressHandlerNotifiesToolCallProgress(t *testing.T) {
	sink := NewNotificationBuffer()
	handler := &appserverMCPProgressHandler{notify: func(method NotificationMethod, params any) {
		sink.Notify(NewNotification(method, params))
	}}

	progress := float64(1)
	total := float64(2)
	handler.HandleMCPProgress(context.Background(), &mcp.MCPProgressNotification{
		ServerName:    " docs ",
		ThreadID:      " thread-1 ",
		TurnID:        " turn-1 ",
		ItemID:        " item-1 ",
		ProgressToken: "token-1",
		Progress:      &progress,
		Total:         &total,
		Message:       "Working",
		Params:        map[string]any{"progressToken": "token-1", "message": "Working"},
	})
	progress = 99
	total = 100

	notifications := sink.List()
	if len(notifications) != 1 || notifications[0].Method != NotificationMCPToolCallProgress {
		t.Fatalf("notifications = %#v", notifications)
	}
	payload, ok := notifications[0].Params.(*MCPToolCallProgressNotification)
	if !ok {
		t.Fatalf("notification params = %#v", notifications[0].Params)
	}
	if payload.ThreadID != "thread-1" || payload.TurnID != "turn-1" || payload.ItemID != "item-1" || payload.Message != "Working" {
		t.Fatalf("progress payload = %#v", payload)
	}
	if payload.ServerName != "docs" || payload.ProgressToken != "token-1" {
		t.Fatalf("progress payload metadata = %#v", payload)
	}
	if payload.Progress == nil || *payload.Progress != 1 || payload.Total == nil || *payload.Total != 2 {
		t.Fatalf("progress payload numeric fields = %#v", payload)
	}
	if payload.Params["progressToken"] != "token-1" || payload.Params["message"] != "Working" {
		t.Fatalf("progress payload params = %#v", payload.Params)
	}
}

func TestAppserverMCPProgressHandlerRequiresItemContext(t *testing.T) {
	sink := NewNotificationBuffer()
	handler := &appserverMCPProgressHandler{notify: func(method NotificationMethod, params any) {
		sink.Notify(NewNotification(method, params))
	}}

	handler.HandleMCPProgress(context.Background(), &mcp.MCPProgressNotification{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Message:  "Working",
	})
	if len(sink.List()) != 0 {
		t.Fatalf("unexpected notifications = %#v", sink.List())
	}
}
