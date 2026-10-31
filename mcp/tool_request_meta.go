package mcp

import (
	"errors"
	"fmt"
)

// MCPToolRequestMetaProvider supplies one MCP request metadata value for a live
// tool invocation (Rust McpToolRequestMetaProvider, #58395). An error fails the
// tool call before dispatch and is reported to the tool caller.
type MCPToolRequestMetaProvider func(threadID string, turnID string, callID string) (any, error)

// MCPToolRequestMetaHook mirrors Rust McpToolRequestMetaHook (#58395): it adds
// one private _meta field to one tool on the host-selected local MCP
// registration. The source and the full transport supplied by the trusted host
// bind the hook to that registration, including routing headers for a proxied
// endpoint. Transport may contain credentials and must not be logged.
type MCPToolRequestMetaHook struct {
	ServerName string
	Source     CatalogSource
	Transport  ServerConfig
	ToolName   string
	// Key is a metadata key owned by the provider. Existing metadata cannot be
	// overwritten.
	Key      string
	Provider MCPToolRequestMetaProvider
}

// Matches reports whether the immutable call is bound to this hook: the server
// name, the remote tool name, the local environment, the registration source
// and the transport must all equal the host-supplied selection.
func (h *MCPToolRequestMetaHook) Matches(serverName string, toolName string, registration *ResolvedServer) bool {
	if h == nil || registration == nil {
		return false
	}
	if serverName != h.ServerName || toolName != h.ToolName {
		return false
	}
	if !registration.Config.IsLocalEnvironment() {
		return false
	}
	if registration.Source != h.Source {
		return false
	}
	return mcpServerTransportEqual(registration.Config, h.Transport)
}

// Apply adds the provider's value only when the call matches the selected
// registration. A missing metadata object, an already-present key or a provider
// error is returned before the MCP request is dispatched.
func (h *MCPToolRequestMetaHook) Apply(meta any, serverName string, toolName string, registration *ResolvedServer, threadID string, turnID string, callID string) (any, error) {
	if !h.Matches(serverName, toolName, registration) {
		return meta, nil
	}
	object, ok := meta.(map[string]any)
	if !ok || object == nil {
		return nil, errors.New("missing MCP request metadata object")
	}
	if _, exists := object[h.Key]; exists {
		return nil, fmt.Errorf("MCP request metadata already contains %s", h.Key)
	}
	if h.Provider == nil {
		return nil, errors.New("missing MCP request metadata provider")
	}
	value, err := h.Provider(threadID, turnID, callID)
	if err != nil {
		return nil, err
	}
	object[h.Key] = value
	return object, nil
}
