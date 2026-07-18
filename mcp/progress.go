package mcp

import (
	"context"
	"encoding/json"
	"strings"
)

type MCPProgressNotification struct {
	ServerName    string
	ThreadID      string
	TurnID        string
	ItemID        string
	ProgressToken any
	Progress      *float64
	Total         *float64
	Message       string
	Params        map[string]any
}

type MCPProgressHandler interface {
	HandleMCPProgress(ctx context.Context, notification *MCPProgressNotification)
}

type MCPProgressHandlerFunc func(ctx context.Context, notification *MCPProgressNotification)

func (f MCPProgressHandlerFunc) HandleMCPProgress(ctx context.Context, notification *MCPProgressNotification) {
	if f != nil {
		f(ctx, notification)
	}
}

func handleMCPClientNotification(ctx context.Context, serverName string, progress MCPProgressHandler, method string, params json.RawMessage) error {
	if strings.TrimSpace(method) != "notifications/progress" {
		return nil
	}
	if progress == nil {
		return nil
	}
	notification := parseMCPProgressNotification(ctx, serverName, params)
	progress.HandleMCPProgress(ctx, notification)
	return nil
}

func parseMCPProgressNotification(ctx context.Context, serverName string, params json.RawMessage) *MCPProgressNotification {
	threadID, turnID, itemID := mcpClientContextFromContext(ctx)
	out := &MCPProgressNotification{
		ServerName: strings.TrimSpace(serverName),
		ThreadID:   threadID,
		TurnID:     turnID,
		ItemID:     itemID,
		Params:     map[string]any{},
	}
	if len(params) == 0 {
		return out
	}
	var raw map[string]any
	if err := json.Unmarshal(params, &raw); err != nil {
		return out
	}
	out.Params = cloneAnyMap(raw)
	if value, ok := raw["progressToken"]; ok {
		out.ProgressToken = cloneJSONValue(value)
	} else if value, ok := raw["progress_token"]; ok {
		out.ProgressToken = cloneJSONValue(value)
	}
	if value, ok := raw["progress"].(float64); ok {
		out.Progress = &value
	}
	if value, ok := raw["total"].(float64); ok {
		out.Total = &value
	}
	out.Message = stringFromAnyMap(raw, "message")
	return out
}
