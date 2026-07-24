package mcp

import "testing"

func TestNormalizeRuntimeToolsForModelMatchesRustNames(t *testing.T) {
	tools := NormalizeRuntimeToolsForModel([]RuntimeToolInfo{
		{ServerName: "geogebra", Tool: RuntimeTool{Name: "geogebra_create_circle"}},
		{ServerName: "music-studio", Tool: RuntimeTool{Name: "get-strudel-guide"}},
	})
	if len(tools) != 2 {
		t.Fatalf("tools = %#v", tools)
	}
	if tools[0].ServerName != "geogebra" || tools[0].Tool.Name != "geogebra_create_circle" ||
		tools[0].CallableNamespace != "mcp__geogebra" || tools[0].CallableName != "geogebra_create_circle" {
		t.Fatalf("geogebra tool = %#v", tools[0])
	}
	if tools[1].ServerName != "music-studio" || tools[1].Tool.Name != "get-strudel-guide" ||
		tools[1].CallableNamespace != "mcp__music_studio" || tools[1].CallableName != "get_strudel_guide" {
		t.Fatalf("music tool = %#v", tools[1])
	}

	again := NormalizeRuntimeToolsForModel(tools)
	if len(again) != len(tools) || again[0].CallableNamespace != tools[0].CallableNamespace || again[0].CallableName != tools[0].CallableName {
		t.Fatalf("normalization is not idempotent: first=%#v second=%#v", tools, again)
	}
}

func TestNormalizeRuntimeToolsForModelOmitsLegacyPrefixSelectively(t *testing.T) {
	tools := NormalizeRuntimeToolsForModel([]RuntimeToolInfo{
		{ServerName: "docs", OmitLegacyPrefix: true, Tool: RuntimeTool{Name: "search"}},
		{ServerName: "memory", Tool: RuntimeTool{Name: "read"}},
	})
	if tools[0].CallableNamespace != "docs" {
		t.Fatalf("selected namespace = %q, want docs", tools[0].CallableNamespace)
	}
	if tools[1].CallableNamespace != "mcp__memory" {
		t.Fatalf("legacy namespace = %q, want mcp__memory", tools[1].CallableNamespace)
	}
	again := NormalizeRuntimeToolsForModel(tools)
	if again[0].CallableNamespace != "docs" || again[1].CallableNamespace != "mcp__memory" {
		t.Fatalf("normalization is not idempotent: %#v", again)
	}
}

func TestNormalizeRuntimeToolsForModelDisambiguatesCollisions(t *testing.T) {
	tools := NormalizeRuntimeToolsForModel([]RuntimeToolInfo{
		{ServerName: "basic-server", Tool: RuntimeTool{Name: "lookup"}},
		{ServerName: "basic_server", Tool: RuntimeTool{Name: "lookup"}},
		{ServerName: "same", Tool: RuntimeTool{Name: "tool-name"}},
		{ServerName: "same", Tool: RuntimeTool{Name: "tool_name"}},
	})
	if len(tools) != 4 {
		t.Fatalf("tools = %#v", tools)
	}
	seen := map[string]bool{}
	for _, info := range tools {
		key := info.CallableNamespace + "__" + info.CallableName
		if seen[key] {
			t.Fatalf("duplicate callable name %q in %#v", key, tools)
		}
		seen[key] = true
		if len(key) > runtimeMCPToolNameMaxLength {
			t.Fatalf("callable name too long: %q", key)
		}
	}
	if tools[0].CallableNamespace == tools[1].CallableNamespace {
		t.Fatalf("sanitized namespace collision was not resolved: %#v", tools[:2])
	}
	if tools[2].CallableName == tools[3].CallableName {
		t.Fatalf("sanitized tool collision was not resolved: %#v", tools[2:])
	}
	again := NormalizeRuntimeToolsForModel(tools)
	for i := range tools {
		if again[i].CallableNamespace != tools[i].CallableNamespace || again[i].CallableName != tools[i].CallableName {
			t.Fatalf("collision normalization changed at %d: first=%#v second=%#v", i, tools[i], again[i])
		}
	}
}

