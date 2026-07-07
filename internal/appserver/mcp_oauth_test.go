package appserver

import (
	"context"
	"testing"

	"codex_go/internal/mcp"
)

func TestAppserverMCPOAuthLoginCompletionHandlerNotifies(t *testing.T) {
	sink := NewNotificationBuffer()
	handler := &appserverMCPOAuthLoginCompletionHandler{notify: func(method NotificationMethod, params any) {
		sink.Notify(NewNotification(method, params))
	}}

	handler.HandleMCPOAuthLoginCompleted(context.Background(), &mcp.MCPOAuthLoginCompletion{
		Name:     " docs ",
		ThreadID: " thread-1 ",
		Success:  true,
	})

	notifications := sink.List()
	if len(notifications) != 1 || notifications[0].Method != NotificationMCPServerOauthLoginCompleted {
		t.Fatalf("notifications = %#v", notifications)
	}
	payload, ok := notifications[0].Params.(*MCPServerOauthLoginCompletedNotification)
	if !ok {
		t.Fatalf("notification params = %#v", notifications[0].Params)
	}
	if payload.Name != "docs" || payload.ThreadID == nil || *payload.ThreadID != "thread-1" || !payload.Success || payload.Error != nil {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAppserverMCPOAuthLoginCompletionHandlerMapsError(t *testing.T) {
	sink := NewNotificationBuffer()
	handler := &appserverMCPOAuthLoginCompletionHandler{notify: func(method NotificationMethod, params any) {
		sink.Notify(NewNotification(method, params))
	}}

	handler.HandleMCPOAuthLoginCompleted(context.Background(), &mcp.MCPOAuthLoginCompletion{
		Name:    "docs",
		Success: false,
		Error:   "denied",
	})

	notifications := sink.List()
	if len(notifications) != 1 {
		t.Fatalf("notifications = %#v", notifications)
	}
	payload := notifications[0].Params.(*MCPServerOauthLoginCompletedNotification)
	if payload.Success || payload.Error == nil || *payload.Error != "denied" {
		t.Fatalf("payload = %#v", payload)
	}
}
