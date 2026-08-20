package turn

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex_go/agent"
	"codex_go/compact"
	featureflags "codex_go/features"
	"codex_go/mcp"
	"codex_go/plugin"
	"codex_go/sandbox"
	"codex_go/skillprovider"
	"codex_go/tool"
)

type ToolRegistryOptions struct {
	PlanStore                      *tool.PlanStore
	ContextStatus                  func() compact.TokenStatus
	UserInputResponder             tool.UserInputResponder
	RequestUserInputAvailableModes []string
	DynamicToolCaller              DynamicToolCaller
	ClockProvider                  tool.ClockProvider
	EnvironmentWaiter              tool.EnvironmentWaiter
	SelectedEnvironmentIDs         []string
	WaitForEnvironmentToolConfig   *tool.WaitForEnvironmentToolConfig

	Shell       *tool.ShellExecutorOptions
	ApplyPatch  *tool.ApplyPatchExecutorOptions
	UnifiedExec *tool.UnifiedExecManager

	MCPService                *mcp.MCPService
	MCPTools                  []mcp.RuntimeToolInfo
	MCPConnectors             []mcp.RuntimeConnector
	MCPExposure               tool.Exposure
	OrchestratorSkillsEnabled *bool
	SkillProviders            *skillprovider.Registry
	OpenAIFileRewriter        *mcp.OpenAIFileRewriter
	Model                     string

	AgentController                agent.ToolController
	AgentExposure                  tool.Exposure
	AgentVersion                   agent.MultiAgentVersion
	AgentNamespace                 string
	AgentUsageHintText             *string
	AgentWaitDefault               time.Duration
	AgentWaitMin                   time.Duration
	AgentWaitMax                   time.Duration
	AgentWaitConfigured            bool
	AgentHideSpawnMetadata         bool
	AgentExposeSpawnModelOverrides bool
	AgentRoles                     map[string]agent.RoleConfig
	AgentDefaults                  agent.SpawnDefaults

	PluginInstallCandidates            []plugin.DiscoverableInfo
	PluginInstallRecommendationContext bool
	PluginInstallRuntime               tool.PluginInstallRuntime
	PluginInstallAppServerClientName   string
	WebSearch                          *WebSearchOptions
	ImageGeneration                    *ImageGenerationOptions
	ViewImage                          *tool.ViewImageOptions

	EnableCore                   bool
	EnableShell                  bool
	EnableUnifiedExec            bool
	EnableCodeMode               bool
	CodeModeProvider             tool.CodeModeRemoteProvider
	CodeModeRuntime              *tool.CodeModeRuntime
	CodeModeDefaultExecYieldTime time.Duration
	DisableCodeModeFallback      bool
	EnableApplyPatch             bool
	EnableMCP                    bool
	EnableRequestPermissions     bool
	RequestPermissionsReviewer   tool.RequestPermissionsReviewer
	EnableAgents                 bool
	EnableToolSearch             bool
	OmitToolSearchSources        bool
	EnableCurrentTimeTool        bool
	EnableSleepTool              bool
	EnableWaitForEnvironment     bool
	DisableUpdatePlan            bool
	NewContextWindow             func()
	// SendUserMessageAsync, when set, emits an asynchronous user-visible
	// agent message for the send_user_message_async tool (#39319).
	SendUserMessageAsync func(message string)
	DisableWaitAgent             bool
	DynamicTools                 []DynamicToolSpec
	ThreadID                     string
	TurnID                       string
	SessionID                    string
	PluginMetricsResolver        func(command []string, cwd string) *plugin.ResolvedPluginMetricsOperation
	PluginMeasurementTracker     func(context.Context, plugin.PluginMeasurementBatch)
	ExtraTools                   []tool.Executor
	// ExperimentalSupportedTools mirrors Rust model_info.experimental_supported_tools:
	// tools the selected model declares as supported are registered
	// conditionally (e.g. test_sync_tool for testing models).
	ExperimentalSupportedTools []string
}

