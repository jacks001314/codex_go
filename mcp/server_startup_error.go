package mcp

import "fmt"

// MCPServerStartupError reports a failed MCP server startup to the caller so a
// cached tool call can carry the reason its server is unusable (Rust #60038:
// "Could not start MCP server `{server_name}`"). Rust keeps the generic
// unavailable-tool text for tools that are missing, filtered, or no longer
// visible to the model, so this error only exists when a start was actually
// attempted and failed.
//
// A cached tool can stay advertised after its server failed to start. Reporting
// the generic text there hid the startup failure from the model, which then had
// no way to tell a broken command from a removed tool.
type MCPServerStartupError struct {
	// Server is the configured server name, not the namespaced tool name.
	Server string
	// Err is the startup failure, with its wrapped causes preserved.
	Err error
}

func (e *MCPServerStartupError) Error() string {
	if e == nil {
		return ""
	}
	message := mcpStartupErrorMessage(e.Err)
	if message == "" {
		message = "unknown startup failure"
	}
	return fmt.Sprintf("Could not start MCP server `%s`: %s", e.Server, message)
}

// Unwrap keeps the cause chain available to errors.Is and errors.As.
func (e *MCPServerStartupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ModelVisibleToolError marks the failure as this tool call's result instead of
// a turn-level error (Rust core returns the same text through
// FunctionCallError::RespondToModel).
func (e *MCPServerStartupError) ModelVisibleToolError() {}
