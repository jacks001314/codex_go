package mcp

import (
	"strings"
	"sync"
)

// MCPInvocationMetadata is the host-observed identity of a live MCP tool call.
// Connector identity and tool name come from registered tool metadata, never
// from provider-supplied elicitation arguments.
type MCPInvocationMetadata struct {
	ServerName           string
	ToolName             string
	ConnectorID          string
	ConnectorName        string
	ConnectorDescription string
	ToolTitle            string
	ToolDescription      string
	// Arguments is the JSON value the model supplied for the call, cloned when
	// the call was registered so later rewrites cannot change its identity.
	Arguments any
	ThreadID  string
	TurnID    string
}

func cloneMCPInvocationArguments(arguments any) any {
	if values, ok := arguments.(map[string]any); ok {
		return cloneAnyMap(values)
	}
	return arguments
}

// MCPInvocationRegistry tracks only calls that are currently being executed.
type MCPInvocationRegistry struct {
	mu   sync.RWMutex
	live map[string]MCPInvocationMetadata
}

func NewMCPInvocationRegistry() *MCPInvocationRegistry {
	return &MCPInvocationRegistry{live: map[string]MCPInvocationMetadata{}}
}

func (r *MCPInvocationRegistry) register(serverName, callID string, metadata MCPInvocationMetadata) func() {
	if r == nil {
		return func() {}
	}
	serverName = strings.TrimSpace(serverName)
	callID = strings.TrimSpace(callID)
	if serverName == "" || callID == "" {
		return func() {}
	}
	key := serverName + "\x00" + callID
	r.mu.Lock()
	if r.live == nil {
		r.live = map[string]MCPInvocationMetadata{}
	}
	r.live[key] = metadata
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.live, key)
		r.mu.Unlock()
	}
}

func (r *MCPInvocationRegistry) lookup(serverName, callID string) (MCPInvocationMetadata, bool) {
	if r == nil {
		return MCPInvocationMetadata{}, false
	}
	serverName = strings.TrimSpace(serverName)
	callID = strings.TrimSpace(callID)
	if serverName == "" || callID == "" {
		return MCPInvocationMetadata{}, false
	}
	r.mu.RLock()
	metadata, ok := r.live[serverName+"\x00"+callID]
	r.mu.RUnlock()
	return metadata, ok
}

// RegisterToolInvocation marks one MCP tools/call as live until the returned
// release function runs.
func (s *MCPService) RegisterToolInvocation(serverName, callID string, metadata MCPInvocationMetadata) func() {
	if s == nil {
		return func() {}
	}
	s.mu.Lock()
	if s.invocations == nil {
		s.invocations = NewMCPInvocationRegistry()
	}
	registry := s.invocations
	s.mu.Unlock()
	return registry.register(serverName, callID, metadata)
}

// LiveToolInvocation returns the registered metadata for a call that is still
// executing.
func (s *MCPService) LiveToolInvocation(serverName, callID string) (MCPInvocationMetadata, bool) {
	if s == nil {
		return MCPInvocationMetadata{}, false
	}
	s.mu.Lock()
	registry := s.invocations
	s.mu.Unlock()
	if registry == nil {
		return MCPInvocationMetadata{}, false
	}
	return registry.lookup(serverName, callID)
}
