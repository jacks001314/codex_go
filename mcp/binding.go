package mcp

import (
	"fmt"
	"strings"
)

// Binding captures the MCP catalog revision that a model step observed.
type Binding struct {
	service    *MCPService
	generation uint64
	tools      map[string]map[string]bool
}

func (s *MCPService) CaptureBinding(statuses []MCPServerStatus) *Binding {
	b := &Binding{service: s, generation: s.Generation(), tools: map[string]map[string]bool{}}
	for _, status := range statuses {
		if status.State != "" && status.State != MCPServerReady {
			continue
		}
		server := status.effectiveName()
		if server == "" {
			continue
		}
		set := map[string]bool{}
		for _, tool := range status.Tools {
			if name := strings.TrimSpace(tool.Name); name != "" {
				set[name] = true
			}
		}
		b.tools[server] = set
	}
	return b
}
func (b *Binding) Generation() uint64 {
	if b == nil {
		return 0
	}
	return b.generation
}
func (b *Binding) CallTool(params *MCPToolCallParams) (*MCPToolCallResponse, error) {
	if b == nil || b.service == nil {
		return nil, invalidMCPRequest("MCP binding is unavailable")
	}
	if b.service.Generation() != b.generation {
		return nil, fmt.Errorf("%w: MCP tool catalog changed after this model step was prepared", ErrInvalidMCPRequest)
	}
	server := strings.TrimSpace(firstNonEmptyMCP(params.Server, params.ServerName))
	tool := strings.TrimSpace(firstNonEmptyMCP(params.Tool, params.ToolName))
	if !b.tools[server][tool] {
		return nil, fmt.Errorf("%w: MCP tool %q was not present in captured catalog for server %q", ErrInvalidMCPRequest, tool, server)
	}
	response, err := b.service.CallTool(params)
	if err != nil {
		return nil, err
	}
	if b.service.Generation() != b.generation {
		return nil, fmt.Errorf("%w: MCP tool catalog changed while the call was executing", ErrInvalidMCPRequest)
	}
	return response, nil
}