func TestBuildExposureDefersToolsWhenSearchEnabled(t *testing.T) {
	visible := true
	hidden := false
	exposure := BuildRuntimeExposure([]RuntimeToolInfo{
		{ServerName: "local", Tool: RuntimeTool{Name: "b", ModelVisible: &visible}},
		{ServerName: "local", Tool: RuntimeTool{Name: "hidden", ModelVisible: &hidden}},
		{ServerName: RuntimeCodexAppsMCPServerName, ConnectorID: "calendar", Tool: RuntimeTool{Name: "create", ModelVisible: &visible}},
		{ServerName: RuntimeCodexAppsMCPServerName, ConnectorID: " docs ", Tool: RuntimeTool{Name: "lookup", ModelVisible: &visible}},
		{ServerName: RuntimeCodexAppsMCPServerName, ConnectorID: "mail", Tool: RuntimeTool{Name: "send", ModelVisible: &visible}},
	}, []RuntimeConnector{{ID: "calendar", Enabled: true}, {ID: " docs ", Enabled: true}}, true)
	if len(exposure.DirectTools) != 0 {
		t.Fatalf("direct tools should be empty with search enabled")
	}
	if len(exposure.DeferredTools) != 3 {
		t.Fatalf("unexpected deferred tools: %#v", exposure.DeferredTools)
	}
	for _, tool := range exposure.DeferredTools {
		if tool.Tool.Name == "lookup" && tool.ConnectorID != "docs" {
			t.Fatalf("lookup connector id = %q, want canonical docs", tool.ConnectorID)
		}
	}
}

func TestBuildExposureDirectWhenSearchDisabled(t *testing.T) {
	exposure := BuildRuntimeExposure([]RuntimeToolInfo{{ServerName: "local", Tool: RuntimeTool{Name: "read"}}}, nil, false)
	if len(exposure.DirectTools) != 1 || len(exposure.DeferredTools) != 0 {
		t.Fatalf("unexpected exposure: %#v", exposure)
	}
}

func TestRuntimeToolsFromStatusesMatchesRustMCPInventory(t *testing.T) {
	inputSchema := map[string]any{"type": "object"}
	statuses := []MCPServerStatus{
		{
			Name:  "broken",
			State: MCPServerFailed,
			Tools: []MCPToolInfo{{
				Name: "ignored_failed",
			}},
		},
		{
			Name:  "docs",
			State: MCPServerReady,
			Tools: []MCPToolInfo{
				{
					Name:        " search ",
					Title:       " Search ",
					Description: " Search docs ",
					InputSchema: inputSchema,
					Annotations: map[string]any{"readOnlyHint": true},
					Meta:        map[string]any{"modelVisible": "false"},
				},
				{
					Name: "link",
					Meta: map[string]any{"synthetic_link": true},
				},
			},
		},
		{
			Name:  CodexAppsServerName,
			State: MCPServerReady,
			Tools: []MCPToolInfo{
				{
					Name: "drive_search",
					Meta: map[string]any{
						"_codex_apps": map[string]any{
							"connectorId":          "drive",
							"connectorDescription": "Drive files",
							"pluginDisplayNames":   []any{"Drive Plugin"},
						},
					},
				},
				{
					Name: "drive_link",
					Meta: map[string]any{
						"_codex_apps": map[string]any{
							"synthetic_link": true,
						},
					},
				},
			},
		},
	}

	tools := RuntimeToolsFromStatuses(statuses)
	if len(tools) != 2 {
		t.Fatalf("tools = %#v, want docs search and drive search only", tools)
	}
	docs := tools[0]
	if docs.ServerName != "docs" || docs.CallableNamespace != "mcp__docs" || docs.CallableName != "search" || docs.Tool.Name != "search" || docs.Tool.Title != "Search" || docs.Tool.Description != "Search docs" {
		t.Fatalf("docs tool = %#v", docs)
	}
	if docs.Tool.InputSchema["type"] != "object" {
		t.Fatalf("input schema = %#v", docs.Tool.InputSchema)
	}
	inputSchema["type"] = "mutated"
	if docs.Tool.InputSchema["type"] != "object" {
		t.Fatalf("input schema was not cloned: %#v", docs.Tool.InputSchema)
	}
	if docs.Tool.Annotations == nil || docs.Tool.Annotations.ReadOnlyHint == nil || !*docs.Tool.Annotations.ReadOnlyHint {
		t.Fatalf("annotations = %#v", docs.Tool.Annotations)
	}
	if docs.Tool.ModelVisible == nil || *docs.Tool.ModelVisible {
		t.Fatalf("model visible = %#v", docs.Tool.ModelVisible)
	}

	drive := tools[1]
	if drive.ServerName != RuntimeCodexAppsMCPServerName || drive.ConnectorID != "drive" {
		t.Fatalf("drive tool identity = %#v", drive)
	}
	if drive.Tool.Description != "Drive files" {
		t.Fatalf("drive description = %q", drive.Tool.Description)
	}
	if len(drive.PluginDisplayNames) != 1 || drive.PluginDisplayNames[0] != "Drive Plugin" {
		t.Fatalf("plugin display names = %#v", drive.PluginDisplayNames)
	}
}

