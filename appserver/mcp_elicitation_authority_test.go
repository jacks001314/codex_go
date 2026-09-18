package appserver

import (
	"context"
	"testing"

	"codex_go/mcp"
	"codex_go/sandbox"
)

// Mirrors Rust #40728: an elicitation request for a server whose published
// permission authority is unavailable is declined without surfacing a prompt,
// before any approval-policy handling.
func TestAppserverMCPElicitationDeclinesWithoutServerAuthorityLikeRust(t *testing.T) {
	handler := &appserverMCPElicitationHandler{
		authority: func(string, string, string) mcpElicitationAuthority {
			return mcpElicitationAuthority{
				ApprovalPolicy:           sandbox.ApprovalOnRequest,
				ApprovalsReviewer:        "user",
				ServerAuthorityPublished: true,
				AllowsMCPElicitations:    true,
				AllowUserInteraction:     true,
			}
		},
	}
	response, err := handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
		ServerName: "docs",
		ThreadID:   "thread-1",
		Method:     "elicitation/create",
	})
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionDecline || response.Meta != nil {
		t.Fatalf("response = %#v, want fail-closed decline without meta", response)
	}
}

// When the server's own authority is published, the handler keeps using it for
// the approval decision instead of the thread-wide authority.
func TestAppserverMCPElicitationUsesServerAuthorityLikeRust(t *testing.T) {
	readOnly := sandbox.ReadOnlyPermissionProfile()
	handler := &appserverMCPElicitationHandler{
		authority: func(string, string, string) mcpElicitationAuthority {
			return mcpElicitationAuthority{
				ApprovalPolicy:           sandbox.ApprovalNever,
				ApprovalsReviewer:        "user",
				PermissionProfile:        &readOnly,
				ServerAuthorityPublished: true,
				AllowsMCPElicitations:    true,
				AllowUserInteraction:     true,
			}
		},
	}
	response, err := handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
		ServerName:      "docs",
		ThreadID:        "thread-1",
		Method:          "elicitation/create",
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	// Read-only authority is not auto-approved, so the elicitation is declined
	// rather than surfacing a prompt (approval never).
	if response == nil || response.Action != mcp.MCPElicitationActionDecline {
		t.Fatalf("response = %#v, want decline for restricted server authority", response)
	}
}
