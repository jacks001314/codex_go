package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"codex_go/agent"
	"codex_go/codemode"
	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/utils"
)

type RuntimeOptions struct {
	Agent  model.AgentRunner
	Router *tool.Router
	// ToolRegistrationMetrics receives the finalized registration histogram
	// before each sampling request (Rust #51868). Transport retries inside the
	// model client do not produce another sample.
	ToolRegistrationMetrics ToolRegistrationMetricSink
	// ToolMetadataMetrics receives the `codex.tool_metadata.shed_bytes`
	// histogram from retention and outgoing-message budgets (Rust #47758).
	ToolMetadataMetrics model.ToolMetadataMetricSink
	// CodeModeMessages carries the active model catalog's Code Mode overrides
	// for code_mode_only exec descriptions (Rust #47118).
	CodeModeMessages *model.CodeModeToolMessages
	// AllowedTools, when non-nil, restricts the model-visible tools and hosted
	// declarations (Rust #45711).
	AllowedTools *[]string
	// CodeModeMessagesForModel resolves the catalog overrides for a model so a
	// mid-turn model change can refresh exec/wait descriptions (Rust #47118).
	CodeModeMessagesForModel func(model string) *model.CodeModeToolMessages
	// CodeModeDefaultExecYieldTime substitutes
	// {{ default_exec_yield_time_ms }} in catalog exec text.
	CodeModeDefaultExecYieldTime time.Duration
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
	// IndirectDescriptionPrefixes carries the active model's indirect namespace
	// guidance for Code Mode rendering (Rust #47642).
	IndirectDescriptionPrefixes *tool.IndirectNamespacePrefixes
	// DynamicToolCaller, DynamicToolThreadID, and DynamicToolTurnID let a later
	// ReplaceDynamicTools register executors for a new catalog (Rust #57553).
	DynamicToolCaller   DynamicToolCaller
	DynamicToolThreadID string
	DynamicToolTurnID   string
	// DynamicTools is the catalog registered in Router when the runtime was
	// built, used to recognize tools owned by this thread.
	DynamicTools []DynamicToolSpec
	// DynamicToolStepRefresh rebuilds the model-visible tool list before every
	// sampling step, so a mid-turn replacement applies to the following step
	// (Rust #57553).
	DynamicToolStepRefresh bool
	// InvocationRegistry opts this turn into live V2 tool-call lookup and unique
	// active call IDs (Rust #57586 HostToolInvocationLookup). A nil registry
	// leaves dispatch unchanged.
	InvocationRegistry *agent.InvocationRegistry
}

type Runtime struct {
	agent                        model.AgentRunner
	router                       *tool.Router
	toolRegistrationMetrics      ToolRegistrationMetricSink
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
	indirectDescriptionPrefixes  *tool.IndirectNamespacePrefixes
	codeModeMessages             *model.CodeModeToolMessages
	allowedTools                 *[]string
	codeModeMessagesForModel     func(model string) *model.CodeModeToolMessages
	codeModeDefaultExecYieldTime time.Duration
	dynamicToolMu                sync.Mutex
	dynamicToolCaller            DynamicToolCaller
	dynamicToolThreadID          string
	dynamicToolTurnID            string
	dynamicToolNames             map[string]tool.ToolName
	dynamicToolStepRefresh       bool
	invocationRegistry           *agent.InvocationRegistry
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
	executedToolCalls.SetToolMetadataMetrics(options.ToolMetadataMetrics)
	dynamicTools := make(map[string]tool.ToolName)
	for _, entry := range dynamicToolEntries(options.DynamicTools) {
		if entry.function == nil {
			continue
		}
		_, spec, err := newDynamicToolExecutor(nil, entry.namespace, entry.function)
		if err != nil {
			continue
		}
		dynamicTools[spec.Name.Key()] = spec.Name
	}
	return &Runtime{
		agent:                        options.Agent,
		router:                       options.Router,
		toolRegistrationMetrics:      options.ToolRegistrationMetrics,
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
		indirectDescriptionPrefixes:  options.IndirectDescriptionPrefixes,
		codeModeMessages:             options.CodeModeMessages,
		allowedTools:                 options.AllowedTools,
		codeModeMessagesForModel:     options.CodeModeMessagesForModel,
		codeModeDefaultExecYieldTime: options.CodeModeDefaultExecYieldTime,
		dynamicToolCaller:            options.DynamicToolCaller,
		dynamicToolThreadID:          strings.TrimSpace(options.DynamicToolThreadID),
		dynamicToolTurnID:            strings.TrimSpace(options.DynamicToolTurnID),
		dynamicToolNames:             dynamicTools,
		dynamicToolStepRefresh:       options.DynamicToolStepRefresh,
		invocationRegistry:           options.InvocationRegistry,
	}
}

