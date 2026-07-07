package turn

import (
	"context"
	"strings"
	"testing"

	"codex_go/internal/agent"
	"codex_go/internal/mcp"
	"codex_go/internal/plugin"
	"codex_go/internal/tool"
)

type fakeDynamicToolCaller struct {
	method string
	params *DynamicToolCallParams
	result *DynamicToolCallResponse
	err    error
}

func (c *fakeDynamicToolCaller) Request(ctx context.Context, method string, params any, target any) error {
	_ = ctx
	c.method = method
	if typed, ok := params.(*DynamicToolCallParams); ok {
		copied := *typed
		c.params = &copied
	}
	if c.err != nil {
		return c.err
	}
	if c.result == nil {
		return nil
	}
	if typed, ok := target.(*DynamicToolCallResponse); ok {
		*typed = *c.result
	}
	return nil
}

func TestBuildToolRegistryIncludesCoreAndRuntimeTools(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.MCPTools = []mcp.RuntimeToolInfo{{
		ServerName: "calendar",
		Tool: mcp.RuntimeTool{
			Name:        "create_event",
			Description: "Create calendar events",
		},
	}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	for _, name := range []tool.ToolName{
		tool.PlainName("update_plan"),
		tool.PlainName(tool.DefaultExecCommandToolName),
		tool.PlainName(tool.DefaultApplyPatchToolName),
		tool.NamespacedName("calendar", "create_event"),
		tool.NamespacedName(agent.MultiAgentV1Namespace, string(agent.MultiAgentToolSpawn)),
		tool.PlainName(tool.ToolSearchName),
	} {
		if _, ok := registry.Lookup(name); !ok {
			t.Fatalf("missing tool %s", name.Key())
		}
	}
}

func TestBuildToolRegistryClockToolsFollowOptions(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(default) error = %v", err)
	}
	if _, ok := registry.Lookup(tool.NamespacedName("clock", "curr_time")); ok {
		t.Fatal("clock.curr_time should be disabled by default")
	}
	if _, ok := registry.Lookup(tool.NamespacedName("clock", "sleep")); ok {
		t.Fatal("clock.sleep should be disabled by default")
	}
	if _, ok := registry.Lookup(tool.PlainName("sleep")); ok {
		t.Fatal("legacy plain sleep should not be part of turn core tools")
	}

	options = DefaultToolRegistryOptions(t.TempDir())
	options.EnableCurrentTimeTool = true
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(current time) error = %v", err)
	}
	if _, ok := registry.Lookup(tool.NamespacedName("clock", "curr_time")); !ok {
		t.Fatal("clock.curr_time missing")
	}
	if _, ok := registry.Lookup(tool.NamespacedName("clock", "sleep")); ok {
		t.Fatal("clock.sleep should follow sleep_tool")
	}

	options.EnableSleepTool = true
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(sleep) error = %v", err)
	}
	if _, ok := registry.Lookup(tool.NamespacedName("clock", "sleep")); !ok {
		t.Fatal("clock.sleep missing")
	}
}

