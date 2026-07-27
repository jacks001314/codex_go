package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/tool"
)

type RuntimeOptions struct {
	Agent        model.AgentRunner
	Router       *tool.Router
	Hooks        tool.HookRunner
	SteerMailbox *SteerMailbox
	HostedTools  []any
	Now          func() time.Time
	MaxTurns     int
}

type Runtime struct {
	agent        model.AgentRunner
	router       *tool.Router
	hooks        tool.HookRunner
	steerMailbox *SteerMailbox
	hostedTools  []any
	now          func() time.Time
	maxTurns     int
}

func NewRuntime(options *RuntimeOptions) *Runtime {
	if options == nil {
		options = &RuntimeOptions{}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Runtime{
		agent:        options.Agent,
		router:       options.Router,
		hooks:        options.Hooks,
		steerMailbox: options.SteerMailbox,
		hostedTools:  append([]any(nil), options.HostedTools...),
		now:          now,
		maxTurns:     options.MaxTurns,
	}
}

func (r *Runtime) DeferredToolNamespaces() map[string]string {
	if r == nil || r.router == nil {
		return nil
	}
	return r.router.DeferredToolNamespaces()
}

func (r *Runtime) Run(ctx context.Context, request *AgentLoopRequest) (*AgentLoopResult, error) {
	if r == nil || r.agent == nil {
		return nil, errors.New("turn runtime agent is nil")
	}
	if request == nil {
		return nil, model.ErrInvalidAgentRequest
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
		return &AgentLoopResult{Response: response, Responses: []*model.AgentResponse{response}, InputItems: resultInputItems, Usage: response.Usage, Iterations: 1, TimingProfile: &profile}, nil
	}
	loopRequest := *request
	if loopRequest.SteerMailbox == nil {
		loopRequest.SteerMailbox = r.steerMailbox
	}
	if len(loopRequest.Tools) == 0 {
		loopRequest.Tools = model.ResponsesToolsFromSpecs(r.router.ModelVisibleSpecs())
	}
	loopRequest.ClientMetadataTransform = newCodeModeClientMetadataTransform(loopRequest.ClientMetadata, r.router)
	loopRequest.ClientMetadata = loopRequest.ClientMetadataTransform(loopRequest.ClientMetadata)
	loopRequest.Tools = MergeHostedTools(MergeHostedTools(loopRequest.Tools, r.hostedTools), request.HostedTools)
	return NewAgentLoop(&AgentLoopOptions{
		Agent:        r.agent,
		SteerMailbox: r.steerMailbox,
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
		}),
		MaxTurns: r.maxTurns,
		Now:      r.now,
	}).Run(ctx, &loopRequest)
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
