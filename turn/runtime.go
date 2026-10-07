package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"codex_go/codemode"
	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/utils"
)

type RuntimeOptions struct {
	Agent  model.AgentRunner
	Router *tool.Router
	// TurnMetadataIncludesToolInfo mirrors the
	// features.tool_registry.turn_metadata_includes_tool_info gate: when set,
	// a Responses Lite request's turn metadata carries the model-visible tool
	// inventory (Rust collect_tool_namespaces_info).
	TurnMetadataIncludesToolInfo bool
	Hooks                        tool.HookRunner
	SteerMailbox                 *SteerMailbox
	HostedTools                  []any
	Now                          func() time.Time
	MaxTurns                     int
	ExecutedToolCalls            *ExecutedToolCallRecorder
	// TruncationPolicyForModel resolves a model's output-truncation policy
	// (Rust `ModelInfo::truncation_policy`). When set, every tool call of a turn
	// carries the policy so tools can bound their response budget; when nil, the
	// tool's own limit governs.
	TruncationPolicyForModel func(model string) *utils.TruncationPolicy
	// OnToolOutputExternalContext is invoked with a turn's thread ID after a tool
	// returns an output that declares external context (Rust `handle_any_tool`
	// marking the thread's memory mode polluted).
	OnToolOutputExternalContext func(ctx context.Context, threadID string, invocation *tool.Invocation)
	// InstantInterrupt mirrors the `features.instant_interrupt` gate (#48135):
	// when set, each sampling request watches the turn's queued user input and
	// yields its code-mode observations once a user message arrives.
	InstantInterrupt bool
	// DeferMailboxPreemption mirrors the `features.defer_mailbox_preemption`
	// gate (#47913, default off): when set, a response keeps its remaining tool
	// calls even though inter-agent mail is queued at a commentary or
	// partial-answer boundary (#49262, #51249).
	DeferMailboxPreemption bool
}

type Runtime struct {
	agent                        model.AgentRunner
	router                       *tool.Router
	turnMetadataIncludesToolInfo bool
	hooks                        tool.HookRunner
	steerMailbox                 *SteerMailbox
	hostedTools                  []any
	now                          func() time.Time
	maxTurns                     int
	executedToolCalls            *ExecutedToolCallRecorder
	truncationPolicyForModel     func(model string) *utils.TruncationPolicy
	onToolOutputExternalContext  func(ctx context.Context, threadID string, invocation *tool.Invocation)
	instantInterrupt             bool
	deferMailboxPreemption       bool
}