func TestAnnotateRuntimeToolsWithConnectorPluginProvenance(t *testing.T) {
	provenance := NewConnectorPluginProvenance()
	provenance.Add(" drive ", "Docs")
	provenance.Add("drive", "Archive")
	tools := AnnotateRuntimeToolsWithConnectorPluginProvenance([]RuntimeToolInfo{{
		ServerName:  RuntimeCodexAppsMCPServerName,
		ConnectorID: " drive ",
		Tool:        RuntimeTool{Name: "search", Description: "Search Drive"},
	}}, provenance)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", tools)
	}
	if got := tools[0].PluginDisplayNames; len(got) != 2 || got[0] != "Archive" || got[1] != "Docs" {
		t.Fatalf("plugin names = %#v", got)
	}
	wantDescription := "Search Drive. This tool is part of plugins `Archive`, `Docs`."
	if tools[0].Tool.Description != wantDescription {
		t.Fatalf("description = %q", tools[0].Tool.Description)
	}
	connectors := ConnectorAccessibleFromMCPTools(ConnectorToolInfoFromRuntimeTools(tools))
	if len(connectors) != 1 || connectors[0].ID != "drive" || len(connectors[0].PluginDisplayNames) != 2 {
		t.Fatalf("connectors = %#v", connectors)
	}
}

func TestAnnotateRuntimeToolWithSinglePluginProvenance(t *testing.T) {
	tool := RuntimeToolInfo{Tool: RuntimeTool{Name: "read"}}
	AnnotateRuntimeToolWithPluginProvenance(&tool, []string{"Docs", "Docs", ""})
	if tool.Tool.Description != "This tool is part of plugin `Docs`." {
		t.Fatalf("description = %q", tool.Tool.Description)
	}
}

func TestCollectMissingDependencies(t *testing.T) {
	skills := []RuntimeSkillMetadata{{Name: "skill", Dependencies: []RuntimeDependency{
		{Type: "mcp", Value: "calendar", URL: "https://mcp.example.test"},
		{Type: "mcp", Value: "shell", Transport: "stdio", Command: "tool-server"},
	}}}
	installed := map[string]RuntimeServerConfig{
		"calendar": {Transport: "streamable_http", URL: "https://mcp.example.test", Enabled: true},
		"shell":    {Transport: "stdio", Command: "tool-server", Enabled: false},
	}
	missing := CollectMissingRuntimeDependencies(skills, installed)
	if len(missing) != 1 || missing["shell"].Command != "tool-server" || missing["shell"].Required {
		t.Fatalf("unexpected missing dependencies: %#v", missing)
	}
	if FormatMissingRuntimeDependencies(missing) != "shell" {
		t.Fatalf("unexpected formatted missing dependencies")
	}
}

