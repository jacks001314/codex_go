package mcp

import (
	"context"
	"testing"
)

func TestOAuthLoginCompletionBroadcastPreservesAppServerHandler(t *testing.T) {
	service := NewMCPService(nil)
	var primary, listener MCPOAuthLoginCompletion
	service.SetOAuthLoginCompletionHandler(MCPOAuthLoginCompletionHandlerFunc(func(_ context.Context, completion *MCPOAuthLoginCompletion) {
		primary = *completion
	}))
	unsubscribe := service.AddOAuthLoginCompletionListener(func(_ context.Context, completion *MCPOAuthLoginCompletion) {
		listener = *completion
	})

	service.notifyOAuthLoginCompleted("docs", "thread-1", "login-1", &OAuthLoginServerResult{})
	if primary.Name != "docs" || primary.LoginID != "login-1" || !primary.Success {
		t.Fatalf("primary completion = %#v", primary)
	}
	if listener.Name != "docs" || listener.LoginID != "login-1" || !listener.Success {
		t.Fatalf("listener completion = %#v", listener)
	}

	unsubscribe()
	listener = MCPOAuthLoginCompletion{}
	service.notifyOAuthLoginCompleted("other", "thread-2", "login-2", &OAuthLoginServerResult{})
	if listener.Name != "" {
		t.Fatalf("unsubscribed listener received %#v", listener)
	}
	if primary.Name != "other" || primary.LoginID != "login-2" {
		t.Fatalf("primary handler stopped receiving completions: %#v", primary)
	}
}