func NewRuntime(options *RuntimeOptions) *Runtime {
	if options == nil {
		options = &RuntimeOptions{}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	executedToolCalls := options.ExecutedToolCalls
	if executedToolCalls == nil {
		executedToolCalls = NewExecutedToolCallRecorder()
	}
	return &Runtime{
		agent:                        options.Agent,
		router:                       options.Router,
		turnMetadataIncludesToolInfo: options.TurnMetadataIncludesToolInfo,
		hooks:                        options.Hooks,
		steerMailbox:                 options.SteerMailbox,
		hostedTools:                  append([]any(nil), options.HostedTools...),
		now:                          now,
		maxTurns:                     options.MaxTurns,
		executedToolCalls:            executedToolCalls,
		truncationPolicyForModel:     options.TruncationPolicyForModel,
		onToolOutputExternalContext:  options.OnToolOutputExternalContext,
		instantInterrupt:             options.InstantInterrupt,
		deferMailboxPreemption:       options.DeferMailboxPreemption,
	}
}

func (r *Runtime) DeferredToolNamespaces() map[string]string {
	if r == nil || r.router == nil {
		return nil
	}
	return r.router.DeferredToolNamespaces()
}

func (r *Runtime) StandaloneWebSearchRegistered() bool {
	if r == nil || r.router == nil {
		return false
	}
	executor, ok := r.router.Executor(tool.NamespacedName(WebSearchNamespace, WebSearchRunTool))
	if !ok {
		return false
	}
	_, ok = executor.(*WebSearchHandler)
	return ok
}

// PrepareToolMode resolves code-mode availability and consumes the per-thread
// warning before a turn starts. Run calls it as a fallback for non-app-server
// callers that do not preflight the turn.
func (r *Runtime) PrepareToolMode(requestedToolMode string, disableCodeModeFallback bool) (string, string) {
	requestedToolMode = strings.ToLower(strings.TrimSpace(requestedToolMode))
	if requestedToolMode == "" {
		// Mirrors Rust's requested_tool_mode default when no model tool_mode
		// is declared and no code-mode feature is enabled. Callers resolve the
		// model-level tool mode (model.ResolveToolMode) before reaching here;
		// direct is the safe fallback so third-party providers never see the
		// code-mode exec freeform tool unexpectedly.
		requestedToolMode = model.ToolModeDirect
	}
	effectiveToolMode := requestedToolMode
	if r == nil || r.router == nil {
		return effectiveToolMode, ""
	}
	codeModeErr := r.router.CodeModeAvailability()
	if codeModeErr != nil && requestedToolMode == model.ToolModeCodeMode && !disableCodeModeFallback {
		effectiveToolMode = model.ToolModeDirect
	}
	if codeModeErr == nil || (requestedToolMode != model.ToolModeCodeMode && requestedToolMode != model.ToolModeCodeModeOnly) {
		return effectiveToolMode, ""
	}
	return effectiveToolMode, r.router.TakeCodeModeUnavailableWarning(effectiveToolMode)
}

func (r *Runtime) Run(ctx context.Context, request *AgentLoopRequest) (*AgentLoopResult, error) {
	if r == nil || r.agent == nil {
		return nil, errors.New("turn runtime agent is nil")
	}
	if request == nil {
		return nil, model.ErrInvalidAgentRequest
	}
	var executedToolCalls *ExecutedToolCallRecorder
	if request.ExecutedToolCallMetadataEnabled {
		executedToolCalls = r.executedToolCalls
	}
	if r.router == nil {
		inputItems := append([]any(nil), request.InputItems...)
		clientMetadata := cloneStringMap(request.ClientMetadata)
		if steer := drainSteer(r.steerMailbox, request); steer != nil {
			if len(steer.InputItems) > 0 {
				inputItems = append(inputItems, steer.InputItems...)
			}
			if len(steer.ClientMetadata) > 0 {
				clientMetadata = cloneStringMap(steer.ClientMetadata)
			}
		}
		timing := request.Timing
		if timing == nil {
			timing = NewTimingState()
		}
		timing.MarkTurnStarted(r.now())
		var executedToolCallAttachment *ExecutedToolCallAttachment
		if executedToolCalls != nil {
			inputItems, executedToolCallAttachment = executedToolCalls.AttachPendingToPrompt(inputItems)
		} else {
			// Capture is disabled for this turn, so direct records already in
			// history are stripped from the request (Rust #45185).
			StripDirectCallMetadata(inputItems)
		}
		tools := MergeHostedTools(MergeHostedTools(request.Tools, r.hostedTools), request.HostedTools)
		sampling := timing.BeginSampling(r.now())
		// Rust #50964: the sampling request compares its full model-visible tool
		// list against the retained one and counts a change on the turn profile.
		if tracker, ok := r.agent.(model.InferenceToolTracker); ok && tracker.InferenceToolsChanged(request.ThreadID, tools) {
			timing.RecordToolsChange()
		}
		instructions := request.Instructions
		if request.InstructionsProvider != nil {
			instructions = request.InstructionsProvider()
		}
		response, err := r.agent.Run(ctx, &model.AgentRequest{
			Prompt:                       request.Prompt,
			Instructions:                 instructions,
			InputItems:                   inputItems,
			Tools:                        tools,
			Model:                        request.Model,
			ProviderID:                   request.ProviderID,
			TaskKind:                     request.TaskKind,
			ThreadID:                     request.ThreadID,
			TurnID:                       request.TurnID,
			Ephemeral:                    request.Ephemeral,
			Originator:                   request.Originator,
			Store:                        request.Store,
			PreviousResponseID:           request.PreviousResponseID,
			ParallelToolCalls:            request.ParallelToolCalls,
			ReasoningEffort:              request.ReasoningEffort,
			ReasoningSummary:             request.ReasoningSummary,
			DropReasoningEffortUpdates:   request.DropReasoningEffortUpdates,
			ConcurrentReasoningSummaries: request.ConcurrentReasoningSummaries,
			ModelVerbosity:               request.ModelVerbosity,
			IncludeTimingMetrics:         request.IncludeTimingMetrics,
			BetaFeaturesHeader:           request.BetaFeaturesHeader,
			ItemIDsEnabled:               request.ItemIDsEnabled,
			ServiceTier:                  request.ServiceTier,
			PromptCacheKey:               request.PromptCacheKey,
			CyberAccessProgram:           request.CyberAccessProgram,
			ClientMetadata:               cloneStringMap(clientMetadata),
			Trace:                        request.Trace,
			AttestationProvider:          request.AttestationProvider,
			OutputSchema:                 request.OutputSchema,
			DisableHostedImageGeneration: request.DisableHostedImageGeneration,
			StreamHandler:                combineResponsesStreamHandlers(request.StreamHandler, timingStreamHandler(timing, r.now)),
		})
		sampling.CloseAt(r.now())
		if err != nil {
			return nil, err
		}
		if executedToolCalls != nil {
			executedToolCalls.CommitAttachment(executedToolCallAttachment)
		}
		recordResponseTiming(timing, response, r.now())
		resultInputItems := append([]any(nil), inputItems...)
		if strings.TrimSpace(request.Prompt) != "" {
			if userMessage := model.UserMessageInputItem(request.Prompt); userMessage != nil {
				resultInputItems = append(resultInputItems, userMessage)
			}
		}
		for i := range response.Items {
			if !isToolAgentItem(&response.Items[i]) {
				item := response.Items[i]
				resultInputItems = append(resultInputItems, &item)
			}
		}
		if len(toolAgentItems(response)) > 0 {
			return nil, errors.New("agent requested tool calls but tool dispatcher is nil")
		}
		profile := timing.CompleteProfile(r.now())
		return &AgentLoopResult{Response: response, Responses: []*model.AgentResponse{response}, InputItems: resultInputItems, InitialInputCount: len(request.InputItems), Usage: response.Usage, Iterations: 1, TimingProfile: &profile}, nil
	}
	loopRequest := *request
	if loopRequest.SteerMailbox == nil {
		loopRequest.SteerMailbox = r.steerMailbox
	}
	effectiveToolMode, warning := r.PrepareToolMode(loopRequest.ToolMode, loopRequest.DisableCodeModeFallback)
	if warning != "" && loopRequest.OnWarning != nil {
		loopRequest.OnWarning(warning)
	}
	loopRequest.ToolMode = effectiveToolMode
	var visibleSpecs []tool.Spec
	if len(loopRequest.Tools) == 0 {
		visibleSpecs = r.modelVisibleSpecsForRequest(effectiveToolMode)
		loopRequest.Tools = model.ResponsesToolsFromSpecs(visibleSpecs)
	}
	if effectiveToolMode == model.ToolModeCodeMode || effectiveToolMode == model.ToolModeCodeModeOnly {
		loopRequest.ClientMetadataTransform = newCodeModeClientMetadataTransform(loopRequest.ClientMetadata, r.router, visibleSpecs, r.turnMetadataIncludesToolInfo)
	}
	if loopRequest.ClientMetadataTransform != nil {
		loopRequest.ClientMetadata = loopRequest.ClientMetadataTransform(loopRequest.ClientMetadata)
	}
	loopRequest.Tools = MergeHostedTools(MergeHostedTools(loopRequest.Tools, r.hostedTools), request.HostedTools)
	return NewAgentLoop(&AgentLoopOptions{
		Agent:             r.agent,
		SteerMailbox:      r.steerMailbox,
		ExecutedToolCalls: executedToolCalls,
		Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{
			Router:                      r.router,
			Hooks:                       r.hooks,
			Now:                         r.now,
			PostToolInputItems:          request.PostToolInputItems,
			OnToolStarted:               request.OnToolStarted,
			OnToolCompleted:             request.OnToolCompleted,
			EmitCodeModeNestedLifecycle: request.EmitCodeModeNestedLifecycle,
			OnCodeModeNotify:            request.OnCodeModeNotify,
			ThreadID:                    request.ThreadID,
			TurnID:                      request.TurnID,
			ExecutedToolCalls:           executedToolCalls,
			ToolMode:                    loopRequest.ToolMode,
			InstantInterrupt:            r.instantInterrupt,
			Truncation:                  r.truncationPolicy(loopRequest.Model),
			OnToolOutputExternalContext: r.toolOutputExternalContextHandler(loopRequest.ThreadID),
		}),
		MaxTurns:               r.maxTurns,
		Now:                    r.now,
		DeferMailboxPreemption: r.deferMailboxPreemption,
	}).Run(ctx, &loopRequest)
}

