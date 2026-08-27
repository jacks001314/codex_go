package turn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"codex_go/agent"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/plugin"
	"codex_go/skillprovider"
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

func TestMcpActorConfirmationPoliciesMapping(t *testing.T) {
	browser := "# Browser confirmations\n\nKeep.\n"
	computer := "# Native confirmations."
	cp := &model.ConfirmationPolicies{BrowserUse: &browser, ComputerUse: &computer}
	actor := mcpActorConfirmationPolicies(cp)
	if actor == nil || actor.BrowserUse != browser || actor.ComputerUse != computer {
		t.Fatalf("actor confirmation policies = %#v", actor)
	}
	// Empty-string overrides are preserved (Rust forwards text verbatim).
	empty := ""
	cpEmpty := &model.ConfirmationPolicies{BrowserUse: &empty}
	actorEmpty := mcpActorConfirmationPolicies(cpEmpty)
	if actorEmpty == nil || actorEmpty.BrowserUse != "" || actorEmpty.ComputerUse != "" {
		t.Fatalf("empty override actor policies = %#v", actorEmpty)
	}
	// A nil input maps to nil so the executor attaches an empty object.
	if mcpActorConfirmationPolicies(nil) != nil {
		t.Fatal("nil model confirmation policies should map to nil actor policies")
	}
}

func TestSkillsReadAliasResolutionAndExecutorSkillRoot(t *testing.T) {
	entries := []skillprovider.CatalogEntry{
		{Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "root-1"}, PackageID: "skill://root-1/skills/foo/SKILL.md", MainResource: "skill://root-1/skills/foo/SKILL.md", Enabled: true, PromptVisible: true},
		{Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "root-1"}, PackageID: "skill://root-1/skills/bar/SKILL.md", MainResource: "skill://root-1/skills/bar/SKILL.md", Enabled: true, PromptVisible: true},
	}
	for _, tt := range []struct {
		packageID string
		candidate string
		want      bool
	}{
		{packageID: "skill://root-1/skills/foo/SKILL.md", candidate: "skill://root-1/skills/foo/SKILL.md", want: true},
		{packageID: "skill://root-1/skills/foo/SKILL.md", candidate: "e0/foo/SKILL.md", want: true},
		{packageID: "skill://root-1/skills/bar/SKILL.md", candidate: "e0/bar/SKILL.md", want: true},
		{packageID: "skill://root-1/skills/foo/SKILL.md", candidate: "e0/bar/SKILL.md", want: false},
	} {
		if got := skillPackageMatchesAlias(entries, tt.packageID, tt.candidate); got != tt.want {
			t.Fatalf("skillPackageMatchesAlias(%q, %q) = %v, want %v", tt.packageID, tt.candidate, got, tt.want)
		}
	}
	if got := executorSkillRoot("skill://root-1/skills/foo/SKILL.md"); got != "skill://root-1/skills/foo" {
		t.Fatalf("executorSkillRoot() = %q", got)
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
		tool.PlainName(tool.DefaultShellCommandToolName),
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
	mainReadOutput, err := router.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "skills-read-main",
		ToolName: tool.NamespacedName("skills", "read"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"orchestrator"},"package":"` + skillPackage + `"}`},
	})
	if err != nil {
		t.Fatalf("skills.read without resource error = %v", err)
	}
	var mainRead skillsReadResponse
	if err := json.Unmarshal([]byte(mainReadOutput.Body), &mainRead); err != nil {
		t.Fatalf("skills.read without resource JSON = %v in %s", err, mainReadOutput.Body)
	}
	if mainRead.Resource != mainResource || !strings.Contains(mainRead.Contents, "Use this skill") {
		t.Fatalf("omitted-resource read = %#v, want main resource %q", mainRead, mainResource)
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
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"host"}}`},
	})
	var callErr *tool.FunctionCallError
	if !errors.As(err, &callErr) || !callErr.RespondsToModel() || !strings.Contains(callErr.ModelMessage(), "expected `orchestrator` or `executor`") {
		t.Fatalf("error = %#v", err)
	}
}

