package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/mcp"
)

// Execute runs an mcp_tool hook through the session's shared MCP runtime
// (Rust #39296/#39331). The call goes through the current connection set
// without waiting for server startup or reconnection, and without model-tool
// approval or recursive hook dispatch. Unavailable servers fail immediately.
func (r *RuntimeRouter) Execute(ctx context.Context, call HookMcpCall) (string, error) {
	if r == nil {
		return "", fmt.Errorf("MCP hook executor is unavailable")
	}
	server := strings.TrimSpace(call.Server)
	toolName := strings.TrimSpace(call.Tool)
	if server == "" || toolName == "" {
		return "", fmt.Errorf("MCP hook requires server and tool")
	}
	threadID := ""
	service := r.mcpServiceForThread(threadID, nil)
	if service == nil {
		return "", fmt.Errorf("MCP runtime is unavailable for hook call %s.%s", server, toolName)
	}
	// Rust #39296/#39331: hook calls are restricted to already-connected,
	// cataloged, policy-allowed tools. Unavailable servers fail immediately
	// without starting or reconnecting them.
	config, ok := service.ServerConfigForServer(server)
	if !ok {
		return "", fmt.Errorf("MCP hook server %q is not cataloged or connected", server)
	}
	if strings.TrimSpace(config.URL) == "" && strings.TrimSpace(config.Command) == "" {
		return "", fmt.Errorf("MCP hook server %q has no configured endpoint", server)
	}
	var arguments map[string]any
	if len(call.Input) > 0 {
		arguments = call.Input
	}
	response, err := service.CallTool(&mcp.MCPToolCallParams{
		Server:    server,
		Tool:      toolName,
		Arguments: arguments,
	})
	if err != nil {
		return "", err
	}
	return mcpHookResponseText(response), nil
}

func mcpHookResponseText(response *mcp.MCPToolCallResponse) string {
	if response == nil {
		return ""
	}
	parts := make([]string, 0, len(response.Content))
	for _, content := range response.Content {
		if strings.TrimSpace(content.Text) != "" {
			parts = append(parts, strings.TrimSpace(content.Text))
			continue
		}
		if len(content.Raw) > 0 {
			if data, err := json.Marshal(content.Raw); err == nil {
				parts = append(parts, string(data))
			}
		}
	}
	return strings.Join(parts, "\n")
}