// modelVisibleSpecsForRequest resolves the model-visible tool specs a request
// with no pinned tool list carries for an effective tool mode. Run and the
// incremental Responses Lite catalog share it so the recorded declarations
// match the tools the request would otherwise send (Rust #50540
// `create_tools_json_for_responses_lite(model_visible_specs())`).
func (r *Runtime) modelVisibleSpecsForRequest(effectiveToolMode string) []tool.Spec {
	if r == nil || r.router == nil {
		return nil
	}
	visibleSpecs := r.router.ModelVisibleSpecs()
	if effectiveToolMode == model.ToolModeDirect {
		visibleSpecs = directModeVisibleSpecs(visibleSpecs, r.router.CodeModeToolSpecs())
	} else if effectiveToolMode == model.ToolModeCodeModeOnly && codemode.HasExecTool(visibleSpecs) {
		visibleSpecs = codeModeOnlyVisibleSpecs(visibleSpecs)
		visibleSpecs = codeModeOnlyExecPromptSpecs(visibleSpecs, r.router.CodeModeToolSpecs())
	}
	if codemode.HasExecTool(visibleSpecs) {
		visibleSpecs = augmentCodeModeWinnerSpecs(visibleSpecs, r.router.CodeModeToolSpecs())
	}
	return visibleSpecs
}

