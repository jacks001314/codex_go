package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
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

// OpenMCPEventStream opens a continuous MCP event subscription with the
// supplied event arguments (Rust McpResourceClient::open_event_stream,
// events/stream). HTTP servers stream every server notification on the
// returned stream until Close; stdio servers deliver the single response
// notification and then end (the hosted Plugin Runtime path is HTTP).
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
	if strings.TrimSpace(config.URL) != "" {
		if err := ValidateServerAuth(serverName, &config); err != nil {
			return nil, err
		}
		return s.openHTTPEventStream(serverName, &config, params), nil
	}
	if strings.TrimSpace(config.Command) != "" {
		client := s.stdioClientForServer(serverName, &config)
		var raw json.RawMessage
		if err := client.Call("events/stream", params, &raw); err != nil {
			return nil, err
		}
		stream := newMCPEventStream(context.Background(), nil)
		if notification := decodeMCPEventStreamNotification(raw); notification != nil {
			stream.notifications <- notification
		}
		close(stream.done)
		return stream, nil
	}
	return nil, invalidMCPRequest("MCP server has no transport")
}

func (s *MCPService) openHTTPEventStream(serverName string, config *ServerConfig, params map[string]any) *MCPEventStream {
	ctx, cancel := context.WithCancel(context.Background())
	stream := newMCPEventStream(ctx, cancel)
	client := s.httpClientForServer(serverName, config)
	go func() {
		defer stream.finish()
		err := client.CallStream(ctx, &httpClientCallOptions{ServerName: serverName}, "events/stream", params, func(envelope *stdioRPCEnvelope) error {
			if envelope == nil || strings.TrimSpace(envelope.Method) == "" {
				return nil
			}
			notification := &MCPEventNotification{Method: strings.TrimSpace(envelope.Method)}
			if len(envelope.Params) > 0 {
				notification.Params = append(json.RawMessage(nil), envelope.Params...)
			}
			select {
			case stream.notifications <- notification:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		stream.err = err
	}()
	return stream
}

func decodeMCPEventStreamNotification(raw json.RawMessage) *MCPEventNotification {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var notification MCPEventNotification
	if err := json.Unmarshal(raw, &notification); err != nil {
		return nil
	}
	return &notification
}

// MCPEventStream owns a continuous event subscription (Rust McpEventStream).
type MCPEventStream struct {
	notifications chan *MCPEventNotification
	done          chan struct{}
	err           error
	cancel        context.CancelFunc
	closeOnce     sync.Once
}

func newMCPEventStream(ctx context.Context, cancel context.CancelFunc) *MCPEventStream {
	return &MCPEventStream{
		notifications: make(chan *MCPEventNotification, 64),
		done:          make(chan struct{}),
		cancel:        cancel,
	}
}

// Notifications returns the lifecycle notifications as they arrive. The
// channel closes when the stream ends (Close, server termination, transport
// failure, or auth/runtime ownership change at the app-server layer).
func (s *MCPEventStream) Notifications() <-chan *MCPEventNotification {
	if s == nil {
		return nil
	}
	return s.notifications
}

// Done is closed when the underlying transport ends.
func (s *MCPEventStream) Done() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.done
}

// Err returns the transport error, if any, once the stream has ended.
func (s *MCPEventStream) Err() error {
	if s == nil {
		return nil
	}
	return s.err
}

// Close cancels the subscription and releases the transport. The stream's
// Done channel closes once the transport goroutine exits.
func (s *MCPEventStream) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
}

func (s *MCPEventStream) finish() {
	close(s.done)
}