func TestCollectMissingDependenciesNormalizesTransportAliases(t *testing.T) {
	skills := []RuntimeSkillMetadata{{Name: "skill", Dependencies: []RuntimeDependency{
		{Type: "mcp", Value: "calendar", Transport: "streamable-http", URL: "https://mcp.example.test"},
		{Type: "mcp", Value: "docs", Transport: "sse", URL: "https://docs.example.test/mcp"},
		{Type: "mcp", Value: "shell", Command: "shell-mcp"},
	}}}
	installed := map[string]RuntimeServerConfig{
		"calendar": {Transport: "http", URL: "https://mcp.example.test", Enabled: true},
		"shell":    {Command: "shell-mcp", Enabled: true},
	}
	missing := CollectMissingRuntimeDependencies(skills, installed)
	if len(missing) != 1 {
		t.Fatalf("missing dependencies = %#v", missing)
	}
	if got := missing["docs"]; got.Transport != "streamable_http" || got.URL != "https://docs.example.test/mcp" || got.Required {
		t.Fatalf("docs dependency = %#v", got)
	}
	if FormatMissingRuntimeDependencies(missing) != "docs" {
		t.Fatalf("formatted missing dependencies = %q", FormatMissingRuntimeDependencies(missing))
	}
}

func TestCollectMissingDependenciesTrimsDependencyNames(t *testing.T) {
	skills := []RuntimeSkillMetadata{{Name: "skill", Dependencies: []RuntimeDependency{
		{Type: "mcp", Value: " docs ", URL: "https://docs.example.test/mcp"},
		{Type: "mcp", Value: " docs ", URL: "https://docs.example.test/mcp"},
	}}}
	missing := CollectMissingRuntimeDependencies(skills, nil)
	if len(missing) != 1 {
		t.Fatalf("missing dependencies = %#v", missing)
	}
	if _, ok := missing[" docs "]; ok {
		t.Fatalf("missing dependencies kept untrimmed key: %#v", missing)
	}
	if got := missing["docs"]; got.Name != "docs" || got.URL != "https://docs.example.test/mcp" {
		t.Fatalf("docs dependency = %#v", got)
	}
	if FormatMissingRuntimeDependencies(missing) != "docs" {
		t.Fatalf("formatted missing dependencies = %q", FormatMissingRuntimeDependencies(missing))
	}
}

func TestRenderRuntimeApprovalTemplate(t *testing.T) {
	templates := []RuntimeApprovalTemplate{{
		ConnectorID: "calendar",
		ServerName:  RuntimeCodexAppsMCPServerName,
		ToolTitle:   "create_event",
		Template:    "Allow {connector_name} to create an event?",
		TemplateParams: []RuntimeApprovalTemplateParam{
			{Name: "calendar_id", Label: "Calendar"},
			{Name: "title", Label: "Title"},
		},
	}}
	rendered, ok := RenderRuntimeApprovalTemplate(templates, RuntimeCodexAppsMCPServerName, "calendar", "Calendar", "create_event", map[string]any{
		"title":       "Roadmap",
		"calendar_id": "primary",
		"timezone":    "UTC",
	})
	if !ok {
		t.Fatalf("expected template to render")
	}
	if rendered.Question != "Allow Calendar to create an event?" {
		t.Fatalf("unexpected question: %q", rendered.Question)
	}
	if len(rendered.ToolParamsDisplay) != 3 || rendered.ToolParamsDisplay[0].DisplayName != "Calendar" {
		t.Fatalf("unexpected display params: %#v", rendered.ToolParamsDisplay)
	}
}

func TestRenderApprovalTemplateRejectsCollidingLabels(t *testing.T) {
	templates := []RuntimeApprovalTemplate{{
		ConnectorID:    "calendar",
		ServerName:     RuntimeCodexAppsMCPServerName,
		ToolTitle:      "create_event",
		Template:       "Allow Calendar?",
		TemplateParams: []RuntimeApprovalTemplateParam{{Name: "calendar_id", Label: "timezone"}},
	}}
	_, ok := RenderRuntimeApprovalTemplate(templates, RuntimeCodexAppsMCPServerName, "calendar", "", "create_event", map[string]any{
		"calendar_id": "primary",
		"timezone":    "UTC",
	})
	if ok {
		t.Fatalf("expected collision to reject template")
	}
}
