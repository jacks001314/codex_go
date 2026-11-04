package appserver

import (
	"context"
	"strings"

	"codex_go/config"
	"codex_go/features"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/turn"
)

// rememberedTurnToolInputs keeps the non-model half of a turn's tool plan.
// Rust #44242's `built_tools` plans every sampling step from that step's
// captured model while the rest of the inputs (MCP tools, sandbox, skills,
// environments) stay fixed for the turn, so Go keeps the options assembled at
// turn start and re-derives only the model-dependent fields per step.
type rememberedTurnToolInputs struct {
	turnID  string
	model   string
	cwd     string
	params  turn.TurnStartParams
	cfg     *config.Config
	options *turn.ToolRegistryOptions
	// The raw MCP catalog and binding are retained so a later step can apply
	// that step model's search provider without losing the original declaration.
	mcpTools        []mcp.RuntimeToolInfo
	mcpBinding      *mcp.Binding
	turnMetadata    map[string]any
	searchSelection modelSearchSelection
}

// rememberTurnToolOptions records the tool options a turn assembled so a later
// sampling step can be planned from its own model. The newest turn of a thread
// replaces the previous one, which keeps the store bounded by the live threads
// without hooking the turn lifecycle.
func (r *RuntimeRouter) rememberTurnToolOptions(threadID string, turnID string, cwd string, params *turn.TurnStartParams, cfg *config.Config, options *turn.ToolRegistryOptions) {
	if r == nil || params == nil || options == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return
	}
	inputs := rememberedTurnToolInputs{
		turnID:  turnID,
		model:   strings.TrimSpace(params.Model),
		cwd:     strings.TrimSpace(cwd),
		params:  *params,
		cfg:     cfg,
		options: options,
	}
	r.stepToolPlansMu.Lock()
	if r.stepToolPlans == nil {
		r.stepToolPlans = make(map[string]rememberedTurnToolInputs)
	}
	r.stepToolPlans[threadID] = inputs
	r.stepToolPlansMu.Unlock()
}

// stepToolPlanForTurn returns the per-step plan builder for a turn. A step that
// keeps the turn's model has nothing to rebuild, so it keeps the turn-start
// plan; a step that switches models is planned from that step's own settings.
func (r *RuntimeRouter) stepToolPlanForTurn(threadID string, turnID string) func(string) (*turn.StepToolPlan, error) {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return nil
	}
	return func(modelID string) (*turn.StepToolPlan, error) {
		modelID = strings.TrimSpace(modelID)
		inputs, ok := r.rememberedTurnToolInputs(threadID, turnID)
		if !ok || modelID == "" {
			return nil, nil
		}
		if modelID == inputs.model {
			// Switching back to the turn's own model restores the plan the
			// turn started with instead of keeping the previous step's.
			return r.restoreTurnToolPlan(inputs)
		}
		return r.buildStepToolPlan(inputs, modelID)
	}
}

func (r *RuntimeRouter) rememberModelSearchToolInputs(threadID string, turnID string, tools []mcp.RuntimeToolInfo, binding *mcp.Binding, metadata map[string]any, selection modelSearchSelection) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return
	}
	r.stepToolPlansMu.Lock()
	if inputs, ok := r.stepToolPlans[threadID]; ok && inputs.turnID == turnID {
		inputs.mcpTools = append([]mcp.RuntimeToolInfo(nil), tools...)
		inputs.mcpBinding = binding
		inputs.turnMetadata = cloneSearchObject(metadata)
		inputs.searchSelection = selection
		r.stepToolPlans[threadID] = inputs
	}
	r.stepToolPlansMu.Unlock()
}

func (r *RuntimeRouter) rememberedTurnToolInputs(threadID string, turnID string) (rememberedTurnToolInputs, bool) {
	r.stepToolPlansMu.Lock()
	defer r.stepToolPlansMu.Unlock()
	inputs, ok := r.stepToolPlans[threadID]
	if !ok || inputs.turnID != turnID {
		return rememberedTurnToolInputs{}, false
	}
	return inputs, true
}

// restoreTurnToolPlan rebuilds the plan the turn started with, which a step
// that switches back to the turn's model must use (Rust's `built_tools` plans
// every step from that step's model, so returning to the turn's model restores
// its tools rather than keeping the previous step's).
func (r *RuntimeRouter) restoreTurnToolPlan(inputs rememberedTurnToolInputs) (*turn.StepToolPlan, error) {
	r.threads.UpdateTurn(strings.TrimSpace(inputs.params.ThreadID), strings.TrimSpace(inputs.turnID), func(active *activeRuntimeTurn) {
		active.ModelSearchProvider = inputs.searchSelection.provider
		active.ModelSearchMode = inputs.searchSelection.mode
		active.ModelSearchFallback = inputs.searchSelection.fallback
	})
	options := *inputs.options
	router, err := turn.BuildToolRouter(&options)
	if err != nil {
		return nil, err
	}
	hostedTools, err := r.hostedToolsForTurn(&inputs.params)
	if err != nil {
		return nil, err
	}
	return &turn.StepToolPlan{Router: router, HostedTools: hostedTools, ReplaceHostedTools: true}, nil
}

