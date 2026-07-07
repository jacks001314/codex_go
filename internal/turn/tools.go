package turn

import (
	"fmt"

	"codex_go/internal/agent"
	"codex_go/internal/compact"
	"codex_go/internal/mcp"
	"codex_go/internal/plugin"
	"codex_go/internal/sandbox"
	"codex_go/internal/tool"
)

type ToolRegistryOptions struct {
	PlanStore          *tool.PlanStore
	ContextStatus      func() compact.TokenStatus
	UserInputResponder tool.UserInputResponder
	DynamicToolCaller  DynamicToolCaller
	ClockProvider      tool.ClockProvider

	Shell      *tool.ShellExecutorOptions
	ApplyPatch *tool.ApplyPatchExecutorOptions

	MCPService    *mcp.MCPService
	MCPTools      []mcp.RuntimeToolInfo
	MCPConnectors []mcp.RuntimeConnector
	MCPExposure   tool.Exposure

	AgentController agent.ToolController
	AgentExposure   tool.Exposure

	PluginInstallCandidates            []plugin.DiscoverableInfo
	PluginInstallRecommendationContext bool
	PluginInstallRuntime               tool.PluginInstallRuntime
	PluginInstallAppServerClientName   string

	EnableCore            bool
	EnableShell           bool
	EnableApplyPatch      bool
	EnableMCP             bool
	EnableAgents          bool
	EnableToolSearch      bool
	EnableCurrentTimeTool bool
	EnableSleepTool       bool
	DynamicTools          []DynamicToolSpec
	ThreadID              string
	TurnID                string
}

func DefaultToolRegistryOptions(cwd string) *ToolRegistryOptions {
	return &ToolRegistryOptions{
		Shell: &tool.ShellExecutorOptions{
			Validation: tool.ShellValidationOptions{
				AdditionalPermissionsAllowed: true,
				ApprovalPolicy:               sandbox.ApprovalOnRequest,
				CWD:                          cwd,
			},
		},
		ApplyPatch:       &tool.ApplyPatchExecutorOptions{CWD: cwd},
		AgentController:  agent.NewMemoryToolController(),
		AgentExposure:    tool.ExposureDiscoverable,
		EnableCore:       true,
		EnableShell:      true,
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
	if options.EnableCore {
		if err := tool.RegisterCoreHandlersWithOptions(registry, &tool.CoreHandlerOptions{
			PlanStore:          options.PlanStore,
			ContextStatus:      options.ContextStatus,
			UserInputResponder: options.UserInputResponder,
			ClockProvider:      options.ClockProvider,
			ThreadID:           options.ThreadID,
			EnableCurrentTime:  options.EnableCurrentTimeTool,
			EnableClockSleep:   options.EnableSleepTool,
		}); err != nil {
			return nil, err
		}
	}
	if options.EnableShell {
		if err := tool.RegisterShellHandler(registry, options.Shell); err != nil {
			return nil, err
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
	}
	if options.EnableAgents {
		if err := agent.RegisterMultiAgentHandlers(registry, options.AgentController, options.AgentExposure); err != nil {
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
		if err := tool.RegisterToolSearchFromRegistry(registry); err != nil {
			return nil, err
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
			ToolName: tool.NamespacedName(info.ServerName, info.Tool.Name),
			ThreadID: options.ThreadID,
			TurnID:   options.TurnID,
		})
		spec := executor.Spec()
		if exposure != "" && exposure != tool.ExposureModelVisible {
			spec.Exposure = exposure
		}
		if err := registry.Register(&specOverrideExecutor{Executor: executor, spec: spec}); err != nil {
			return err
		}
	}
	return nil
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