func TestBuildToolRegistryToolSearchFindsDeferredTools(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.MCPTools = []mcp.RuntimeToolInfo{{
		ServerName: "drive",
		Tool: mcp.RuntimeTool{
			Name:        "create_doc",
			Description: "Create Google Docs files",
		},
	}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	output, err := tool.NewRouter(registry).Dispatch(context.Background(), &tool.Invocation{
		CallID:   "search",
		ToolName: tool.PlainName(tool.ToolSearchName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"query":"google docs create"}`},
	})
	if err != nil {
		t.Fatalf("Dispatch(tool_search) error = %v", err)
	}
	if !strings.Contains(output.Body, "create_doc") || strings.Contains(output.Body, "exposure") {
		t.Fatalf("tool_search output = %q", output.Body)
	}
}

func TestBuildToolRegistryMCPToolsDirectWhenToolSearchDisabled(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableAgents = false
	options.EnableToolSearch = false
	options.MCPTools = []mcp.RuntimeToolInfo{{
		ServerName: "drive",
		Tool: mcp.RuntimeTool{
			Name:        "create_doc",
			Description: "Create Google Docs files",
		},
	}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.ToolSearchName)); ok {
		t.Fatal("tool_search should not be registered when disabled")
	}
	if _, ok := registry.Lookup(tool.NamespacedName("drive", "create_doc")); !ok {
		t.Fatal("mcp tool missing")
	}
	visible := specKeySet(registry.ModelVisibleSpecs())
	if !visible["drive.create_doc"] {
		t.Fatalf("model-visible specs = %#v, want drive.create_doc", visible)
	}
	if discoverable := registry.DiscoverableSpecs(); len(discoverable) != 0 {
		t.Fatalf("discoverable specs = %#v, want none when tool_search is disabled", specKeySet(discoverable))
	}
}

func TestBuildToolRegistryMCPExposureFiltersHiddenAndDisabledConnectors(t *testing.T) {
	visible := true
	hidden := false
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableAgents = false
	options.MCPTools = []mcp.RuntimeToolInfo{
		{ServerName: "drive", Tool: mcp.RuntimeTool{Name: "read", Description: "Read Drive files", ModelVisible: &visible}},
		{ServerName: "drive", Tool: mcp.RuntimeTool{Name: "secret", Description: "Hidden Drive tool", ModelVisible: &hidden}},
		{ServerName: mcp.RuntimeCodexAppsMCPServerName, ConnectorID: "calendar", Tool: mcp.RuntimeTool{Name: "create_event", Description: "Create calendar event", ModelVisible: &visible}},
		{ServerName: mcp.RuntimeCodexAppsMCPServerName, ConnectorID: "mail", Tool: mcp.RuntimeTool{Name: "send_mail", Description: "Send mail", ModelVisible: &visible}},
	}
	options.MCPConnectors = []mcp.RuntimeConnector{
		{ID: "calendar", Enabled: true},
		{ID: "mail", Enabled: false},
	}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	visibleSpecs := specKeySet(registry.ModelVisibleSpecs())
	if !visibleSpecs[tool.ToolSearchName] || visibleSpecs["drive.read"] || visibleSpecs["codex_apps.create_event"] {
		t.Fatalf("model-visible specs = %#v, want only tool_search from MCP surface", visibleSpecs)
	}
	discoverable := specKeySet(registry.DiscoverableSpecs())
	for _, want := range []string{"drive.read", "codex_apps.create_event"} {
		if !discoverable[want] {
			t.Fatalf("discoverable specs = %#v, missing %s", discoverable, want)
		}
	}
	for _, unwanted := range []string{"drive.secret", "codex_apps.send_mail"} {
		if discoverable[unwanted] {
			t.Fatalf("discoverable specs = %#v, should not include %s", discoverable, unwanted)
		}
	}
}

func TestBuildToolRegistryMCPWrapperKeepsHookInterfaces(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableAgents = false
	options.MCPTools = []mcp.RuntimeToolInfo{{
		ServerName: "memory",
		Tool:       mcp.RuntimeTool{Name: "create_entities"},
	}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	executor, ok := registry.Lookup(tool.NamespacedName("memory", "create_entities"))
	if !ok {
		t.Fatal("mcp tool missing")
	}
	provider, ok := executor.(tool.PreToolUsePayloadProvider)
	if !ok {
		t.Fatalf("wrapped MCP executor lost PreToolUsePayloadProvider")
	}
	payload, ok := provider.PreToolUsePayload(&tool.Invocation{
		ToolName: tool.NamespacedName("memory", "create_entities"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"name":"Ada"}`},
	})
	if !ok || payload.ToolName == nil || payload.ToolName.Name != "mcp__memory__create_entities" {
		t.Fatalf("payload = %#v/%v", payload, ok)
	}
}

func TestBuildToolRegistryRegistersPluginInstallSuggestionTools(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.EnableToolSearch = false
	options.PluginInstallCandidates = []plugin.DiscoverableInfo{{ID: "docs@market", Name: "Docs"}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.ListAvailablePluginsToInstallToolName)); !ok {
		t.Fatal("list_available_plugins_to_install missing")
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.RequestPluginInstallToolName)); !ok {
		t.Fatal("request_plugin_install missing")
	}
	modelVisible := specKeySet(registry.ModelVisibleSpecs())
	if !modelVisible[tool.ListAvailablePluginsToInstallToolName] || !modelVisible[tool.RequestPluginInstallToolName] {
		t.Fatalf("plugin install tools should be model-visible: %#v", modelVisible)
	}
	if discoverable := specKeySet(registry.DiscoverableSpecs()); discoverable[tool.ListAvailablePluginsToInstallToolName] || discoverable[tool.RequestPluginInstallToolName] {
		t.Fatalf("plugin install tools should not be discoverable: %#v", discoverable)
	}

	options.PluginInstallRecommendationContext = true
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(recommendation) error = %v", err)
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.ListAvailablePluginsToInstallToolName)); ok {
		t.Fatal("list_available_plugins_to_install should not be registered for recommendation context")
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.RequestPluginInstallToolName)); !ok {
		t.Fatal("request_plugin_install missing for recommendation context")
	}
	modelVisible = specKeySet(registry.ModelVisibleSpecs())
	if !modelVisible[tool.RequestPluginInstallToolName] || modelVisible[tool.ListAvailablePluginsToInstallToolName] {
		t.Fatalf("recommendation plugin tools = %#v, want only request_plugin_install", modelVisible)
	}
}

