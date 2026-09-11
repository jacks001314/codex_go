package mcp

import (
	"encoding/json"
	"strings"
)

// mcpServerCapabilitiesFromInitializeResult extracts the raw `capabilities`
// object advertised by an initialized MCP server (Rust #44826). It returns nil
// when the result is absent or has no capabilities object, so the status wire
// field serializes as null.
func mcpServerCapabilitiesFromInitializeResult(result *json.RawMessage) json.RawMessage {
	if result == nil || len(*result) == 0 {
		return nil
	}
	var payload struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(*result, &payload); err != nil {
		return nil
	}
	if strings.TrimSpace(string(payload.Capabilities)) == "" || strings.TrimSpace(string(payload.Capabilities)) == "null" {
		return nil
	}
	return append(json.RawMessage(nil), payload.Capabilities...)
}

func cloneMCPRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}