func TestExecutorSkillsToolsListReadPaginateAndValidateAuthorityLikeRust(t *testing.T) {
	visible := skillprovider.CatalogEntry{
		PackageID: "skill://demo@1/plugin/skills/deploy",
		Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: "demo@1"},
		Name:      "demo:deploy", Description: "Deploy through the executor.",
		MainResource: "skill://demo@1/plugin/skills/deploy/SKILL.md", Enabled: true, PromptVisible: true,
	}
	hidden := visible
	hidden.PackageID = "skill://demo@1/plugin/skills/hidden"
	hidden.MainResource = hidden.PackageID + "/SKILL.md"
	hidden.Name = "demo:hidden"
	hidden.PromptVisible = false
	contents := "MARKER\n" + strings.Repeat("x", maxSkillToolResponseBytes+1024)
	provider := skillprovider.ProviderFuncs{
		ListFunc: func(context.Context, skillprovider.ListQuery) (skillprovider.Catalog, error) {
			return skillprovider.Catalog{Entries: []skillprovider.CatalogEntry{visible, hidden}}, nil
		},
		ReadFunc: func(_ context.Context, request skillprovider.ReadRequest) (skillprovider.ReadResult, error) {
			if request.Authority.ID != "demo@1" || request.Resource == "" || request.PackageID != visible.PackageID && request.PackageID != hidden.PackageID {
				return skillprovider.ReadResult{}, errors.New("unexpected read request")
			}
			return skillprovider.ReadResult{Resource: request.Resource, Contents: contents}, nil
		},
	}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableMCP = false
	options.EnableAgents = false
	options.EnableToolSearch = false
	options.SkillProviders = skillprovider.NewRegistry(skillprovider.Source{Kind: skillprovider.SourceExecutor, Label: "executor", Provider: provider})
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	router := tool.NewRouter(registry)
	listOutput, err := router.Dispatch(context.Background(), &tool.Invocation{CallID: "list", ToolName: tool.NamespacedName("skills", "list"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"executor"}}`}})
	if err != nil {
		t.Fatalf("skills.list error = %v", err)
	}
	var listed skillsListResponse
	if err := json.Unmarshal([]byte(listOutput.Body), &listed); err != nil {
		t.Fatalf("skills.list JSON = %v", err)
	}
	if len(listed.Skills) != 1 || listed.Skills[0].Authority.ID != "demo@1" || listed.Skills[0].Package != visible.PackageID || listed.NextCursor != nil {
		t.Fatalf("skills.list = %#v", listed)
	}
	resource := visible.PackageID + "/references/details.md"
	readOutput, err := router.Dispatch(context.Background(), &tool.Invocation{CallID: "read", ToolName: tool.NamespacedName("skills", "read"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"executor","id":"demo@1"},"package":"` + visible.PackageID + `","resource":"` + resource + `"}`}})
	if err != nil {
		t.Fatalf("skills.read error = %v", err)
	}
	var first skillsReadResponse
	if err := json.Unmarshal([]byte(readOutput.Body), &first); err != nil {
		t.Fatalf("skills.read JSON = %v", err)
	}
	if !strings.Contains(first.Contents, "MARKER") || first.NextCursor == nil || len(readOutput.Body) > maxSkillToolResponseBytes {
		t.Fatalf("first page = len(body)=%d response=%#v", len(readOutput.Body), first)
	}
	mainOutput, err := router.Dispatch(context.Background(), &tool.Invocation{CallID: "read-main", ToolName: tool.NamespacedName("skills", "read"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"executor","id":"demo@1"},"package":"` + visible.PackageID + `"}`}})
	if err != nil {
		t.Fatalf("skills.read without resource error = %v", err)
	}
	var mainRead skillsReadResponse
	if err := json.Unmarshal([]byte(mainOutput.Body), &mainRead); err != nil {
		t.Fatalf("skills.read without resource JSON = %v", err)
	}
	if mainRead.Resource != visible.MainResource {
		t.Fatalf("omitted-resource read = %#v, want main resource %q", mainRead, visible.MainResource)
	}
	secondOutput, err := router.Dispatch(context.Background(), &tool.Invocation{CallID: "read-2", ToolName: tool.NamespacedName("skills", "read"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"authority":{"kind":"executor","id":"demo@1"},"package":"` + visible.PackageID + `","resource":"` + resource + `","cursor":"` + *first.NextCursor + `"}`}})
	if err != nil {
		t.Fatalf("skills.read second page error = %v", err)
	}
	var second skillsReadResponse
	if err := json.Unmarshal([]byte(secondOutput.Body), &second); err != nil || len(second.Contents) == 0 {
		t.Fatalf("second page = %#v err=%v", second, err)
	}
	for name, arguments := range map[string]string{
		"authority substitution": `{"authority":{"kind":"executor","id":"other@1"},"package":"` + visible.PackageID + `","resource":"` + resource + `"}`,
		"path traversal":         `{"authority":{"kind":"executor","id":"demo@1"},"package":"` + visible.PackageID + `","resource":"` + visible.PackageID + `/../secret"}`,
		"unknown package":        `{"authority":{"kind":"executor","id":"demo@1"},"package":"skill://demo@1/plugin/skills/missing","resource":"skill://demo@1/plugin/skills/missing/SKILL.md"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := router.Dispatch(context.Background(), &tool.Invocation{CallID: name, ToolName: tool.NamespacedName("skills", "read"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: arguments}})
			var callErr *tool.FunctionCallError
			if !errors.As(err, &callErr) || !callErr.RespondsToModel() {
				t.Fatalf("error = %#v", err)
			}
		})
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
	shellSpec, ok := registry.Spec(tool.PlainName(tool.DefaultShellCommandToolName))
	if !ok || shellSpec.Exposure != tool.ExposureHidden {
		t.Fatalf("shell_command spec = %#v", shellSpec)
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.DefaultExecCommandToolName)); ok {
		t.Fatal("exec_command registered while unified_exec disabled")
	}
	shellRequired, _ := shellSpec.InputSchema["required"].([]string)
	if len(shellRequired) != 1 || shellRequired[0] != "command" {
		t.Fatalf("shell_command required = %#v", shellSpec.InputSchema["required"])
	}
	codeModeSpec, ok := registry.Spec(tool.PlainName(tool.CodeModeExecToolName))
	if !ok || !strings.Contains(codeModeSpec.Description, "tools.shell_command") {
		t.Fatalf("code-mode spec = %#v", codeModeSpec)
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
	legacySpec, ok := registry.Spec(tool.PlainName(tool.DefaultShellCommandToolName))
	if !ok || legacySpec.Exposure != tool.ExposureHidden {
		t.Fatalf("legacy shell_command spec = %#v", legacySpec)
	}
	codeModeSpec, ok = registry.Spec(tool.PlainName(tool.CodeModeExecToolName))
	if !ok || !strings.Contains(codeModeSpec.Description, "tools.exec_command") || strings.Contains(codeModeSpec.Description, "tools.shell_command") {
		t.Fatalf("unified code-mode spec = %#v", codeModeSpec)
	}
	visible := registry.ModelVisibleSpecs()
	for i := range visible {
		if visible[i].Name.Key() == tool.DefaultShellCommandToolName {
			t.Fatalf("legacy shell_command is model-visible with unified_exec: %#v", visible)
		}
	}
}

func TestBuildToolRegistryRestrictsLegacyShellCommandToSingleLocalEnvironmentLikeRust(t *testing.T) {
	for _, ids := range [][]string{{"remote"}, {"local", "remote"}, {"local", "local"}} {
		options := DefaultToolRegistryOptions(t.TempDir())
		options.EnableUnifiedExec = true
		options.SelectedEnvironmentIDs = ids
		registry, err := BuildToolRegistry(options)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := registry.Lookup(tool.PlainName(tool.DefaultShellCommandToolName)); ok {
			t.Fatalf("shell_command registered for environments %#v", ids)
		}
		if _, ok := registry.Lookup(tool.PlainName(tool.DefaultExecCommandToolName)); !ok {
			t.Fatalf("exec_command missing for environments %#v", ids)
		}
	}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableUnifiedExec = false
	options.SelectedEnvironmentIDs = []string{"remote"}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{tool.DefaultShellCommandToolName, tool.DefaultExecCommandToolName, tool.DefaultWriteStdinToolName} {
		if _, ok := registry.Lookup(tool.PlainName(name)); ok {
			t.Fatalf("%s registered for remote-only non-unified turn", name)
		}
	}
}

func TestBuildToolRegistryGatesAdditionalPermissionsSchemaLikeRust(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableUnifiedExec = true
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(default) error = %v", err)
	}
	execSpec, ok := registry.Spec(tool.PlainName(tool.DefaultExecCommandToolName))
	if !ok {
		t.Fatal("exec_command spec missing")
	}
	properties := execSpec.InputSchema["properties"].(map[string]any)
	if _, ok := properties["additional_permissions"]; ok {
		t.Fatalf("default exec_command schema exposes additional_permissions: %#v", properties)
	}

	options = DefaultToolRegistryOptions(t.TempDir())
	options.EnableUnifiedExec = true
	options.Shell.Validation.AdditionalPermissionsAllowed = true
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry(enabled) error = %v", err)
	}
	execSpec, ok = registry.Spec(tool.PlainName(tool.DefaultExecCommandToolName))
	if !ok {
		t.Fatal("exec_command spec missing with exec_permission_approvals")
	}
	properties = execSpec.InputSchema["properties"].(map[string]any)
	if _, ok := properties["additional_permissions"]; !ok {
		t.Fatalf("enabled exec_command schema lacks additional_permissions: %#v", properties)
	}
}

func TestBuildToolRegistryOmitsEmptyToolSearchLikeRust(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableAgents = false
	options.EnableMCP = false
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	if _, ok := registry.Lookup(tool.PlainName(tool.ToolSearchName)); ok {
		t.Fatal("tool_search registered without discoverable tools")
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

func TestBuildToolRegistryProtectsHostSyntheticToolsFromDynamicCollisions(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableAgents = false
	options.EnableCodeMode = true
	options.MCPTools = []mcp.RuntimeToolInfo{{
		ServerName: "drive",
		Tool:       mcp.RuntimeTool{Name: "create_doc", Description: "searchable MCP tool"},
	}}
	options.DynamicTools = []DynamicToolSpec{
		{Type: "function", Function: &DynamicToolFunctionSpec{Name: tool.ToolSearchName, Description: "client shadow", InputSchema: map[string]any{"type": "object"}}},
		{Type: "function", Function: &DynamicToolFunctionSpec{Name: tool.CodeModeExecToolName, Description: "exec shadow", InputSchema: map[string]any{"type": "object"}}},
		{Type: "function", Function: &DynamicToolFunctionSpec{Name: "wait", Description: "wait shadow", InputSchema: map[string]any{"type": "object"}}},
	}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	searchExecutor, ok := registry.Lookup(tool.PlainName(tool.ToolSearchName))
	if !ok {
		t.Fatal("host tool_search is missing")
	}
	if _, ok := searchExecutor.(*tool.ToolSearchHandler); !ok {
		t.Fatalf("tool_search runtime = %T, want host handler", searchExecutor)
	}
	execSpec, ok := registry.Spec(tool.PlainName(tool.CodeModeExecToolName))
	if !ok || execSpec.Freeform == nil || execSpec.Description == "exec shadow" {
		t.Fatalf("exec spec = %#v", execSpec)
	}
	waitSpec, ok := registry.Spec(tool.PlainName("wait"))
	if !ok || waitSpec.Description == "wait shadow" {
		t.Fatalf("wait spec = %#v", waitSpec)
	}
}

func TestBuildToolRegistryExternalCollisionKeepsMCPRuntime(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableAgents = false
	options.EnableCodeMode = false
	options.EnableToolSearch = false
	options.MCPTools = []mcp.RuntimeToolInfo{{
		ServerName: "drive",
		Tool:       mcp.RuntimeTool{Name: "create_doc", Description: "MCP winner"},
	}}
	options.DynamicTools = []DynamicToolSpec{{
		Type: "namespace",
		Namespace: &DynamicToolNamespaceSpec{Name: "mcp__drive", Tools: []DynamicToolFunctionSpec{{
			Name: "create_doc", Description: "dynamic shadow", InputSchema: map[string]any{"type": "object"},
		}}},
	}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := registry.Spec(tool.NamespacedName("mcp__drive", "create_doc"))
	if !ok || spec.Description != "MCP winner" {
		t.Fatalf("winning spec = %#v", spec)
	}
}

func TestBuildToolRegistryCodeModeUsesFirstNormalizedDynamicTool(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableAgents = false
	options.EnableMCP = false
	options.EnableToolSearch = false
	options.EnableCodeMode = true
	options.DynamicTools = []DynamicToolSpec{
		{Type: "function", Function: &DynamicToolFunctionSpec{Name: "foo-bar", Description: "first winner", InputSchema: map[string]any{"type": "object"}}},
		{Type: "function", Function: &DynamicToolFunctionSpec{Name: "foo_bar", Description: "shadowed tool", InputSchema: map[string]any{"type": "object"}}},
	}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	router := tool.NewRouter(registry)
	metadata := router.CodeModeToolNames()
	if len(metadata) != 1 || metadata["foo_bar"].Name != "foo-bar" {
		t.Fatalf("code mode metadata = %#v", metadata)
	}
	winners := router.CodeModeToolSpecs()
	if len(winners) != 1 || winners[0].Name.Key() != "foo-bar" {
		t.Fatalf("code mode winners = %#v", winners)
	}
	visible := augmentCodeModeWinnerSpecs(registry.ModelVisibleSpecs(), winners)
	byName := map[string]tool.Spec{}
	for _, spec := range visible {
		byName[spec.Name.Key()] = spec
	}
	if !strings.Contains(byName["foo-bar"].Description, "exec tool declaration") {
		t.Fatalf("winning description was not augmented: %q", byName["foo-bar"].Description)
	}
	if byName["foo_bar"].Description != "shadowed tool" {
		t.Fatalf("shadowed description = %q", byName["foo_bar"].Description)
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

func TestBuildToolRegistryRegistersMCPResourceToolsWithServersEvenWithToolSearch(t *testing.T) {
	newOptions := func(withServers bool) *ToolRegistryOptions {
		options := DefaultToolRegistryOptions(t.TempDir())
		options.EnableCore = false
		options.EnableShell = false
		options.EnableApplyPatch = false
		options.EnableAgents = false
		options.EnableToolSearch = true
		if withServers {
			service := mcp.NewMCPService(nil)
			service.SetServerConfig("docs", &mcp.ServerConfig{Enabled: true, Command: "docs"})
			options.MCPService = service
		}
		return options
	}
	resourceTools := []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"}

	t.Run("servers configured", func(t *testing.T) {
		registry, err := BuildToolRegistry(newOptions(true))
		if err != nil {
			t.Fatalf("BuildToolRegistry() error = %v", err)
		}
		visible := specKeySet(registry.ModelVisibleSpecs())
		for _, name := range resourceTools {
			if !visible[name] {
				t.Fatalf("model-visible specs = %#v, missing MCP resource tool %q", visible, name)
			}
		}
	})

	t.Run("no servers", func(t *testing.T) {
		registry, err := BuildToolRegistry(newOptions(false))
		if err != nil {
			t.Fatalf("BuildToolRegistry() error = %v", err)
		}
		visible := specKeySet(registry.ModelVisibleSpecs())
		for _, name := range resourceTools {
			if visible[name] {
				t.Fatalf("model-visible specs = %#v, unexpected MCP resource tool %q without servers", visible, name)
			}
		}
	})
}

func TestBuildToolRegistryAppliesPerServerOmitToolsFrom(t *testing.T) {
	newOptions := func(searchEnabled bool) *ToolRegistryOptions {
		options := DefaultToolRegistryOptions(t.TempDir())
		options.EnableCore = false
		options.EnableShell = false
		options.EnableApplyPatch = false
		options.EnableAgents = false
		options.EnableToolSearch = searchEnabled
		service := mcp.NewMCPService(nil)
		service.SetServerConfig("calendar", &mcp.ServerConfig{Enabled: true, OmitToolsFrom: []string{"code_mode"}})
		service.SetServerConfig("drive", &mcp.ServerConfig{Enabled: true, OmitToolsFrom: []string{"deferred", "code_mode"}})
		service.SetServerConfig("notes", &mcp.ServerConfig{Enabled: true, OmitToolsFrom: []string{"direct"}})
		options.MCPService = service
		options.MCPTools = []mcp.RuntimeToolInfo{
			{ServerName: "calendar", Tool: mcp.RuntimeTool{Name: "create_event", Description: "Calendar events"}},
			{ServerName: "drive", Tool: mcp.RuntimeTool{Name: "create_doc", Description: "Drive docs"}},
			{ServerName: "notes", Tool: mcp.RuntimeTool{Name: "list", Description: "Notes list"}},
		}
		return options
	}
	specByName := func(registry *tool.Registry, name string) (tool.Spec, bool) {
		return registry.Spec(tool.NamespacedName("mcp__"+name, "create_event"))
	}

	t.Run("tool search enabled", func(t *testing.T) {
		registry, err := BuildToolRegistry(newOptions(true))
		if err != nil {
			t.Fatalf("BuildToolRegistry() error = %v", err)
		}
		calendar, _ := specByName(registry, "calendar")
		drive, ok := registry.Spec(tool.NamespacedName("mcp__drive", "create_doc"))
		if !ok {
			t.Fatal("drive tool missing")
		}
		notes, ok := registry.Spec(tool.NamespacedName("mcp__notes", "list"))
		if !ok {
			t.Fatal("notes tool missing")
		}
		// calendar omits code_mode: deferred + code_mode -> discoverable but not
		// nested-callable nor model-visible.
		if calendar.Exposure != tool.ExposureDeferredModelOnly {
			t.Fatalf("calendar exposure = %q, want deferred_model_only", calendar.Exposure)
		}
		// drive omits deferred + code_mode: direct only.
		if drive.Exposure != tool.ExposureDirectModelOnly {
			t.Fatalf("drive exposure = %q, want direct_model_only", drive.Exposure)
		}
		// notes omits direct: discoverable + code_mode.
		if notes.Exposure != tool.ExposureDiscoverable {
			t.Fatalf("notes exposure = %q, want discoverable", notes.Exposure)
		}
		visible := specKeySet(registry.ModelVisibleSpecs())
		if !visible["mcp__drive.create_doc"] || visible["mcp__calendar.create_event"] || visible["mcp__notes.list"] {
			t.Fatalf("model-visible = %#v, want drive but not calendar/notes", visible)
		}
		if discoverable := registry.DiscoverableSpecs(); len(discoverable) != 2 {
			t.Fatalf("discoverable = %#v, want calendar and notes", specKeySet(discoverable))
		}
	})

	t.Run("tool search disabled", func(t *testing.T) {
		registry, err := BuildToolRegistry(newOptions(false))
		if err != nil {
			t.Fatalf("BuildToolRegistry() error = %v", err)
		}
		calendar, _ := specByName(registry, "calendar")
		drive, _ := registry.Spec(tool.NamespacedName("mcp__drive", "create_doc"))
		notes, _ := registry.Spec(tool.NamespacedName("mcp__notes", "list"))
		if calendar.Exposure != tool.ExposureDirectModelOnly {
			t.Fatalf("calendar exposure = %q, want direct_model_only", calendar.Exposure)
		}
		if drive.Exposure != tool.ExposureDirectModelOnly {
			t.Fatalf("drive exposure = %q, want direct_model_only", drive.Exposure)
		}
		if notes.Exposure != tool.ExposureCodeModeOnly {
			t.Fatalf("notes exposure = %q, want code_mode_only", notes.Exposure)
		}
		visible := specKeySet(registry.ModelVisibleSpecs())
		if !visible["mcp__calendar.create_event"] || !visible["mcp__drive.create_doc"] || visible["mcp__notes.list"] {
			t.Fatalf("model-visible = %#v, want calendar and drive but not notes", visible)
		}
		if discoverable := registry.DiscoverableSpecs(); len(discoverable) != 0 {
			t.Fatalf("discoverable = %#v, want none", specKeySet(discoverable))
		}
	})
}

func TestBuildToolRegistryOmitsDeferredSourcesFromSearchDescription(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableAgents = false
	options.OmitToolSearchSources = true
	options.MCPTools = []mcp.RuntimeToolInfo{{
		ServerName:           "drive",
		NamespaceDescription: "Drive tools",
		Tool:                 mcp.RuntimeTool{Name: "create_doc", Description: "Create Google Docs files"},
	}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	spec, ok := registry.Spec(tool.PlainName(tool.ToolSearchName))
	if !ok {
		t.Fatal("tool_search missing")
	}
	if strings.Contains(spec.Description, "following sources") || strings.Contains(spec.Description, "Drive tools") {
		t.Fatalf("tool_search description = %q", spec.Description)
	}
	if got := registry.DeferredToolNamespaces(); got["mcp__drive"] != "Drive tools" {
		t.Fatalf("deferred namespaces = %#v", got)
	}
}

func TestBuildToolRegistryBoundsMCPNamespaceDescriptionAndForwardsReadiness(t *testing.T) {
	expected := strings.Repeat("é", 499)
	full := expected + "🦀keep the complete MCP metadata"
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableAgents = false
	options.MCPService = mcp.NewMCPService(nil)
	options.MCPTools = []mcp.RuntimeToolInfo{{
		ServerName:           "drive",
		NamespaceDescription: full,
		Tool:                 mcp.RuntimeTool{Name: "create_doc", Description: "Create a document"},
	}}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	name := tool.NamespacedName("mcp__drive", "create_doc")
	spec, ok := registry.Spec(name)
	if !ok || spec.NamespaceDescription != expected || spec.Search == nil || spec.Search.Source == nil || spec.Search.Source.Description != expected {
		t.Fatalf("MCP spec = %#v", spec)
	}
	if full == spec.NamespaceDescription {
		t.Fatal("model-facing namespace description was not bounded")
	}
	if !tool.NewRouter(registry).HasReadinessWait(name) {
		t.Fatal("MCP readiness hook was not forwarded through the spec override")
	}
}

func TestBuildToolRegistryWaitForEnvironmentFeatureGateAndHostDescription(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCore = false
	options.EnableShell = false
	options.EnableApplyPatch = false
	options.EnableAgents = false
	options.EnableMCP = false
	options.EnableToolSearch = false
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Spec(tool.PlainName(tool.WaitForEnvironmentToolName)); ok {
		t.Fatal("wait_for_environment visible without feature gate")
	}

	options.EnableWaitForEnvironment = true
	options.SelectedEnvironmentIDs = []string{"env-1"}
	options.WaitForEnvironmentToolConfig = &tool.WaitForEnvironmentToolConfig{
		ToolDescription:          "Host wait description",
		EnvironmentIDDescription: "Host environment ID description",
	}
	registry, err = BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := registry.Spec(tool.PlainName(tool.WaitForEnvironmentToolName))
	if !ok || spec.Description != "Host wait description" {
		t.Fatalf("wait spec = %#v, ok=%v", spec, ok)
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

type mcpPermissionHookRunner struct {
	pre *tool.PreToolUseHookOutcome
}

func (h *mcpPermissionHookRunner) RunPreToolUse(ctx context.Context, invocation *tool.Invocation, payload *tool.PreToolUsePayload) (*tool.PreToolUseHookOutcome, error) {
	return h.pre, nil
}

func (h *mcpPermissionHookRunner) RunPostToolUse(ctx context.Context, invocation *tool.Invocation, payload *tool.PostToolUsePayload) (*tool.PostToolUseHookOutcome, error) {
	return nil, nil
}

// TestDispatchWithHooksPermissionHookResolvesMcpToolCallBeforeReviewLikeRust
// mirrors Rust #38108 hooks_mcp.rs: permission hooks resolve MCP tool calls
// before any user or guardian review, so an allow lets the call execute and a
// deny blocks it without surfacing an approval (elicitation) to a reviewer.
func TestDispatchWithHooksPermissionHookResolvesMcpToolCallBeforeReviewLikeRust(t *testing.T) {
	service := mcp.NewMCPService(nil)
	var elicitations atomic.Int32
	service.SetElicitationHandler(mcp.MCPElicitationHandlerFunc(func(ctx context.Context, request *mcp.MCPElicitationRequest) (*mcp.MCPElicitationResponse, error) {
		elicitations.Add(1)
		return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionDecline}, nil
	}))
	registry := tool.NewRegistry()
	if err := mcp.RegisterToolExecutors(registry, service, []mcp.RuntimeToolInfo{{
		ServerName: "memory",
		Tool:       mcp.RuntimeTool{Name: "create_entities"},
	}}); err != nil {
		t.Fatalf("RegisterToolExecutors() error = %v", err)
	}
	router := tool.NewRouter(registry)
	invocation := &tool.Invocation{
		CallID:   "call-mcp",
		ToolName: tool.NamespacedName("mcp__memory", "create_entities"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"name":"Ada"}`},
	}

	denied, err := router.DispatchWithHooks(context.Background(), invocation, &mcpPermissionHookRunner{pre: &tool.PreToolUseHookOutcome{
		Blocked:     true,
		BlockReason: "MCP tool access denied by the integration-test hook",
	}})
	if err == nil || !strings.Contains(err.Error(), "MCP tool access denied by the integration-test hook") {
		t.Fatalf("deny error = %v", err)
	}
	if denied != nil {
		t.Fatalf("deny output = %#v", denied)
	}
	if elicitations.Load() != 0 {
		t.Fatalf("deny should not reach user/guardian review: %d elicitations", elicitations.Load())
	}

	allowed, err := router.DispatchWithHooks(context.Background(), invocation, &mcpPermissionHookRunner{})
	if err != nil {
		t.Fatalf("allow error = %v", err)
	}
	if allowed == nil || !allowed.Success {
		t.Fatalf("allow output = %#v", allowed)
	}
	if elicitations.Load() != 0 {
		t.Fatalf("allow should not reach user/guardian review: %d elicitations", elicitations.Load())
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

func TestBuildToolRegistryRegistersTestSyncWhenModelDeclaresItLikeRust(t *testing.T) {
	// Mirrors Rust spec_plan.rs: test_sync_tool is registered only when the
	// model's experimental_supported_tools declares it.
	without := DefaultToolRegistryOptions(t.TempDir())
	registry, err := BuildToolRegistry(without)
	if err != nil {
		t.Fatalf("BuildToolRegistry: %v", err)
	}
	if _, ok := registry.Lookup(tool.PlainName("test_sync_tool")); ok {
		t.Fatal("test_sync_tool registered without model declaration")
	}

	with := DefaultToolRegistryOptions(t.TempDir())
	with.ExperimentalSupportedTools = []string{"test_sync_tool"}
	registry, err = BuildToolRegistry(with)
	if err != nil {
		t.Fatalf("BuildToolRegistry with declaration: %v", err)
	}
	executor, ok := registry.Lookup(tool.PlainName("test_sync_tool"))
	if !ok {
		t.Fatal("test_sync_tool not registered when model declares it")
	}
	spec := executor.Spec()
	if spec.Name.Key() != "test_sync_tool" {
		t.Fatalf("spec name = %q", spec.Name.Key())
	}
}
