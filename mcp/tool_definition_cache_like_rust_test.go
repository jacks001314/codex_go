package mcp

import (
	"runtime"
	"testing"
	"time"
)

func definitionKeyCount(cache *sharedMCPDefinitions, key mcpDefinitionKey) int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.entries[key]; ok {
		return 1
	}
	return 0
}

func TestSharedMCPDefinitionsReusesMatchingDefinitionLikeRust(t *testing.T) {
	cache := newSharedMCPDefinitions()
	info := MCPToolInfo{
		Name:        "search",
		Description: "Search things",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"q": map[string]any{"type": "string"}},
		},
	}
	key := mcpDefinitionKey{server: "server", tool: "search", schemaMax: 2048}
	first := cache.getOrCreate(info, "server", false, 2048)
	second := cache.getOrCreate(info, "server", false, 2048)
	if first != second {
		t.Fatal("matching tool metadata rebuilt the definition")
	}
	if cache.size() != 1 || definitionKeyCount(cache, key) != 1 {
		t.Fatalf("cache entries = %d", cache.size())
	}

	if cache.getOrCreate(info, "server", false, 4096) == first {
		t.Fatal("a different schema budget reused the definition")
	}
	if cache.getOrCreate(info, "server", true, 2048) == first {
		t.Fatal("agent-plugin mode reused the definition")
	}
	changed := info
	changed.InputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
	}
	if cache.getOrCreate(changed, "server", false, 2048) == first {
		t.Fatal("changed tool metadata reused the definition")
	}
}

func TestSharedMCPDefinitionsPrunesCollectedDefinitionsLikeRust(t *testing.T) {
	cache := newSharedMCPDefinitions()
	oldKey := mcpDefinitionKey{server: "server", tool: "old"}
	storeDefinition := func() {
		cache.getOrCreate(MCPToolInfo{Name: "old", InputSchema: map[string]any{"type": "object"}}, "server", false, 0)
	}
	storeDefinition()
	if cache.size() != 1 {
		t.Fatalf("cache entries = %d, want 1", cache.size())
	}
	deadline := time.Now().Add(5 * time.Second)
	for definitionKeyCount(cache, oldKey) != 0 && time.Now().Before(deadline) {
		runtime.GC()
		// A miss pass prunes entries whose owners were collected.
		cache.getOrCreate(MCPToolInfo{Name: "new", InputSchema: map[string]any{"type": "object"}}, "server", false, 0)
		time.Sleep(time.Millisecond)
	}
	if definitionKeyCount(cache, oldKey) != 0 {
		t.Fatal("collected definition stayed in the shared index")
	}
}

func TestToolExecutorSpecClonesSharedParametersLikeRust(t *testing.T) {
	service := NewMCPService(nil)
	info := MCPToolInfo{
		Name:        "search",
		Description: "Search things",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"q": map[string]any{"type": "string"}},
		},
	}
	newExecutor := func(toolInfo MCPToolInfo) *ToolExecutor {
		return NewToolExecutor(&ToolExecutorOptions{
			Service:                 service,
			ServerName:              "server",
			ToolInfo:                &toolInfo,
			ToolInputSchemaMaxBytes: 2048,
		})
	}
	executor := newExecutor(info)
	first := executor.Spec()
	first.InputSchema["type"] = "mutated"
	second := executor.Spec()
	if second.InputSchema["type"] != "object" {
		t.Fatalf("shared parameters were mutated through the spec: %#v", second.InputSchema)
	}
	if service.sharedDefinitions.size() != 1 {
		t.Fatalf("cache entries = %d, want 1", service.sharedDefinitions.size())
	}

	refreshed := info
	refreshed.InputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
	}
	spec := newExecutor(refreshed).Spec()
	properties, _ := spec.InputSchema["properties"].(map[string]any)
	if _, ok := properties["query"]; !ok {
		t.Fatalf("changed catalog schema reused stale parameters: %#v", spec.InputSchema)
	}
}