// buildStepToolPlan rebuilds a sampling step's complete tool plan from the
// step's captured model (Rust `built_tools`).
func (r *RuntimeRouter) buildStepToolPlan(inputs rememberedTurnToolInputs, modelID string) (*turn.StepToolPlan, error) {
	options, hostedTools, err := r.stepToolRegistryOptions(inputs, modelID)
	if err != nil {
		return nil, err
	}
	router, err := turn.BuildToolRouter(options)
	if err != nil {
		return nil, err
	}
	return &turn.StepToolPlan{Router: router, HostedTools: hostedTools, ReplaceHostedTools: true}, nil
}

// stepToolRegistryOptions re-derives the model-dependent half of the turn's
// tool options for a later step: the model catalog decides the tool mode,
// apply_patch, the clock and sleep tools, the catalog-declared and
// catalog-described tools, and whether standalone web search or image
// generation stay registered. Everything the turn resolved before sampling
// (MCP tools, sandbox, skills, environments) is reused unchanged.
func (r *RuntimeRouter) stepToolRegistryOptions(inputs rememberedTurnToolInputs, modelID string) (*turn.ToolRegistryOptions, []any, error) {
	options := *inputs.options
	info := r.modelInfoForRuntimeWithConfigAndOriginator(modelID, inputs.cfg, inputs.params.Originator)
	featureSettings := map[string]bool(nil)
	if inputs.cfg != nil {
		featureSettings = inputs.cfg.FeatureSettings()
	}
	options.Model = modelID
	if info != nil {
		options.ToolMode = model.ResolveToolMode(info.ToolMode, featureSettings)
		// Rust model_info.apply_patch_tool_type decides whether the step's
		// model is offered apply_patch at all.
		options.EnableApplyPatch = strings.TrimSpace(info.ApplyPatchToolType) != ""
	}
	// Rust #41243: the clock and sleep tools follow the step model's declared
	// capabilities, and the current-time reminder decides the clock provider.
	if inputs.cfg != nil {
		modelHasClock := false
		if info != nil {
			for _, supported := range info.ExperimentalSupportedTools {
				if supported == "clock" {
					modelHasClock = true
					break
				}
			}
		}
		options.EnableCurrentTimeTool, options.EnableSleepTool = decideClockToolEnablement(inputs.cfg, &inputs.params, modelHasClock)
		options.ClockProvider = nil
		if options.EnableCurrentTimeTool {
			if reminder := inputs.cfg.CurrentTimeReminder(); reminder != nil && reminder.ClockSource == config.CurrentTimeSourceExternal {
				options.ClockProvider = &appServerClockProvider{router: r}
			}
		}
	}
	// The model catalog supplies the tool schemas, guidance and the tools a
	// model declares as supported; a step model sees only its own entries.
	options.ExperimentalSupportedTools = nil
	options.ModelConfirmationPolicies = nil
	options.FunctionDescriptionPrefixes = nil
	options.IndirectDescriptionPrefixes = nil
	options.SendUserMessageAsyncDescription = nil
	options.RequestUserInputAsyncDescription = nil
	options.RequestUserInputAsyncParameters = nil
	options.MCPResourceMessages = nil
	options.CodeModeMessages = nil
	options.ContextManagementMessages = nil
	if info != nil {
		options.ExperimentalSupportedTools = append(options.ExperimentalSupportedTools, info.ExperimentalSupportedTools...)
		if messages := info.ModelMessages; messages != nil {
			options.ModelConfirmationPolicies = messages.ConfirmationPolicies
			if modelTools := messages.Tools; modelTools != nil {
				if modelTools.SendUserMessageAsync != nil {
					options.SendUserMessageAsyncDescription = modelTools.SendUserMessageAsync.Description
				}
				options.FunctionDescriptionPrefixes = modelTools.FunctionsNamespaceFunctionsDescriptionPrefixes
				options.IndirectDescriptionPrefixes = indirectDescriptionPrefixesFromModel(modelTools.IndirectDescriptionPrefixes)
			}
			options.RequestUserInputAsyncDescription = messages.RequestUserInputAsyncDescription()
			options.RequestUserInputAsyncParameters = messages.RequestUserInputAsyncParametersOverride()
			options.MCPResourceMessages = messages.MCPResources()
			options.CodeModeMessages = messages.CodeMode()
			options.ContextManagementMessages = messages.ContextManagementTools()
		}
	}
	// Rust #45124/#47773: the async user-message and request_user_input tools
	// follow the step model's advertised catalog entries as well.
	freeformAsyncRootAgent := !r.turnThreadIsSubagent(inputs.params.ThreadID)
	requestUserInputEnabled := inputs.cfg == nil || inputs.cfg.ExperimentalRequestUserInputEnabled()
	freeformAsyncFeature := inputs.cfg != nil && features.Enabled(featureSettings, "send_message_to_user_async")
	catalogTools := options.ExperimentalSupportedTools
	if requestUserInputAsyncAvailable(freeformAsyncRootAgent, requestUserInputEnabled, catalogTools) {
		options.ExperimentalSupportedTools = append(options.ExperimentalSupportedTools, tool.DefaultRequestUserInputAsyncToolName)
	}
	if freeformAsyncToolAvailable(freeformAsyncRootAgent, catalogTools, freeformAsyncFeature) {
		options.ExperimentalSupportedTools = append(options.ExperimentalSupportedTools, tool.DefaultSendMessageToUserAsyncToolName)
	}
	// Standalone web search and standalone image generation are model catalog
	// capabilities, so the step model decides whether they stay registered and
	// whether the turn still declares the hosted tools.
	stepParams := inputs.params
	stepParams.Model = modelID
	nativeWebSearchOptions, err := r.webSearchOptionsForTurn(inputs.cfg, &stepParams)
	if err != nil {
		return nil, nil, err
	}
	mcpTools := inputs.mcpTools
	mcpBinding := inputs.mcpBinding
	hasSearchConnector := false
	for i := range mcpTools {
		if mcp.IsModelSearchTool(&mcpTools[i]) {
			hasSearchConnector = true
			break
		}
	}
	if !hasSearchConnector && modelSearchMayUseApps(info, inputs.cfg) && inputs.options.MCPService != nil {
		refreshed, connectors, binding := r.mcpRuntimeInputsForServiceWithRequirementsAndBinding(inputs.params.ThreadID, inputs.cfg, inputs.options.MCPService, []string{mcp.CodexAppsServerName}, nil)
		if len(refreshed) > 0 {
			mcpTools = refreshed
			mcpBinding = binding
			options.MCPConnectors = connectors
		}
	}
	searchSelection := r.resolveModelSearchForTurn(context.Background(), inputs.params.ThreadID, inputs.turnID, inputs.cfg, info, mcpTools, mcpBinding, nativeWebSearchOptions, inputs.turnMetadata)
	searchSelection, searchReservationID := r.reserveModelSearchSelection(inputs.params.ThreadID, searchSelection)
	switch searchSelection.provider {
	case modelSearchNative:
		if searchSelection.mode != "" {
			mode := searchSelection.mode
			nativeWebSearchOptions, err = r.webSearchOptionsForTurnWithMode(inputs.cfg, &stepParams, &mode)
			if err != nil {
				return nil, nil, err
			}
			nativeWebSearchOptions = r.nativeModelSearchOptions(nativeWebSearchOptions, inputs.params.ThreadID, inputs.turnID)
		}
	case modelSearchUnmanaged:
		// Legacy configuration keeps the step model's standalone tool unchanged.
	default:
		nativeWebSearchOptions = nil
	}
	options.MCPTools = applyModelSearchTools(mcpTools, searchSelection)
	if searchSelection.provider == modelSearchHosted {
		options.MCPTurnMetadata = searchCallTurnMetadata(options.MCPTurnMetadata, searchSelection.context)
	}
	options.MCPToolAdmissionHook = nil
	if searchSelection.provider == modelSearchHosted || searchSelection.provider == modelSearchClientMCP {
		options.MCPToolAdmissionHook = r.modelSearchMCPAdmission(inputs.params.ThreadID, inputs.turnID, searchSelection)
	}
	r.threads.UpdateTurn(strings.TrimSpace(inputs.params.ThreadID), strings.TrimSpace(inputs.turnID), func(active *activeRuntimeTurn) {
		active.ModelSearchProvider = searchSelection.provider
		active.ModelSearchMode = searchSelection.mode
		active.ModelSearchFallback = searchSelection.fallback
		active.ModelSearchReservationID = searchReservationID
	})
	imageGenerationOptions, err := r.imageGenerationOptionsForTurn(inputs.cfg, &stepParams)
	if err != nil {
		return nil, nil, err
	}
	options.WebSearch = nativeWebSearchOptions
	options.ImageGeneration = imageGenerationOptions
	hostedTools, err := r.hostedToolsForTurn(&stepParams)
	if err != nil {
		return nil, nil, err
	}
	return &options, hostedTools, nil
}