// effectiveToolModeForRequest mirrors PrepareToolMode's code-mode downgrade
// without consuming the per-thread unavailability warning, so callers that only
// inspect the turn's tools leave the warning for the request itself.
func (r *Runtime) effectiveToolModeForRequest(requestedToolMode string, disableCodeModeFallback bool) string {
	requestedToolMode = strings.ToLower(strings.TrimSpace(requestedToolMode))
	if requestedToolMode == "" {
		requestedToolMode = model.ToolModeDirect
	}
	if r == nil || r.router == nil {
		return requestedToolMode
	}
	if err := r.router.CodeModeAvailability(); err != nil && requestedToolMode == model.ToolModeCodeMode && !disableCodeModeFallback {
		return model.ToolModeDirect
	}
	return requestedToolMode
}

// ModelVisibleToolDefinitions returns the serialized Responses Lite tool
// declarations (namespaces, their members and built-ins) for this turn's
// visible tools. The incremental tool catalog diffs this value against the
// catalog recorded for the context window (Rust
// `create_tools_json_for_responses_lite(&model_visible_specs())`, #50540).
func (r *Runtime) ModelVisibleToolDefinitions(toolMode string, disableCodeModeFallback bool) []any {
	if r == nil {
		return nil
	}
	return model.ResponsesToolsFromSpecs(r.modelVisibleSpecsForRequest(r.effectiveToolModeForRequest(toolMode, disableCodeModeFallback)))
}

// truncationPolicy resolves the effective model's output-truncation policy for
// the turn's tool calls (Rust `ToolCall::truncation_policy`).
func (r *Runtime) truncationPolicy(modelID string) *utils.TruncationPolicy {
	if r == nil || r.truncationPolicyForModel == nil {
		return nil
	}
	return r.truncationPolicyForModel(strings.TrimSpace(modelID))
}

// toolOutputExternalContextHandler binds the turn's thread ID to the host's
// external-context handler (Rust `handle_any_tool`).
func (r *Runtime) toolOutputExternalContextHandler(threadID string) func(ctx context.Context, invocation *tool.Invocation, output *tool.Output) {
	if r == nil || r.onToolOutputExternalContext == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	return func(ctx context.Context, invocation *tool.Invocation, output *tool.Output) {
		r.onToolOutputExternalContext(ctx, threadID, invocation)
	}
}

func directModeVisibleSpecs(visibleSpecs []tool.Spec, codeModeSpecs []tool.Spec) []tool.Spec {
	out := make([]tool.Spec, 0, len(visibleSpecs)+1)
	seen := make(map[string]struct{}, len(visibleSpecs)+1)
	for _, spec := range visibleSpecs {
		if codemode.IsPublicToolName(spec.Name) || spec.Name.Key() == codemode.WaitToolName {
			continue
		}
		out = append(out, spec)
		seen[spec.Name.Key()] = struct{}{}
	}
	for _, spec := range codeModeSpecs {
		if spec.Exposure != tool.ExposureHidden {
			continue
		}
		if _, exists := seen[spec.Name.Key()]; exists {
			continue
		}
		spec.Exposure = tool.ExposureModelVisible
		out = append(out, spec)
		seen[spec.Name.Key()] = struct{}{}
	}
	return out
}

