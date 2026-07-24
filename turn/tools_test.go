package turn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/mcp"
	"codex_go/plugin"
	"codex_go/tool"
)

func TestDynamicToolPreservesInlineAudioContent(t *testing.T) {
	items, ok := normalizeDynamicToolContentItems([]DynamicToolCallOutputContentItem{{Type: "inputAudio", AudioURL: "data:audio/wav;base64,YXVkaW8="}})
	if !ok || len(items) != 1 || items[0].Type != "inputAudio" {
		t.Fatalf("items = %#v ok=%v", items, ok)
	}
	modelItems := dynamicToolModelContentItemsAny(items)
	item := modelItems[0].(map[string]any)
	if item["type"] != "input_audio" || item["audio_url"] != "data:audio/wav;base64,YXVkaW8=" {
		t.Fatalf("model item = %#v", item)
	}
}

func TestBuildToolRegistryHonorsToolDisableOptions(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.DisableUpdatePlan = true
	options.DisableWaitAgent = true
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	for _, name := range []tool.ToolName{
		tool.PlainName("update_plan"),
		tool.NamespacedName(agent.MultiAgentV1Namespace, string(agent.MultiAgentToolWait)),
	} {
		if _, ok := registry.Lookup(name); ok {
			t.Fatalf("disabled tool %s was registered", name.Key())
		}
	}
	if _, ok := registry.Lookup(tool.NamespacedName(agent.MultiAgentV1Namespace, string(agent.MultiAgentToolSpawn))); !ok {
		t.Fatal("disabling wait_agent should not disable other agent tools")
	}
}

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

func writeTurnTestMCPResponse(t *testing.T, w http.ResponseWriter, id int64, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}); err != nil {
		t.Fatalf("Encode response = %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
		tool.NamespacedName("mcp__calendar", "create_event"),
		tool.NamespacedName(agent.MultiAgentV1Namespace, string(agent.MultiAgentToolSpawn)),
		tool.PlainName(tool.ToolSearchName),
	} {
		if _, ok := registry.Lookup(name); !ok {
			t.Fatalf("missing tool %s", name.Key())
		}
	}
}

func TestBuildToolRegistryAppliesConfiguredAgentRoles(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.AgentRoles = map[string]agent.RoleConfig{"reviewer": {Description: "Reviews changes."}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	executor, ok := registry.Lookup(tool.NamespacedName(agent.MultiAgentV1Namespace, string(agent.MultiAgentToolSpawn)))
	if !ok || !strings.Contains(executor.Spec().Description, "`reviewer`: Reviews changes.") {
		t.Fatalf("spawn spec = %#v, found=%v", executor, ok)
	}
}

func TestBuildToolRegistryViewImageFollowsModelCapabilityOption(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(default) error = %v", err)
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.ViewImageToolName)); ok {
		t.Fatal("view_image should not be exposed without image model capability")
	}

	options.ViewImage = &tool.ViewImageOptions{CWD: options.Shell.Validation.CWD, CanRequestOriginalDetail: true}
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(view_image) error = %v", err)
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.ViewImageToolName)); !ok {
		t.Fatal("view_image not registered when explicitly enabled")
	}
}

