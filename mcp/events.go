package mcp

import (
	"encoding/json"
	"errors"
	"strings"
)

// MCPEventDefinition mirrors Rust McpEventDefinition (41014b11bd): an event
// advertised by a hosted Plugin Runtime.
type MCPEventDefinition struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Delivery      []string        `json:"delivery"`
	InputSchema   json.RawMessage `json:"inputSchema"`
	PayloadSchema json.RawMessage `json:"payloadSchema"`
}

// MCPEventNotification is one raw lifecycle notification from an event
// subscription (Rust McpEventNotification).
type MCPEventNotification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type mcpEventListResult struct {
	Events []MCPEventDefinition `json:"events"`
}

// ListMCPEvents lists the events advertised by the named server's hosted
// Plugin Runtime (Rust McpResourceClient::list_events, events/list).
func (s *MCPService) ListMCPEvents(serverName string) ([]MCPEventDefinition, error) {
	if s == nil {
		return nil, invalidMCPRequest("MCP service is nil")
	}
	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		return nil, invalidMCPRequest("server is required")
	}
	if err := s.requiredServerAvailable(serverName); err != nil {
		return nil, err
	}
	config, ok := s.serverConfig(serverName)
	if !ok {
		return nil, invalidMCPRequest("unknown MCP server " + serverName)
	}
	var result mcpEventListResult
	if strings.TrimSpace(config.URL) != "" {
		if err := ValidateServerAuth(serverName, &config); err != nil {
			return nil, err
		}
		client := s.httpClientForServer(serverName, &config)
		if err := client.CallWithOptions(&httpClientCallOptions{ServerName: serverName}, "events/list", nil, &result); err != nil {
			return nil, err
		}
		return result.Events, nil
	}
	if strings.TrimSpace(config.Command) != "" {
		client := s.stdioClientForServer(serverName, &config)
		if err := client.Call("events/list", nil, &result); err != nil {
			return nil, err
		}
		return result.Events, nil
	}
	return nil, invalidMCPRequest("MCP server has no transport")
}

// OpenMCPEventStream opens an MCP event subscription with the supplied event
// arguments (Rust McpResourceClient::open_event_stream, events/stream).
func (s *MCPService) OpenMCPEventStream(serverName string, eventName string, arguments any) (*MCPEventStream, error) {
	if s == nil {
		return nil, invalidMCPRequest("MCP service is nil")
	}
	serverName = strings.TrimSpace(serverName)
	eventName = strings.TrimSpace(eventName)
	if serverName == "" || eventName == "" {
		return nil, invalidMCPRequest("server and event name are required")
	}
	if err := s.requiredServerAvailable(serverName); err != nil {
		return nil, err
	}
	config, ok := s.serverConfig(serverName)
	if !ok {
		return nil, invalidMCPRequest("unknown MCP server " + serverName)
	}
	params := map[string]any{"name": eventName, "arguments": arguments}
	var raw json.RawMessage
	var err error
	if strings.TrimSpace(config.URL) != "" {
		if err := ValidateServerAuth(serverName, &config); err != nil {
			return nil, err
		}
		client := s.httpClientForServer(serverName, &config)
		err = client.CallWithOptions(&httpClientCallOptions{ServerName: serverName}, "events/stream", params, &raw)
	} else if strings.TrimSpace(config.Command) != "" {
		client := s.stdioClientForServer(serverName, &config)
		err = client.Call("events/stream", params, &raw)
	} else {
		return nil, invalidMCPRequest("MCP server has no transport")
	}
	if err != nil {
		return nil, err
	}
	return &MCPEventStream{raw: raw}, nil
}

// MCPEventStream owns an event subscription response (Rust McpEventStream).
type MCPEventStream struct {
	raw json.RawMessage
}

var ErrMCPEventStreamEmpty = errors.New("MCP event stream returned no payload")

// Notification returns the raw lifecycle notification payload, or nil when the
// stream ended.
func (s *MCPEventStream) Notification() *MCPEventNotification {
	if s == nil || len(s.raw) == 0 || string(s.raw) == "null" {
		return nil
	}
	var notification MCPEventNotification
	if err := json.Unmarshal(s.raw, &notification); err != nil {
		return nil
	}
	return &notification
}
