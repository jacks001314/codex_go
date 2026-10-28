package mcp

import (
	"reflect"
	"sync"
	"weak"

	"codex_go/jsonschema"
)

// sharedMCPDefinitions mirrors Rust #55724 SharedMcpDefinitions: immutable tool
// metadata and normalized parameters are reused within one MCP manager, while
// weak references let completed sessions release their schema graphs.
type sharedMCPDefinitions struct {
	mu      sync.Mutex
	entries map[mcpDefinitionKey]weak.Pointer[mcpToolDefinition]
}

type mcpDefinitionKey struct {
	server      string
	tool        string
	agentPlugin bool
	schemaMax   int
}

// mcpToolDefinition is the immutable result of one tool normalization. It is
// shared only within a single MCPService.
type mcpToolDefinition struct {
	toolInfo    MCPToolInfo
	agentPlugin bool
	schemaMax   int
	parameters  map[string]any
}

func newSharedMCPDefinitions() *sharedMCPDefinitions {
	return &sharedMCPDefinitions{entries: map[mcpDefinitionKey]weak.Pointer[mcpToolDefinition]{}}
}

// getOrCreate returns a matching live definition or builds a new one outside
// the index lock, as Rust does, so normalization never blocks other sessions.
func (s *sharedMCPDefinitions) getOrCreate(toolInfo MCPToolInfo, server string, agentPlugin bool, schemaMax int) *mcpToolDefinition {
	if s == nil {
		return newMCPToolDefinition(toolInfo, agentPlugin, schemaMax)
	}
	key := mcpDefinitionKey{
		server:      server,
		tool:        toolInfo.Name,
		agentPlugin: agentPlugin,
		schemaMax:   schemaMax,
	}
	if definition := s.lookup(key, toolInfo, agentPlugin, schemaMax); definition != nil {
		return definition
	}

	definition := newMCPToolDefinition(toolInfo, agentPlugin, schemaMax)

	s.mu.Lock()
	defer s.mu.Unlock()
	// Sweep entries whose owners were collected before publishing this one.
	for existingKey, entry := range s.entries {
		if entry.Value() == nil {
			delete(s.entries, existingKey)
		}
	}
	if existing := s.entries[key].Value(); existing != nil && existing.matches(toolInfo, agentPlugin, schemaMax) {
		return existing
	}
	s.entries[key] = weak.Make(definition)
	return definition
}

func (s *sharedMCPDefinitions) lookup(key mcpDefinitionKey, toolInfo MCPToolInfo, agentPlugin bool, schemaMax int) *mcpToolDefinition {
	s.mu.Lock()
	defer s.mu.Unlock()
	definition := s.entries[key].Value()
	if definition == nil || !definition.matches(toolInfo, agentPlugin, schemaMax) {
		return nil
	}
	return definition
}

func (s *sharedMCPDefinitions) size() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (d *mcpToolDefinition) matches(toolInfo MCPToolInfo, agentPlugin bool, schemaMax int) bool {
	return d.agentPlugin == agentPlugin && d.schemaMax == schemaMax && reflect.DeepEqual(d.toolInfo, toolInfo)
}

// newMCPToolDefinition performs the schema normalization shared with the
// existing executor path (Rust create_tool_spec).
func newMCPToolDefinition(toolInfo MCPToolInfo, agentPlugin bool, schemaMax int) *mcpToolDefinition {
	toolInfo = cloneMCPToolInfoForDefinition(toolInfo)
	parameters := jsonschema.NormalizeWithBudget(cloneAnyMap(toolInfo.InputSchema), schemaMax)
	if agentPlugin && len(mustMarshalJSON(parameters)) > maxSerializedMCPToolBytes {
		parameters = map[string]any{"type": "object", "additionalProperties": true}
	}
	return &mcpToolDefinition{
		toolInfo:    toolInfo,
		agentPlugin: agentPlugin,
		schemaMax:   schemaMax,
		parameters:  parameters,
	}
}

// cloneMCPToolInfoForDefinition snapshots the metadata so a later in-place
// catalog mutation cannot leave the shared normalized parameters stale.
func cloneMCPToolInfoForDefinition(info MCPToolInfo) MCPToolInfo {
	out := info
	out.InputSchema, _ = cloneJSONValue(info.InputSchema).(map[string]any)
	out.OutputSchema = cloneJSONValue(info.OutputSchema)
	out.Annotations = cloneJSONValue(info.Annotations)
	out.Icons, _ = cloneJSONValue(info.Icons).([]any)
	out.Meta = cloneJSONValue(info.Meta)
	return out
}