func DefaultToolRegistryOptions(cwd string) *ToolRegistryOptions {
	return &ToolRegistryOptions{
		Shell: &tool.ShellExecutorOptions{
			Validation: tool.ShellValidationOptions{
				// Rust exposes with_additional_permissions only when the
				// exec_permission_approvals feature is enabled for this turn.
				AdditionalPermissionsAllowed: false,
				ApprovalPolicy:               sandbox.ApprovalOnRequest,
				// Rust defaults allow_login_shell to true (per-environment
				// since b258c028fe); the turn runtime overrides this from the
				// effective config when available.
				AllowLoginShell: true,
				CWD:             cwd,
			},
		},
		ApplyPatch:                   &tool.ApplyPatchExecutorOptions{CWD: cwd},
		UnifiedExec:                  tool.NewUnifiedExecManager(),
		CodeModeDefaultExecYieldTime: tool.CodeModeDefaultExecYieldTime,
		AgentController:              agent.NewMemoryToolController(),
		AgentExposure:                tool.ExposureDiscoverable,
		EnableCore:                   true,
		EnableShell:                  true,
		EnableUnifiedExec:            featureflags.Enabled(nil, "unified_exec"),
		// Rust registers the code-mode exec/wait executors from the effective
		// tool mode, not the code_mode feature flag (finalize_tool_router in
		// codex-rs/core/src/tools/spec_plan.rs). Register them unconditionally
		// here; Runtime.Run filters the model-visible surface per turn based on
		// the effective tool mode, so direct-mode turns still see shell tools.
		EnableCodeMode:   true,
		EnableApplyPatch: true,
		EnableMCP:        true,
		EnableAgents:     true,
		EnableToolSearch: true,
	}
}

