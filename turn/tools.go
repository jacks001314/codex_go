package turn

import (
	"fmt"
	"strings"

	"codex_go/agent"
	"codex_go/compact"
	featureflags "codex_go/features"
	"codex_go/mcp"
	"codex_go/plugin"
	"codex_go/sandbox"
	"codex_go/tool"
)

type ToolRegistryOptions struct {
	PlanStore                      *tool.PlanStore
	ContextStatus                  func() compact.TokenStatus
	UserInputResponder             tool.UserInputResponder
	RequestUserInputAvailableModes []string
	DynamicToolCaller              DynamicToolCaller
	ClockProvider                  tool.ClockProvider

	Shell       *tool.ShellExecutorOptions
	ApplyPatch  *tool.ApplyPatchExecutorOptions
	UnifiedExec *tool.UnifiedExecManager

	MCPService                *mcp.MCPService
	MCPTools                  []mcp.RuntimeToolInfo
	MCPConnectors             []mcp.RuntimeConnector
	MCPExposure               tool.Exposure
	OrchestratorSkillsEnabled *bool

	AgentController agent.ToolController
	AgentExposure   tool.Exposure
	AgentRoles      map[string]agent.RoleConfig
	AgentDefaults   agent.SpawnDefaults

	PluginInstallCandidates            []plugin.DiscoverableInfo
	PluginInstallRecommendationContext bool
	PluginInstallRuntime               tool.PluginInstallRuntime
	PluginInstallAppServerClientName   string
	WebSearch                          *WebSearchOptions
	ImageGeneration                    *ImageGenerationOptions
	ViewImage                          *tool.ViewImageOptions

	EnableCore            bool
	EnableShell           bool
	EnableUnifiedExec     bool
	EnableCodeMode        bool
	EnableApplyPatch      bool
	EnableMCP             bool
	EnableAgents          bool
	EnableToolSearch      bool
	EnableCurrentTimeTool bool
	EnableSleepTool       bool
	DisableUpdatePlan     bool
	DisableWaitAgent      bool
	DynamicTools          []DynamicToolSpec
	ThreadID              string
	TurnID                string
}

func DefaultToolRegistryOptions(cwd string) *ToolRegistryOptions {
	return &ToolRegistryOptions{
		Shell: &tool.ShellExecutorOptions{
			Validation: tool.ShellValidationOptions{
				// Rust exposes with_additional_permissions only when the
				// exec_permission_approvals feature is enabled for this turn.
				AdditionalPermissionsAllowed: false,
				ApprovalPolicy:               sandbox.ApprovalOnRequest,
				CWD:                          cwd,
			},
		},
		ApplyPatch:        &tool.ApplyPatchExecutorOptions{CWD: cwd},
		UnifiedExec:       tool.NewUnifiedExecManager(),
		AgentController:   agent.NewMemoryToolController(),
		AgentExposure:     tool.ExposureDiscoverable,
		EnableCore:        true,
		EnableShell:       true,
		EnableUnifiedExec: featureflags.Enabled(nil, "unified_exec"),
		EnableCodeMode:    featureflags.Enabled(nil, "code_mode"),
		EnableApplyPatch:  true,
		EnableMCP:         true,
		EnableAgents:      true,
		EnableToolSearch:  true,
	}
}

func BuildToolRegistry(options *ToolRegistryOptions) (*tool.Registry, error) {
	if options == nil {
		options = DefaultToolRegistryOptions("")
	}
	registry := tool.NewRegistry()
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
		}); err != nil {
			return nil, err
		}
	}
	if options.EnableShell {
		if options.Shell != nil {
			options.Shell.UnifiedExecThreadID = options.ThreadID
			options.Shell.UnifiedExecTurnID = options.TurnID
			options.Shell.UnifiedExec = nil
			if options.EnableUnifiedExec {
				options.Shell.UnifiedExec = options.UnifiedExec
			}
		}
		shellExecutor := tool.NewShellExecutor(options.Shell)
		if options.EnableCodeMode {
			shellSpec := shellExecutor.Spec()
			shellSpec.Exposure = tool.ExposureHidden
			if err := registry.Register(tool.NewExecutorFunc(shellSpec, shellExecutor.Execute)); err != nil {
				return nil, err
			}
			execExecutor, waitExecutor := tool.NewCodeModeExecutors(registry)
			if err := registry.Register(execExecutor); err != nil {
				return nil, err
			}
			if err := registry.Register(waitExecutor); err != nil {
				return nil, err
			}
		} else if err := registry.Register(shellExecutor); err != nil {
			return nil, err
		}
		maxOutputTokens := (*int)(nil)
		if options.Shell != nil {
			maxOutputTokens = options.Shell.MaxOutputTokens
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
	if options.EnableMCP {
		if err := registerMCPTools(registry, options); err != nil {
			return nil, err
		}
		if !options.EnableToolSearch {
			if err := registerMCPResourceHandlers(registry, options.MCPService, options.ThreadID); err != nil {
				return nil, err
			}
		}
	}
	if err := registerSkillsTools(registry, options); err != nil {
		return nil, err
	}
	if options.EnableAgents {
		if err := agent.RegisterMultiAgentHandlersWithOptions(registry, &agent.MultiAgentHandlerOptions{
			Controller:       options.AgentController,
			Exposure:         options.AgentExposure,
			Roles:            options.AgentRoles,
			Defaults:         options.AgentDefaults,
			DisableWaitAgent: options.DisableWaitAgent,
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
	if options.WebSearch != nil {
		if err := registry.Register(NewWebSearchHandler(options.WebSearch)); err != nil {
			return nil, err
		}
	}
	if options.ImageGeneration != nil {
		if err := registry.Register(NewImageGenerationHandler(options.ImageGeneration)); err != nil {
			return nil, err
		}
	}
	if options.ViewImage != nil {
		if err := registry.Register(tool.NewViewImageHandler(*options.ViewImage)); err != nil {
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
		if len(registry.DiscoverableSpecs()) > 0 {
			if err := tool.RegisterToolSearchFromRegistry(registry); err != nil {
				return nil, err
			}
		}
	}
	return registry, nil
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
	if err := registerMCPToolSet(registry, options, exposure.DirectTools, tool.ExposureModelVisible); err != nil {
		return err
	}
	return registerMCPToolSet(registry, options, exposure.DeferredTools, tool.ExposureDiscoverable)
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
			},
			ToolName:    tool.NamespacedName(info.CallableNamespace, info.CallableName),
			ThreadID:    options.ThreadID,
			TurnID:      options.TurnID,
			RequestMeta: mcpRuntimeToolRequestMeta(&info),
		})
		spec := executor.Spec()
		spec.NamespaceDescription = strings.TrimSpace(info.NamespaceDescription)
		if exposure != "" && exposure != tool.ExposureModelVisible {
			spec.Exposure = exposure
		}
		if err := registry.Register(&specOverrideExecutor{Executor: executor, spec: spec}); err != nil {
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