func augmentCodeModeWinnerSpecs(specs []tool.Spec, nestedSpecs []tool.Spec) []tool.Spec {
	winners := make(map[string]struct{}, len(nestedSpecs))
	for _, spec := range nestedSpecs {
		winners[spec.Name.Key()] = struct{}{}
	}
	out := append([]tool.Spec(nil), specs...)
	for index := range out {
		name := out[index].Name
		if codemode.IsPublicToolName(name) || name.Key() == codemode.WaitToolName {
			out[index] = codemode.AugmentToolSpec(out[index])
			continue
		}
		if _, ok := winners[name.Key()]; ok {
			out[index] = codemode.AugmentToolSpec(out[index])
		}
	}
	return out
}

func codeModeOnlyVisibleSpecs(specs []tool.Spec) []tool.Spec {
	out := make([]tool.Spec, 0, len(specs))
	for _, spec := range specs {
		if (spec.Exposure == tool.ExposureDirectModelOnly || spec.Exposure == tool.ExposureDeferredModelOnly) || !codemode.IsNestedTool(codemode.NameForToolName(spec.Name)) {
			out = append(out, spec)
		}
	}
	return out
}

func codeModeOnlyExecPromptSpecs(visibleSpecs []tool.Spec, nestedSpecs []tool.Spec) []tool.Spec {
	enabledSpecs := make([]tool.Spec, 0, len(nestedSpecs))
	deferredSpecs := make([]tool.Spec, 0)
	namespaces := map[string]codemode.NamespaceDescription{}
	for _, spec := range nestedSpecs {
		if tool.IsDeferred(spec.Exposure) {
			deferredSpecs = append(deferredSpecs, spec)
			continue
		}
		enabledSpecs = append(enabledSpecs, spec)
		namespace := strings.TrimSpace(spec.Name.Namespace)
		if namespace == "" {
			continue
		}
		description := strings.TrimSpace(spec.NamespaceDescription)
		existing, ok := namespaces[namespace]
		if !ok || (strings.TrimSpace(existing.Description) == "" && description != "") {
			namespaces[namespace] = codemode.NamespaceDescription{Name: namespace, Description: description}
		}
	}
	description := codemode.BuildExecToolDescriptionWithDeferred(
		codemode.CollectPromptDefinitions(enabledSpecs),
		codemode.CollectPromptDefinitions(deferredSpecs),
		namespaces,
		true,
		len(deferredSpecs) > 0,
	)
	out := append([]tool.Spec(nil), visibleSpecs...)
	for index := range out {
		if codemode.IsPublicToolName(out[index].Name) && out[index].Freeform != nil {
			out[index].Description = description
			break
		}
	}
	return out
}

func codeModeClientMetadataForRequest(metadata map[string]string, router *tool.Router) map[string]string {
	return newCodeModeClientMetadataTransform(metadata, router, nil, false)(metadata)
}

func newCodeModeClientMetadataTransform(base map[string]string, router *tool.Router, modelVisibleSpecs []tool.Spec, includeToolInfo bool) ClientMetadataTransform {
	lite := strings.EqualFold(strings.TrimSpace(base["ws_request_header_x_openai_internal_codex_responses_lite"]), "true")
	baseTurnMetadata := strings.TrimSpace(base[codexapi.ClientCodexTurnMetadataHeader])
	toolInventory := tool.ToolNamespacesInfo(nil)
	if lite && includeToolInfo {
		registry := (*tool.Registry)(nil)
		if router != nil {
			registry = router.Registry()
		}
		specs := modelVisibleSpecs
		if len(specs) == 0 && router != nil {
			specs = router.ModelVisibleSpecs()
		}
		codeModeToolNames := map[string]tool.CodeModeToolNameMetadata(nil)
		if router != nil {
			codeModeToolNames = router.CodeModeToolNames()
		}
		toolInventory = tool.CollectToolNamespacesInfo(registry, codeModeToolNames, specs)
	}
	return func(metadata map[string]string) map[string]string {
		out := cloneStringMap(metadata)
		if !lite || toolInventory == nil {
			return out
		}
		out["ws_request_header_x_openai_internal_codex_responses_lite"] = "true"
		turnMetadataJSON := strings.TrimSpace(out[codexapi.ClientCodexTurnMetadataHeader])
		if turnMetadataJSON == "" {
			turnMetadataJSON = baseTurnMetadata
		}
		var turnMetadata map[string]any
		if err := json.Unmarshal([]byte(turnMetadataJSON), &turnMetadata); err != nil || turnMetadata == nil {
			return out
		}
		turnMetadata[codexapi.ToolNamespacesInfoKey] = toolInventory
		encoded, err := json.Marshal(turnMetadata)
		if err == nil {
			out[codexapi.ClientCodexTurnMetadataHeader] = string(encoded)
		}
		return out
	}
}