func TestBuildToolRegistryRegistersDynamicTools(t *testing.T) {
	caller := &fakeDynamicToolCaller{result: &DynamicToolCallResponse{
		Success: true,
		ContentItems: []DynamicToolCallOutputContentItem{{
			Type: "inputText",
			Text: "dynamic-ok",
		}},
	}}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.DynamicToolCaller = caller
	options.ThreadID = "thread-1"
	options.TurnID = "turn-1"
	options.DynamicTools = []DynamicToolSpec{{
		Type: "namespace",
		Namespace: &DynamicToolNamespaceSpec{
			Name:        "codex_app",
			Description: "Demo namespace tools",
			Tools: []DynamicToolFunctionSpec{{
				Name:        "demo_tool",
				Description: "Demo dynamic tool",
				InputSchema: map[string]any{"type": "object"},
			}},
		},
	}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	executor, ok := registry.Lookup(tool.NamespacedName("codex_app", "demo_tool"))
	if !ok {
		t.Fatal("dynamic tool missing")
	}
	output, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "dyn-call-1",
		ToolName: tool.NamespacedName("codex_app", "demo_tool"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"city":"Paris"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if caller.method != "item/tool/call" || caller.params == nil || caller.params.ThreadID != "thread-1" || caller.params.TurnID != "turn-1" {
		t.Fatalf("caller = %#v method=%s", caller.params, caller.method)
	}
	if caller.params.Namespace == nil || caller.params.Tool != "demo_tool" {
		t.Fatalf("params = %#v", caller.params)
	}
	if !output.Success || output.Body != "dynamic-ok" || output.Data["dynamicToolCall"] != true {
		t.Fatalf("output = %#v", output)
	}
	items, ok := output.Data["content_items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("model content_items = %#v", output.Data["content_items"])
	}
}

func TestDynamicToolRemoteImageOutputBecomesModelVisibleError(t *testing.T) {
	caller := &fakeDynamicToolCaller{result: &DynamicToolCallResponse{
		Success: true,
		ContentItems: []DynamicToolCallOutputContentItem{{
			Type:     "inputImage",
			ImageURL: "https://example.com/tool.png",
		}},
	}}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.DynamicToolCaller = caller
	options.ThreadID = "thread-1"
	options.TurnID = "turn-1"
	options.DynamicTools = []DynamicToolSpec{{
		Type: "function",
		Function: &DynamicToolFunctionSpec{
			Name:        "demo_tool",
			Description: "Demo dynamic tool",
			InputSchema: map[string]any{"type": "object"},
		},
	}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	executor, ok := registry.Lookup(tool.PlainName("demo_tool"))
	if !ok {
		t.Fatal("dynamic tool missing")
	}
	output, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "dyn-call-remote-image",
		ToolName: tool.PlainName("demo_tool"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Success || output.Body != remoteImageURLError {
		t.Fatalf("output = %#v", output)
	}
	items, ok := output.Data["contentItems"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("contentItems = %#v", output.Data["contentItems"])
	}
	modelItems, ok := output.Data["content_items"].([]any)
	if !ok || len(modelItems) != 1 {
		t.Fatalf("content_items = %#v", output.Data["content_items"])
	}
	if !strings.Contains(inputItemTextForDynamicToolTest(items), remoteImageURLError) ||
		!strings.Contains(inputItemTextForDynamicToolTest(modelItems), remoteImageURLError) {
		t.Fatalf("content items = %#v model = %#v", items, modelItems)
	}
}

func inputItemTextForDynamicToolTest(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := []string{}
		for _, item := range typed {
			parts = append(parts, inputItemTextForDynamicToolTest(item))
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		parts := []string{}
		for _, item := range typed {
			parts = append(parts, inputItemTextForDynamicToolTest(item))
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func specKeySet(specs []tool.Spec) map[string]bool {
	out := make(map[string]bool, len(specs))
	for _, spec := range specs {
		if key := spec.Name.Key(); key != "" {
			out[key] = true
		}
	}
	return out
}