func (r *Runtime) DeferredToolNamespaces() map[string]string {
	if r == nil || r.router == nil {
		return nil
	}
	return r.router.DeferredToolNamespaces()
}

// ReplaceDynamicTools publishes a new dynamic-tool catalog on this turn's
// router (Rust #57553). Tools removed from the catalog are hidden from later
// model steps but keep their executors so calls already emitted by the previous
// step still dispatch; a new or updated tool becomes visible on the next step.
// An empty catalog clears the catalog.
func (r *Runtime) ReplaceDynamicTools(tools []DynamicToolSpec) error {
	if r == nil || r.router == nil {
		return errors.New("turn runtime has no tool registry")
	}
	registry := r.router.Registry()
	if registry == nil {
		return errors.New("turn runtime has no tool registry")
	}
	if err := ValidateDynamicTools(tools); err != nil {
		return err
	}
	r.dynamicToolMu.Lock()
	defer r.dynamicToolMu.Unlock()
	next := make(map[string]tool.ToolName, len(tools))
	options := &DynamicToolRegistryOptions{
		Caller:   r.dynamicToolCaller,
		ThreadID: r.dynamicToolThreadID,
		TurnID:   r.dynamicToolTurnID,
	}
	for _, entry := range dynamicToolEntries(tools) {
		executor, spec, err := newDynamicToolExecutor(options, entry.namespace, entry.function)
		if err != nil {
			return err
		}
		if executor == nil {
			continue
		}
		key := spec.Name.Key()
		if current, ok := registry.Spec(spec.Name); ok && isDynamicToolSpec(current) {
			// Keep the executor that served the previous step so a pending call
			// for this name still runs with its original schema.
			registry.ReplaceSpec(spec)
			next[key] = spec.Name
			continue
		}
		if _, exists := registry.Lookup(spec.Name); exists {
			// A built-in tool owns this name; RegisterExternal would skip it.
			continue
		}
		if _, err := registry.RegisterExternal(executor); err != nil {
			return err
		}
		next[key] = spec.Name
	}
	for key, name := range r.dynamicToolNames {
		if _, ok := next[key]; ok {
			continue
		}
		current, ok := registry.Spec(name)
		if !ok || !isDynamicToolSpec(current) {
			continue
		}
		registry.SetExposure(name, tool.ExposureHidden)
	}
	r.dynamicToolNames = next
	return nil
}

