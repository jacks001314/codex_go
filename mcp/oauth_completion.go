package mcp

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// NewMCPOAuthLoginID mirrors the app-server's `Uuid::now_v7().to_string()`
// (Rust #49276, `request_processors/mcp_processor.rs`): it identifies one
// explicit MCP OAuth login attempt across the login response and the completion
// notification. Like `ThreadId::new`, a fresh attempt receives a UUIDv7; the
// random fallback only applies when the v7 generator fails.
func NewMCPOAuthLoginID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}

type MCPOAuthLoginCompletion struct {
	Name     string
	ThreadID string
	// LoginID identifies the explicit login attempt this completion belongs to
	// (Rust #49276); empty for older servers that omit it.
	LoginID string
	Success bool
	Error   string
}

type MCPOAuthLoginCompletionHandler interface {
	HandleMCPOAuthLoginCompleted(ctx context.Context, completion *MCPOAuthLoginCompletion)
}

type MCPOAuthLoginCompletionHandlerFunc func(ctx context.Context, completion *MCPOAuthLoginCompletion)

func (f MCPOAuthLoginCompletionHandlerFunc) HandleMCPOAuthLoginCompleted(ctx context.Context, completion *MCPOAuthLoginCompletion) {
	if f != nil {
		f(ctx, completion)
	}
}

func normalizeMCPOAuthLoginCompletion(completion *MCPOAuthLoginCompletion) *MCPOAuthLoginCompletion {
	if completion == nil {
		return nil
	}
	out := *completion
	out.Name = strings.TrimSpace(out.Name)
	out.ThreadID = strings.TrimSpace(out.ThreadID)
	out.LoginID = strings.TrimSpace(out.LoginID)
	out.Error = strings.TrimSpace(out.Error)
	return &out
}