func TestSkillsToolsListAndReadOrchestratorResourcesLikeRust(t *testing.T) {
	var methods []string
	skillPackage := "skill://drive/docs"
	mainResource := skillPackage + "/SKILL.md"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		methods = append(methods, request.Method)
		switch request.Method {
		case "initialize":
			writeTurnTestMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "codex_apps", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeTurnTestMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		case "resources/list":
			writeTurnTestMCPResponse(t, w, request.ID, map[string]any{
				"resources": []map[string]any{
					{
						"uri":         skillPackage,
						"name":        "docs",
						"description": "Use <docs> & keep quotes \"as-is\"",
						"mimeType":    "mcp/skill",
						"_meta": map[string]any{
							"plugin_name": "Drive",
							"skill_name":  "doc search",
						},
					},
					{
						"uri":         "skill://drive/bad",
						"name":        "bad",
						"description": "missing plugin name",
						"mimeType":    "mcp/skill",
						"_meta":       map[string]any{"skill_name": "bad"},
					},
				},
			})
		case "resources/templates/list":
			writeTurnTestMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []any{}})
		case "resources/read":
			var params struct {
				URI string `json:"uri"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatalf("resources/read params = %v", err)
			}
			writeTurnTestMCPResponse(t, w, request.ID, map[string]any{
				"contents": []map[string]string{{"uri": params.URI, "text": "# Docs\nUse this skill."}},
			})
		default:
			writeTurnTestMCPResponse(t, w, request.ID, map[string]any{})
		}
	}))
	defer server.Close()

	mcpService := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		mcp.RuntimeCodexAppsMCPServerName: {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
	}})
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.EnableToolSearch = false
	options.MCPService = mcpService
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	router := tool.NewRouter(registry)
	listOutput, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "skills-list",
		ToolName: tool.NamespacedName("skills", "list"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"orchestrator"}}`},
	})
	if err != nil {
		t.Fatalf("skills.list error = %v", err)
	}
	var listed skillsListResponse
	if err := json.Unmarshal([]byte(listOutput.Body), &listed); err != nil {
		t.Fatalf("skills.list JSON = %v in %s", err, listOutput.Body)
	}
	if len(listed.Skills) != 1 {
		t.Fatalf("listed skills = %#v", listed.Skills)
	}
	got := listed.Skills[0]
	if got.Authority.Kind != "orchestrator" || got.Package != skillPackage || got.MainResource != mainResource || got.Name != "Drive:doc search" {
		t.Fatalf("listed skill = %#v", got)
	}
	if got.Description != "Use &lt;docs&gt; &amp; keep quotes \"as-is\"" {
		t.Fatalf("description = %q", got.Description)
	}
	if len(listed.Warnings) != 1 || listed.Warnings[0] != "Skipped 1 malformed orchestrator skill resources." {
		t.Fatalf("warnings = %#v", listed.Warnings)
	}

	readOutput, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "skills-read",
		ToolName: tool.NamespacedName("skills", "read"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"orchestrator"},"package":"` + skillPackage + `","resource":"` + mainResource + `"}`},
	})
	if err != nil {
		t.Fatalf("skills.read error = %v", err)
	}
	var read skillsReadResponse
	if err := json.Unmarshal([]byte(readOutput.Body), &read); err != nil {
		t.Fatalf("skills.read JSON = %v in %s", err, readOutput.Body)
	}
	if read.Resource != mainResource || !strings.Contains(read.Contents, "Use this skill") {
		t.Fatalf("read = %#v", read)
	}
	if !containsString(methods, "resources/list") || !containsString(methods, "resources/read") {
		t.Fatalf("methods = %#v", methods)
	}
	resourceLists := 0
	resourceReads := 0
	for _, method := range methods {
		if method == "resources/list" {
			resourceLists++
		}
		if method == "resources/read" {
			resourceReads++
		}
	}
	if resourceLists != 1 || resourceReads != 1 {
		t.Fatalf("methods = %#v, want shared list/read catalog cache", methods)
	}
}

func TestSkillsToolsRejectUnsupportedAuthorityLikeRust(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.EnableToolSearch = false
	options.MCPService = mcp.NewMCPService(nil)
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	_, err = tool.NewRouter(registry).Dispatch(context.Background(), &tool.Invocation{
		CallID:   "skills-list",
		ToolName: tool.NamespacedName("skills", "list"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"executor"}}`},
	})
	var callErr *tool.FunctionCallError
	if !errors.As(err, &callErr) || !callErr.RespondsToModel() || !strings.Contains(callErr.ModelMessage(), "expected `orchestrator`") {
		t.Fatalf("error = %#v", err)
	}
}

