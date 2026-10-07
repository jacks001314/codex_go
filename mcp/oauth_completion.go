package mcp

import (
	"context"
	"strings"
)

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