func isDynamicToolSpec(spec tool.Spec) bool {
	return spec.Search != nil && spec.Search.Source != nil && spec.Search.Source.Name == dynamicToolSearchSourceName
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

// DeferMailboxPreemptionEnabled reports whether the assembled runtime defers
// mailbox preemption (Rust `features.defer_mailbox_preemption`, #49262/#51249).
// The app-server assembly path asserts its feature gate with it, mirroring
// StandaloneWebSearchRegistered above.
func (r *Runtime) DeferMailboxPreemptionEnabled() bool {
	return r != nil && r.deferMailboxPreemption
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
	r.router.SetToolMode(effectiveToolMode)
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
		recordToolRegistrationMetrics(r.toolRegistrationMetrics, tool.RegistrationCounts{}, request.ToolMode, request.IncrementalTools)
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
			OnWireInputBounded: func(original, bounded []any) {
				if executedToolCalls != nil {
					executedToolCalls.InvalidateWireInventoryLoss(original, bounded)
				}
			},
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
	if loopRequest.CriticalPathTiming && loopRequest.Timing == nil {
		loopRequest.Timing = NewTimingState()
	}
	if loopRequest.SteerMailbox == nil {
		loopRequest.SteerMailbox = r.steerMailbox
	}
	effectiveToolMode, warning := r.PrepareToolMode(loopRequest.ToolMode, loopRequest.DisableCodeModeFallback)
	if warning != "" && loopRequest.OnWarning != nil {
		loopRequest.OnWarning(warning)
	}
	loopRequest.ToolMode = effectiveToolMode
	buildTools := func() []any {
		specs := r.modelVisibleSpecsForRequest(effectiveToolMode)
		tools := MergeHostedTools(MergeHostedTools(model.ResponsesToolsFromSpecs(specs), r.hostedTools), request.HostedTools)
		return filterAllowedHostedTools(tools, r.allowedTools)
	}
	var visibleSpecs []tool.Spec
	if len(loopRequest.Tools) == 0 {
		visibleSpecs = r.modelVisibleSpecsForRequest(effectiveToolMode)
		loopRequest.Tools = model.ResponsesToolsFromSpecs(visibleSpecs)
	}
	if r.codeModeMessagesForModel != nil || r.dynamicToolStepRefresh {
		loopRequest.ToolsForStep = func(modelID string) []any {
			if r.codeModeMessagesForModel != nil {
				r.applyCodeModeMessagesForModel(modelID)
			}
			return buildTools()
		}
	}
	if effectiveToolMode == model.ToolModeCodeMode || effectiveToolMode == model.ToolModeCodeModeOnly {
		loopRequest.ClientMetadataTransform = newCodeModeClientMetadataTransform(loopRequest.ClientMetadata, r.router, visibleSpecs, r.turnMetadataIncludesToolInfo)
	}
	if loopRequest.ClientMetadataTransform != nil {
		loopRequest.ClientMetadata = loopRequest.ClientMetadataTransform(loopRequest.ClientMetadata)
	}
	loopRequest.Tools = MergeHostedTools(MergeHostedTools(loopRequest.Tools, r.hostedTools), request.HostedTools)
	loopRequest.Tools = filterAllowedHostedTools(loopRequest.Tools, r.allowedTools)
	return NewAgentLoop(&AgentLoopOptions{
		Agent:                   r.agent,
		SteerMailbox:            r.steerMailbox,
		ExecutedToolCalls:       executedToolCalls,
		ToolRegistrationMetrics: r.toolRegistrationMetrics,
		ToolRegistrationCounts:  r.toolRegistrationCounts,
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
			ToolMode:                    firstNonEmpty(r.router.ToolMode(), loopRequest.ToolMode),
			InstantInterrupt:            r.instantInterrupt,
			Truncation:                  r.truncationPolicy(loopRequest.Model),
			TruncationForModel:          r.truncationPolicy,
			OnToolOutputExternalContext: r.toolOutputExternalContextHandler(loopRequest.ThreadID),
			Tracer:                      loopRequest.Tracer,
			CriticalPathTiming:          loopRequest.CriticalPathTiming,
			TimingState:                 loopRequest.Timing,
			InvocationRegistry:          r.invocationRegistry,
		}),
		MaxTurns:               r.maxTurns,
		Now:                    r.now,
		DeferMailboxPreemption: r.deferMailboxPreemption,
	}).Run(ctx, &loopRequest)
}

func filterAllowedHostedTools(tools []any, allowed *[]string) []any {
	if allowed == nil {
		return tools
	}
	names := make(map[string]struct{}, len(*allowed))
	for _, name := range *allowed {
		if name = strings.TrimSpace(name); name != "" {
			names[name] = struct{}{}
		}
	}
	out := make([]any, 0, len(tools))
	for _, spec := range tools {
		object, _ := spec.(map[string]any)
		name, _ := object["name"].(string)
		name = strings.TrimSpace(name)
		if _, ok := names[name]; ok {
			out = append(out, spec)
		}
	}
	return out
}