func BuildToolRegistry(options *ToolRegistryOptions) (*tool.Registry, error) {
	if options == nil {
		options = DefaultToolRegistryOptions("")
	}
	registry := tool.NewRegistry()
	var codeModeCommandTool tool.ToolName
	if options.EnableCore {
		if err := tool.RegisterCoreHandlersWithOptions(registry, &tool.CoreHandlerOptions{
			PlanStore:                      options.PlanStore,
			ContextStatus:                  options.ContextStatus,
			UserInputResponder:             options.UserInputResponder,
			RequestUserInputAvailableModes: options.RequestUserInputAvailableModes,
			ClockProvider:                  options.ClockProvider,
			ThreadID:                       options.ThreadID,
			EnableCurrentTime:              options.EnableCurrentTimeTool,
			EnableClockSleep:               options.EnableSleepTool,
			DisableUpdatePlan:              options.DisableUpdatePlan,
			NewContextWindow:               options.NewContextWindow,
		}); err != nil {
			return nil, err
		}
		for _, supported := range options.ExperimentalSupportedTools {
			if supported == "test_sync_tool" {
				if err := registry.Register(&tool.TestSyncHandler{}); err != nil {
					return nil, err
				}
			}
			if supported == "send_user_message_async" {
				if err := registry.Register(&tool.SendUserMessageAsyncHandler{EmitAsyncMessage: options.SendUserMessageAsync}); err != nil {
					return nil, err
				}
			}
		}
	}
	if options.EnableShell {
		supportsShellCommand := SupportsLegacyShellCommand(options.SelectedEnvironmentIDs)
		shellOptions := options.Shell
		if shellOptions == nil {
			shellOptions = &tool.ShellExecutorOptions{}
		}
		shellOptions.ToolName = tool.PlainName(tool.DefaultShellCommandToolName)
		shellOptions.UnifiedExecThreadID = options.ThreadID
		shellOptions.UnifiedExecTurnID = options.TurnID
		shellOptions.SessionID = options.SessionID
		shellOptions.PluginMetricsResolver = options.PluginMetricsResolver
		shellOptions.PluginMeasurementTracker = options.PluginMeasurementTracker
		shellOptions.UnifiedExec = nil
		if options.EnableUnifiedExec {
			shellOptions.ToolName = tool.PlainName(tool.DefaultExecCommandToolName)
			shellOptions.UnifiedExec = options.UnifiedExec
		}
		if options.EnableUnifiedExec || supportsShellCommand {
			shellExecutor := tool.NewShellExecutor(shellOptions)
			if options.EnableCodeMode {
				shellSpec := shellExecutor.Spec()
				shellSpec.Exposure = tool.ExposureHidden
				if err := registry.Register(tool.NewExecutorFunc(shellSpec, shellExecutor.Execute)); err != nil {
					return nil, err
				}
				codeModeCommandTool = shellOptions.ToolName
			} else if err := registry.Register(shellExecutor); err != nil {
				return nil, err
			}
		}
		if options.EnableUnifiedExec && supportsShellCommand {
			legacyOptions := *shellOptions
			legacyOptions.ToolName = tool.PlainName(tool.DefaultShellCommandToolName)
			legacyOptions.UnifiedExec = nil
			legacyExecutor := tool.NewShellExecutor(&legacyOptions)
			legacySpec := legacyExecutor.Spec()
			legacySpec.Exposure = tool.ExposureHidden
			if err := registry.Register(tool.NewExecutorFunc(legacySpec, legacyExecutor.Execute)); err != nil {
				return nil, err
			}
		}
		maxOutputTokens := (*int)(nil)
		if shellOptions != nil {
			maxOutputTokens = shellOptions.MaxOutputTokens
		}
		if options.EnableUnifiedExec {
			if err := tool.RegisterWriteStdinHandler(registry, options.UnifiedExec, maxOutputTokens); err != nil {
				return nil, err
			}
		}
	}
	if options.EnableApplyPatch {
		if err := tool.RegisterApplyPatchHandler(registry, options.ApplyPatch); err != nil {
			return nil, err
		}
	}
	if err := registerSkillsTools(registry, options); err != nil {
		return nil, err
	}
	if options.EnableAgents {
		if err := agent.RegisterMultiAgentHandlersWithOptions(registry, &agent.MultiAgentHandlerOptions{
			Controller:                options.AgentController,
			Exposure:                  options.AgentExposure,
			Version:                   options.AgentVersion,
			Namespace:                 options.AgentNamespace,
			UsageHintText:             options.AgentUsageHintText,
			WaitDefault:               options.AgentWaitDefault,
			WaitMin:                   options.AgentWaitMin,
			WaitMax:                   options.AgentWaitMax,
			WaitConfigured:            options.AgentWaitConfigured,
			HideSpawnMetadata:         options.AgentHideSpawnMetadata,
			ExposeSpawnModelOverrides: options.AgentExposeSpawnModelOverrides,
			Roles:                     options.AgentRoles,
			Defaults:                  options.AgentDefaults,
			DisableWaitAgent:          options.DisableWaitAgent,
		}); err != nil {
			return nil, err
		}
	}
	if len(options.PluginInstallCandidates) > 0 {
		if err := tool.RegisterPluginInstallSuggestionHandlers(registry, &tool.PluginInstallSuggestionOptions{
			Candidates:            options.PluginInstallCandidates,
			RecommendationContext: options.PluginInstallRecommendationContext,
			Runtime:               options.PluginInstallRuntime,
			AppServerClientName:   options.PluginInstallAppServerClientName,
		}); err != nil {
			return nil, err
		}
	}
	for _, executor := range options.ExtraTools {
		if executor == nil {
			continue
		}
		if err := registry.Register(executor); err != nil {
			return nil, err
		}
	}
	if options.ViewImage != nil {
		if err := registry.Register(tool.NewViewImageHandler(*options.ViewImage)); err != nil {
			return nil, err
		}
	}
	if options.EnableWaitForEnvironment {
		if err := registry.Register(tool.NewWaitForEnvironmentHandler(options.EnvironmentWaiter, options.SelectedEnvironmentIDs, options.WaitForEnvironmentToolConfig)); err != nil {
			return nil, err
		}
	}

	// Runtime-provided tools are external input. Keep the first registered
	// runtime for a name so they cannot replace host-owned tools or abort a turn.
	if options.EnableMCP {
		if err := registerMCPTools(registry, options); err != nil {
			return nil, err
		}
		// Rust registers the MCP resource tools whenever MCP servers are
		// configured, independently of tool search (add_mcp_resource_tools in
		// codex-rs/core/src/tools/spec_plan.rs). Gating them on tool search
		// being disabled left them hidden behind deferred discovery, so models
		// could not list or read MCP resources even though the servers were
		// configured and their other tools were searchable.
		if options.MCPService.HasServers() {
			if err := registerMCPResourceHandlers(registry, options.MCPService, options.ThreadID); err != nil {
				return nil, err
			}
		}
	}
	if options.WebSearch != nil {
		if _, err := registry.RegisterExternal(NewWebSearchHandler(options.WebSearch)); err != nil {
			return nil, err
		}
	}
	if options.EnableRequestPermissions && options.RequestPermissionsReviewer != nil {
		if err := tool.RegisterRequestPermissionsTool(registry, options.RequestPermissionsReviewer); err != nil {
			return nil, err
		}
	}
	if options.ImageGeneration != nil {
		if _, err := registry.RegisterExternal(NewImageGenerationHandler(options.ImageGeneration)); err != nil {
			return nil, err
		}
	}
	if err := RegisterDynamicToolHandlers(registry, &DynamicToolRegistryOptions{
		Caller:    options.DynamicToolCaller,
		ThreadID:  options.ThreadID,
		TurnID:    options.TurnID,
		Tools:     options.DynamicTools,
		Now:       nil,
		EnableAll: true,
	}); err != nil {
		return nil, err
	}
	if options.EnableToolSearch {
		// Rust only adds tool_search when at least one deferred executor is
		// searchable. Advertising an empty search tool causes models to emit a
		// useless tool_search_call/output pair which is then persisted into
		// resumed Responses history.
		if registryHasSearchableDeferredTool(registry) {
			registry.Remove(tool.PlainName(tool.ToolSearchName))
			if err := tool.RegisterToolSearchFromRegistryWithOptions(registry, options.OmitToolSearchSources); err != nil {
				return nil, err
			}
		}
	}
	if options.EnableCodeMode {
		registry.Remove(tool.PlainName(tool.CodeModeExecToolName))
		registry.Remove(tool.PlainName("wait"))
		var execExecutor, waitExecutor tool.Executor
		if options.CodeModeRuntime != nil {
			options.CodeModeRuntime.SetDefaultExecYieldTime(options.CodeModeDefaultExecYieldTime)
			execExecutor, waitExecutor = options.CodeModeRuntime.Executors(registry, codeModeCommandTool)
		} else {
			runtime := tool.NewCodeModeRuntime(options.CodeModeProvider, options.DisableCodeModeFallback)
			runtime.SetDefaultExecYieldTime(options.CodeModeDefaultExecYieldTime)
			execExecutor, waitExecutor = runtime.Executors(registry, codeModeCommandTool)
		}
		if err := registry.Prepend(waitExecutor); err != nil {
			return nil, err
		}
		if err := registry.Prepend(execExecutor); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// SupportsLegacyShellCommand matches Rust's single-local-environment gate.
// An omitted selection denotes the implicit local environment.
func SupportsLegacyShellCommand(environmentIDs []string) bool {
	if len(environmentIDs) == 0 {
		return true
	}
	return len(environmentIDs) == 1 && strings.EqualFold(strings.TrimSpace(environmentIDs[0]), "local")
}

func registryHasSearchableDeferredTool(registry *tool.Registry) bool {
	if registry == nil {
		return false
	}
	for _, spec := range registry.DiscoverableSpecs() {
		if spec.Name.Key() != tool.ToolSearchName && spec.Search != nil {
			return true
		}
	}
	return false
}

func BuildToolRouter(options *ToolRegistryOptions) (*tool.Router, error) {
	registry, err := BuildToolRegistry(options)
	if err != nil {
		return nil, err
	}
	return tool.NewRouter(registry), nil
}

func registerMCPTools(registry *tool.Registry, options *ToolRegistryOptions) error {
	if len(options.MCPTools) == 0 {
		return nil
	}
	if options.MCPExposure != "" {
		return registerMCPToolSet(registry, options, options.MCPTools, options.MCPExposure)
	}
	exposure := mcp.BuildRuntimeExposure(options.MCPTools, options.MCPConnectors, options.EnableToolSearch)
	// BuildRuntimeExposure returns either the direct or the deferred tool set
	// depending on tool search, never both. Union them and apply Rust's
	// per-server omit_tools_from policy (51c9ed6d4f) to each tool individually
	// so a server can opt out of any model-facing surface without disabling its
	// tools everywhere.
	allTools := append([]mcp.RuntimeToolInfo(nil), exposure.DirectTools...)
	allTools = append(allTools, exposure.DeferredTools...)
	omitByServer := mcpOmitToolsFromByServer(options.MCPService, allTools)
	for i := range allTools {
		info := allTools[i]
		exposure := mcpToolExposureForSurfaces(omitByServer[info.ServerName], options.EnableToolSearch)
		if err := registerMCPToolSet(registry, options, []mcp.RuntimeToolInfo{info}, exposure); err != nil {
			return err
		}
	}
	return nil
}

// mcpToolExposureForSurfaces maps a server's omit_tools_from surfaces to the
// final tool exposure using Rust's apply_mcp_tool_exposure_policy table:
// (direct, deferred, code_mode) -> ToolExposure. Go has no
// direct_only_tool_namespaces configuration, so that restriction is skipped.
func mcpToolExposureForSurfaces(omit []string, searchToolEnabled bool) tool.Exposure {
	allowed := map[string]bool{"direct": true, "deferred": true, "code_mode": true}
	for _, surface := range omit {
		switch strings.ToLower(strings.TrimSpace(surface)) {
		case "direct", "deferred", "code_mode":
			allowed[strings.ToLower(strings.TrimSpace(surface))] = false
		}
	}
	if searchToolEnabled && allowed["deferred"] {
		allowed["direct"] = false
	} else {
		allowed["deferred"] = false
	}
	direct, deferred, codeMode := allowed["direct"], allowed["deferred"], allowed["code_mode"]
	switch {
	case !direct && !deferred && !codeMode:
		return tool.ExposureHidden
	case !direct && !deferred && codeMode:
		return tool.ExposureCodeModeOnly
	case direct && !deferred && !codeMode:
		return tool.ExposureDirectModelOnly
	case direct && !deferred && codeMode:
		return tool.ExposureModelVisible
	case !direct && deferred && !codeMode:
		return tool.ExposureDeferredModelOnly
	default:
		return tool.ExposureDiscoverable
	}
}

func mcpOmitToolsFromByServer(service *mcp.MCPService, tools []mcp.RuntimeToolInfo) map[string][]string {
	omitByServer := map[string][]string{}
	if service == nil {
		return omitByServer
	}
	for i := range tools {
		serverName := strings.TrimSpace(tools[i].ServerName)
		if serverName == "" {
			continue
		}
		if _, seen := omitByServer[serverName]; seen {
			continue
		}
		config, ok := service.ServerConfigForServer(serverName)
		if !ok {
			omitByServer[serverName] = nil
			continue
		}
		omitByServer[serverName] = append([]string(nil), config.OmitToolsFrom...)
	}
	return omitByServer
}

func registerMCPToolSet(registry *tool.Registry, options *ToolRegistryOptions, tools []mcp.RuntimeToolInfo, exposure tool.Exposure) error {
	tools = mcp.NormalizeRuntimeToolsForModel(tools)
	for i := range tools {
		info := tools[i]
		executor := mcp.NewToolExecutor(&mcp.ToolExecutorOptions{
			Service:    options.MCPService,
			ServerName: info.ServerName,
			ToolInfo: &mcp.MCPToolInfo{
				Name:        info.Tool.Name,
				Title:       info.Tool.Title,
				Description: info.Tool.Description,
				InputSchema: info.Tool.InputSchema,
				Annotations: info.Tool.Annotations,
				Meta:        info.Meta,
			},
			ToolName:                      tool.NamespacedName(info.CallableNamespace, info.CallableName),
			ThreadID:                      options.ThreadID,
			TurnID:                        options.TurnID,
			RequestMeta:                   mcpRuntimeToolRequestMeta(&info),
			OpenAIFileRewriter:            options.OpenAIFileRewriter,
			OpenAIFileInputOptionalFields: info.OpenAIFileInputOptionalFields,
			AgentPlugin:                   info.AgentPlugin,
			ConnectorID:                   info.ConnectorID,
			Model:                         options.Model,
		})
		spec := executor.Spec()
		spec.NamespaceDescription = mcp.BoundedMCPNamespaceDescription(info.NamespaceDescription)
		if spec.Search != nil && spec.Search.Source != nil {
			spec.Search.Source.Description = spec.NamespaceDescription
		}
		if exposure != "" && exposure != tool.ExposureModelVisible {
			spec.Exposure = exposure
		}
		if _, err := registry.RegisterExternal(&specOverrideExecutor{Executor: executor, spec: spec}); err != nil {
			return err
		}
	}
	return nil
}

func mcpRuntimeToolRequestMeta(info *mcp.RuntimeToolInfo) map[string]any {
	if info == nil || !mcp.IsCodexAppsMCPServerName(info.ServerName) {
		return nil
	}
	apps := map[string]any{}
	if connectorID := strings.TrimSpace(info.ConnectorID); connectorID != "" {
		apps["connector_id"] = connectorID
	}
	return map[string]any{"_codex_apps": apps}
}

type specOverrideExecutor struct {
	tool.Executor
	spec tool.Spec
}

func (e *specOverrideExecutor) Spec() tool.Spec {
	if e == nil {
		return tool.Spec{}
	}
	return e.spec
}

func (e *specOverrideExecutor) WaitUntilReady(ctx context.Context, invocation *tool.Invocation) error {
	if e == nil {
		return nil
	}
	waiter, ok := e.Executor.(tool.ReadinessWaiter)
	if !ok {
		return nil
	}
	return waiter.WaitUntilReady(ctx, invocation)
}

func (e *specOverrideExecutor) PreToolUsePayload(invocation *tool.Invocation) (*tool.PreToolUsePayload, bool) {
	if e == nil {
		return nil, false
	}
	provider, ok := e.Executor.(tool.PreToolUsePayloadProvider)
	if !ok {
		return nil, false
	}
	return provider.PreToolUsePayload(invocation)
}

func (e *specOverrideExecutor) PostToolUsePayload(invocation *tool.Invocation, output *tool.Output) (*tool.PostToolUsePayload, bool) {
	if e == nil {
		return nil, false
	}
	provider, ok := e.Executor.(tool.PostToolUsePayloadProvider)
	if !ok {
		return nil, false
	}
	return provider.PostToolUsePayload(invocation, output)
}

func (e *specOverrideExecutor) WithUpdatedHookInput(invocation *tool.Invocation, updatedInput any) (*tool.Invocation, error) {
	if e == nil {
		return nil, tool.RespondToModel("hook input rewrite received unsupported payload")
	}
	updater, ok := e.Executor.(tool.HookInputUpdater)
	if !ok {
		return nil, tool.RespondToModel("hook input rewrite received unsupported payload")
	}
	return updater.WithUpdatedHookInput(invocation, updatedInput)
}

func EnsureRegistry(options *ToolRegistryOptions) (*tool.Registry, error) {
	registry, err := BuildToolRegistry(options)
	if err != nil {
		return nil, fmt.Errorf("build tool registry: %w", err)
	}
	return registry, nil
}