func TestSkillsListTruncatesCatalogDescriptionLikeRust(t *testing.T) {
	description := strings.Repeat("x", maxCatalogSkillDescriptionChars+1)
	got := truncateCatalogSkillDescription(description)
	if got != strings.Repeat("x", maxCatalogSkillDescriptionChars-len(skillDescriptionTruncatedSuffix))+skillDescriptionTruncatedSuffix {
		t.Fatalf("truncated description len=%d value suffix=%q", len(got), got[len(got)-3:])
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

func TestBuildToolRegistryUnifiedExecFeatureGatesWriteStdinLikeRust(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableUnifiedExec = false
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(disabled) error = %v", err)
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.DefaultWriteStdinToolName)); ok {
		t.Fatal("write_stdin registered while unified_exec disabled")
	}

	options = DefaultToolRegistryOptions(t.TempDir())
	options.EnableUnifiedExec = true
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(enabled) error = %v", err)
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.DefaultWriteStdinToolName)); !ok {
		t.Fatal("write_stdin missing while unified_exec enabled")
	}
	execSpec, ok := registry.Spec(tool.PlainName(tool.DefaultExecCommandToolName))
	if !ok || !strings.Contains(execSpec.Description, "session ID") {
		t.Fatalf("exec_command spec = %#v", execSpec)
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

func TestBuildToolRegistryMCPToolSearchDispatchesUniqueBareName(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableAgents = false
	options.MCPService = mcp.NewMCPService(nil)
	options.MCPTools = []mcp.RuntimeToolInfo{{
		ServerName: "geogebra",
		Tool: mcp.RuntimeTool{
			Name:        "geogebra_create_circle",
			Description: "Create a GeoGebra circle",
			InputSchema: map[string]any{"type": "object"},
		},
	}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	router := tool.NewRouter(registry)
	searchOutput, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "search",
		ToolName: tool.PlainName(tool.ToolSearchName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"query":"geogebra circle"}`},
	})
	if err != nil {
		t.Fatalf("Dispatch(tool_search) error = %v", err)
	}
	var searchResult tool.ToolSearchResult
	if err := json.Unmarshal([]byte(searchOutput.Body), &searchResult); err != nil {
		t.Fatalf("Unmarshal(tool_search) error = %v", err)
	}
	if len(searchResult.Tools) != 1 || searchResult.Tools[0].Name.Key() != "mcp__geogebra.geogebra_create_circle" {
		t.Fatalf("tool_search result = %#v", searchResult.Tools)
	}

	invocation, ok, err := router.BuildToolCall(tool.ResponseItem{
		Type:      "function_call",
		Name:      "geogebra_create_circle",
		CallID:    "call-circle",
		Arguments: `{"radius":3}`,
	})
	if err != nil || !ok {
		t.Fatalf("BuildToolCall() ok=%v err=%v", ok, err)
	}
	if invocation.ToolName.Key() != "mcp__geogebra.geogebra_create_circle" {
		t.Fatalf("resolved tool = %s", invocation.ToolName.Key())
	}
	output, err := router.Dispatch(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Dispatch(MCP) error = %v", err)
	}
	if !output.Success || output.Data["server"] != "geogebra" || output.Data["tool"] != "geogebra_create_circle" {
		t.Fatalf("MCP output = %#v", output)
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
	if _, ok := registry.Lookup(tool.NamespacedName("mcp__drive", "create_doc")); !ok {
		t.Fatal("mcp tool missing")
	}
	visible := specKeySet(registry.ModelVisibleSpecs())
	if !visible["mcp__drive.create_doc"] {
		t.Fatalf("model-visible specs = %#v, want mcp__drive.create_doc", visible)
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
	if !visibleSpecs[tool.ToolSearchName] || visibleSpecs["mcp__drive.read"] || visibleSpecs["mcp__codex_apps__calendar.create_event"] {
		t.Fatalf("model-visible specs = %#v, want only tool_search from MCP surface", visibleSpecs)
	}
	discoverable := specKeySet(registry.DiscoverableSpecs())
	for _, want := range []string{"mcp__drive.read", "mcp__codex_apps__calendar.create_event"} {
		if !discoverable[want] {
			t.Fatalf("discoverable specs = %#v, missing %s", discoverable, want)
		}
	}
	for _, unwanted := range []string{"mcp__drive.secret", "mcp__codex_apps__mail.send_mail"} {
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
	executor, ok := registry.Lookup(tool.NamespacedName("mcp__memory", "create_entities"))
	if !ok {
		t.Fatal("mcp tool missing")
	}
	provider, ok := executor.(tool.PreToolUsePayloadProvider)
	if !ok {
		t.Fatalf("wrapped MCP executor lost PreToolUsePayloadProvider")
	}
	payload, ok := provider.PreToolUsePayload(&tool.Invocation{
		ToolName: tool.NamespacedName("mcp__memory", "create_entities"),
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

func TestDynamicToolClientErrorUsesRustFallbackResponse(t *testing.T) {
	caller := &fakeDynamicToolCaller{err: errors.New("client disconnected")}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.DynamicToolCaller = caller
	options.DynamicTools = []DynamicToolSpec{{Type: "function", Function: &DynamicToolFunctionSpec{
		Name: "demo_tool", InputSchema: map[string]any{"type": "object"},
	}}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	executor, ok := registry.Lookup(tool.PlainName("demo_tool"))
	if !ok {
		t.Fatal("dynamic tool missing")
	}
	output, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID: "dyn-fallback", ToolName: tool.PlainName("demo_tool"),
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Success || output.Body != "dynamic tool request failed" || output.Error != output.Body {
		t.Fatalf("output = %#v", output)
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