func (r *Runtime) toolRegistrationCounts() tool.RegistrationCounts {
	if r == nil || r.router == nil {
		return tool.RegistrationCounts{}
	}
	return r.router.RegistrationCounts()
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
		visibleSpecs = codeModeOnlyExecPromptSpecsWithOptions(visibleSpecs, r.router.CodeModeToolSpecs(), r.indirectDescriptionPrefixes, r.codeModeMessages, r.codeModeDefaultExecYieldTime)
	}
	if codemode.HasExecTool(visibleSpecs) {
		visibleSpecs = augmentCodeModeWinnerSpecs(visibleSpecs, r.router.CodeModeToolSpecs())
	}
	return visibleSpecs
}

func (r *Runtime) applyCodeModeMessagesForModel(modelID string) {
	if r == nil || r.codeModeMessagesForModel == nil {
		return
	}
	messages := r.codeModeMessagesForModel(modelID)
	r.codeModeMessages = messages
	if r.router != nil {
		r.router.SetCodeModeMessages(messages)
	}
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

// ModelVisibleToolDefinitionsWithoutThirdParty mirrors a turn whose MCP
// projection is empty (Rust `McpServersDisabled`): MCP, app, and other
// third-party specs are omitted while built-in tools stay declared.
func (r *Runtime) ModelVisibleToolDefinitionsWithoutThirdParty(toolMode string, disableCodeModeFallback bool) []any {
	if r == nil {
		return nil
	}
	specs := r.modelVisibleSpecsForRequest(r.effectiveToolModeForRequest(toolMode, disableCodeModeFallback))
	kept := make([]tool.Spec, 0, len(specs))
	for _, spec := range specs {
		if spec.ThirdParty {
			continue
		}
		kept = append(kept, spec)
	}
	return model.ResponsesToolsFromSpecs(kept)
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

func codeModeOnlyExecPromptSpecs(visibleSpecs []tool.Spec, nestedSpecs []tool.Spec, indirectPrefixes *tool.IndirectNamespacePrefixes) []tool.Spec {
	return codeModeOnlyExecPromptSpecsWithOptions(visibleSpecs, nestedSpecs, indirectPrefixes, nil, 0)
}

func codeModeOnlyExecPromptSpecsWithOptions(visibleSpecs []tool.Spec, nestedSpecs []tool.Spec, indirectPrefixes *tool.IndirectNamespacePrefixes, messages *model.CodeModeToolMessages, defaultYieldTime time.Duration) []tool.Spec {
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
	if indirectPrefixes != nil {
		if prefix := indirectPrefixes.PrefixForNamespace("functions"); prefix != "" {
			namespaces[""] = codemode.NamespaceDescription{Name: "functions", Description: prefix}
		}
	}
	defaultYieldMS := int(defaultYieldTime / time.Millisecond)
	if defaultYieldMS <= 0 {
		defaultYieldMS = 10000
	}
	description := codemode.BuildExecToolDescriptionWithOptions(
		codemode.CollectPromptDefinitions(enabledSpecs),
		codemode.CollectPromptDefinitions(deferredSpecs),
		namespaces,
		true,
		len(deferredSpecs) > 0,
		messages,
		defaultYieldMS,
		"image(value), audio(value), generatedImage(value): Append supported media results.",
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
		if router != nil {
			toolInventory = router.ToolNamespacesInfo()
		}
		if toolInventory == nil && router != nil {
			specs := modelVisibleSpecs
			if len(specs) == 0 {
				specs = router.ModelVisibleSpecs()
			}
			toolInventory = tool.CollectToolNamespacesInfo(router.Registry(), router.CodeModeToolNames(), specs)
		}
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
