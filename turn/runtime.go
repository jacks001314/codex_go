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
)

type RuntimeOptions struct {
	Agent             model.AgentRunner
	Router            *tool.Router
	Hooks             tool.HookRunner
	SteerMailbox      *SteerMailbox
	HostedTools       []any
	Now               func() time.Time
	MaxTurns          int
	ExecutedToolCalls *ExecutedToolCallRecorder
}

type Runtime struct {
	agent             model.AgentRunner
	router            *tool.Router
	hooks             tool.HookRunner
	steerMailbox      *SteerMailbox
	hostedTools       []any
	now               func() time.Time
	maxTurns          int
	executedToolCalls *ExecutedToolCallRecorder
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
		agent:             options.Agent,
		router:            options.Router,
		hooks:             options.Hooks,
		steerMailbox:      options.SteerMailbox,
		hostedTools:       append([]any(nil), options.HostedTools...),
		now:               now,
		maxTurns:          options.MaxTurns,
		executedToolCalls: executedToolCalls,
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
		}
		sampling := timing.BeginSampling(r.now())
		response, err := r.agent.Run(ctx, &model.AgentRequest{
			Prompt:                       request.Prompt,
			Instructions:                 request.Instructions,
			InputItems:                   inputItems,
			Tools:                        MergeHostedTools(MergeHostedTools(request.Tools, r.hostedTools), request.HostedTools),
			Model:                        request.Model,
			ProviderID:                   request.ProviderID,
			TaskKind:                     request.TaskKind,
			ThreadID:                     request.ThreadID,
			TurnID:                       request.TurnID,
			Originator:                   request.Originator,
			Store:                        request.Store,
			PreviousResponseID:           request.PreviousResponseID,
			ParallelToolCalls:            request.ParallelToolCalls,
			ReasoningEffort:              request.ReasoningEffort,
			ReasoningSummary:             request.ReasoningSummary,
			ConcurrentReasoningSummaries: request.ConcurrentReasoningSummaries,
			ModelVerbosity:               request.ModelVerbosity,
			IncludeTimingMetrics:         request.IncludeTimingMetrics,
			BetaFeaturesHeader:           request.BetaFeaturesHeader,
			ItemIDsEnabled:               request.ItemIDsEnabled,
			ServiceTier:                  request.ServiceTier,
			PromptCacheKey:               request.PromptCacheKey,
			ClientMetadata:               cloneStringMap(clientMetadata),
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
	if len(loopRequest.Tools) == 0 {
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
		loopRequest.Tools = model.ResponsesToolsFromSpecs(visibleSpecs)
	}
	if effectiveToolMode == model.ToolModeCodeMode || effectiveToolMode == model.ToolModeCodeModeOnly {
		loopRequest.ClientMetadataTransform = newCodeModeClientMetadataTransform(loopRequest.ClientMetadata, r.router)
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
		}),
		MaxTurns: r.maxTurns,
		Now:      r.now,
	}).Run(ctx, &loopRequest)
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
		if spec.Exposure == tool.ExposureDirectModelOnly || !codemode.IsNestedTool(codemode.NameForToolName(spec.Name)) {
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
		if spec.Exposure == tool.ExposureDiscoverable {
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
	return newCodeModeClientMetadataTransform(metadata, router)(metadata)
}

func newCodeModeClientMetadataTransform(base map[string]string, router *tool.Router) ClientMetadataTransform {
	lite := strings.EqualFold(strings.TrimSpace(base["ws_request_header_x_openai_internal_codex_responses_lite"]), "true")
	baseTurnMetadata := strings.TrimSpace(base[codexapi.ClientCodexTurnMetadataHeader])
	toolNames := map[string]tool.CodeModeToolNameMetadata(nil)
	if lite && router != nil {
		toolNames = router.CodeModeToolNames()
	}
	return func(metadata map[string]string) map[string]string {
		out := cloneStringMap(metadata)
		if !lite || len(toolNames) == 0 {
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
		turnMetadata[codexapi.CodeModeToolNamesKey] = toolNames
		encoded, err := json.Marshal(turnMetadata)
		if err == nil {
			out[codexapi.ClientCodexTurnMetadataHeader] = string(encoded)
		}
		return out
	}
}
