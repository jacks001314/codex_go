package turn

import (
	"context"
	"errors"
	"strings"
	"time"

	"codex_go/codexapi"
	"codex_go/model"
)

type AgentLoopOptions struct {
	Agent             model.AgentRunner
	Dispatcher        *ToolDispatcher
	SteerMailbox      *SteerMailbox
	MaxTurns          int
	Now               func() time.Time
	ExecutedToolCalls *ExecutedToolCallRecorder
}

type SamplingFollowUpContext struct {
	Response     *model.AgentResponse
	Usage        model.AgentUsage
	Iteration    int
	HasToolCalls bool
}

type SamplingFollowUp func(*SamplingFollowUpContext) []any
type AssistantMessageCallback func(response *model.AgentResponse, iteration int, hasToolCalls bool)
type ClientMetadataTransform func(map[string]string) map[string]string
type WarningCallback func(message string)

type AgentLoop struct {
	agent             model.AgentRunner
	dispatcher        *ToolDispatcher
	steerMailbox      *SteerMailbox
	now               func() time.Time
	executedToolCalls *ExecutedToolCallRecorder
}

type AgentLoopRequest struct {
	Prompt                          string
	Instructions                    string
	Model                           string
	ToolMode                        string
	DisableCodeModeFallback         bool
	ProviderID                      string
	TaskKind                        model.AgentTaskKind
	ThreadID                        string
	TurnID                          string
	Originator                      string
	InputItems                      []any
	SteerMailbox                    *SteerMailbox
	Tools                           []any
	HostedTools                     []any
	Store                           bool
	PreviousResponseID              string
	ParallelToolCalls               bool
	ReasoningEffort                 string
	ReasoningSummary                string
	ConcurrentReasoningSummaries    bool
	ModelVerbosity                  string
	IncludeTimingMetrics            bool
	BetaFeaturesHeader              string
	ItemIDsEnabled                  bool
	ServiceTier                     string
	PromptCacheKey                  string
	ClientMetadata                  map[string]string
	ClientMetadataTransform         ClientMetadataTransform
	AttestationProvider             codexapi.AttestationProvider
	OutputSchema                    any
	DisableHostedImageGeneration    bool
	PostToolInputItems              ToolPostExecutionInputItems
	OnToolStarted                   ToolStartedCallback
	OnToolCompleted                 ToolCompletedCallback
	EmitCodeModeNestedLifecycle     bool
	OnCodeModeNotify                CodeModeNotifyCallback
	OnWarning                       WarningCallback
	Timing                          *TimingState
	SamplingFollowUp                SamplingFollowUp
	OnAssistantMessage              AssistantMessageCallback
	StreamHandler                   model.ResponsesStreamHandler
	ExecutedToolCallMetadataEnabled bool
}

type AgentLoopResult struct {
	Response          *model.AgentResponse
	Responses         []*model.AgentResponse
	ToolExecutions    []ToolExecutionResult
	InputItems        []any
	Usage             model.AgentUsage
	Iterations        int
	TimingProfile     *Profile
	SamplingFollowUps int
}

func (r *AgentLoopResult) ModelResponses() []*model.AgentResponse {
	if r == nil {
		return nil
	}
	out := make([]*model.AgentResponse, 0, len(r.Responses)+1)
	seen := map[*model.AgentResponse]struct{}{}
	for _, response := range r.Responses {
		if response == nil {
			continue
		}
		if _, ok := seen[response]; ok {
			continue
		}
		seen[response] = struct{}{}
		out = append(out, response)
	}
	if r.Response != nil {
		if _, ok := seen[r.Response]; !ok {
			out = append(out, r.Response)
		}
	}
	return out
}

func NewAgentLoop(options *AgentLoopOptions) *AgentLoop {
	if options == nil {
		options = &AgentLoopOptions{}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &AgentLoop{
		agent:             options.Agent,
		dispatcher:        options.Dispatcher,
		steerMailbox:      options.SteerMailbox,
		now:               now,
		executedToolCalls: options.ExecutedToolCalls,
	}
}

func (l *AgentLoop) Run(ctx context.Context, request *AgentLoopRequest) (*AgentLoopResult, error) {
	if l == nil || l.agent == nil {
		return nil, errors.New("agent loop runner is nil")
	}
	if request == nil {
		return nil, model.ErrInvalidAgentRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := &AgentLoopResult{
		InputItems: append([]any(nil), request.InputItems...),
	}
	timing := request.Timing
	if timing == nil {
		timing = NewTimingState()
	}
	timing.MarkTurnStarted(l.now())
	prompt := strings.TrimSpace(request.Prompt)
	promptAppended := false
	previousResponseID := strings.TrimSpace(request.PreviousResponseID)
	clientMetadata := transformClientMetadata(request.ClientMetadata, request.ClientMetadataTransform)
	for iteration := 0; ; iteration++ {
		if steer := drainSteer(l.steerMailbox, request); steer != nil {
			if len(steer.InputItems) > 0 {
				result.InputItems = append(result.InputItems, steer.InputItems...)
			}
			if len(steer.ClientMetadata) > 0 {
				clientMetadata = transformClientMetadata(steer.ClientMetadata, request.ClientMetadataTransform)
			}
		}
		inputItems := append([]any(nil), result.InputItems...)
		if iteration > 0 && len(inputItems) == 0 {
			if userMessage := model.UserMessageInputItem(prompt); userMessage != nil {
				inputItems = append(inputItems, userMessage)
			}
		}
		var executedToolCallAttachment *ExecutedToolCallAttachment
		if l.executedToolCalls != nil {
			inputItems, executedToolCallAttachment = l.executedToolCalls.AttachPendingToPrompt(inputItems)
		}
		sampling := timing.BeginSampling(l.now())
		response, err := l.agent.Run(ctx, &model.AgentRequest{
			Prompt:                       prompt,
			Instructions:                 request.Instructions,
			InputItems:                   inputItems,
			Tools:                        append([]any(nil), request.Tools...),
			Model:                        request.Model,
			ProviderID:                   request.ProviderID,
			TaskKind:                     request.TaskKind,
			ThreadID:                     request.ThreadID,
			TurnID:                       request.TurnID,
			Originator:                   request.Originator,
			Store:                        request.Store,
			PreviousResponseID:           previousResponseID,
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
			StreamHandler:                combineResponsesStreamHandlers(request.StreamHandler, timingStreamHandler(timing, l.now)),
		})
		sampling.CloseAt(l.now())
		if err != nil {
			return nil, err
		}
		if l.executedToolCalls != nil {
			l.executedToolCalls.CommitAttachment(executedToolCallAttachment)
		}
		recordResponseTiming(timing, response, l.now())
		result.Response = response
		result.Responses = append(result.Responses, response)
		result.Usage = addAgentUsage(result.Usage, response.Usage)
		result.Iterations = iteration + 1
		if responseID := strings.TrimSpace(response.ResponseID); responseID != "" {
			previousResponseID = responseID
		}
		if prompt != "" && !promptAppended {
			if userMessage := model.UserMessageInputItem(prompt); userMessage != nil {
				result.InputItems = append(result.InputItems, userMessage)
				promptAppended = true
			}
		}
		prompt = ""
		for i := range response.Items {
			if !isToolAgentItem(&response.Items[i]) {
				item := response.Items[i]
				result.InputItems = append(result.InputItems, &item)
			}
		}

		toolItems := toolAgentItems(response)
		if request.OnAssistantMessage != nil && responseHasAssistantMessage(response) {
			request.OnAssistantMessage(response, iteration, len(toolItems) > 0)
		}
		if len(toolItems) == 0 && request.SamplingFollowUp != nil {
			followUp := request.SamplingFollowUp(&SamplingFollowUpContext{Response: response, Usage: result.Usage, Iteration: iteration, HasToolCalls: len(toolItems) > 0})
			if len(followUp) > 0 {
				result.InputItems = append(result.InputItems, followUp...)
				result.SamplingFollowUps++
				continue
			}
		}
		if len(toolItems) == 0 {
			profile := timing.CompleteProfile(l.now())
			result.TimingProfile = &profile
			return result, nil
		}
		if l.dispatcher == nil {
			return nil, errors.New("agent requested tool calls but tool dispatcher is nil")
		}
		for i := range toolItems {
			item := toolItems[i]
			result.InputItems = append(result.InputItems, &item)
		}
		toolBlocking := timing.BeginToolBlocking(l.now())
		executions, err := l.dispatcher.ExecuteToolItems(ctx, toolItems)
		toolBlocking.CloseAt(l.now())
		if err != nil {
			return nil, err
		}
		result.ToolExecutions = append(result.ToolExecutions, executions...)
		for i := range executions {
			if executions[i].Response != nil {
				result.InputItems = append(result.InputItems, executions[i].Response)
			}
			if len(executions[i].InputItems) > 0 {
				result.InputItems = append(result.InputItems, executions[i].InputItems...)
			}
		}
		if request.SamplingFollowUp != nil {
			followUp := request.SamplingFollowUp(&SamplingFollowUpContext{Response: response, Usage: result.Usage, Iteration: iteration, HasToolCalls: true})
			if len(followUp) > 0 {
				result.InputItems = append(result.InputItems, followUp...)
				result.SamplingFollowUps++
			}
		}
	}
}

func transformClientMetadata(metadata map[string]string, transform ClientMetadataTransform) map[string]string {
	out := cloneStringMap(metadata)
	if transform != nil {
		out = transform(out)
	}
	return out
}

func combineResponsesStreamHandlers(handlers ...model.ResponsesStreamHandler) model.ResponsesStreamHandler {
	active := make([]model.ResponsesStreamHandler, 0, len(handlers))
	for _, handler := range handlers {
		if handler != nil {
			active = append(active, handler)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return func(event *model.ResponsesStreamEvent) {
		for _, handler := range active {
			handler(event)
		}
	}
}

func responseHasAssistantMessage(response *model.AgentResponse) bool {
	if response == nil {
		return false
	}
	if strings.TrimSpace(response.Message) != "" {
		return true
	}
	for i := range response.Items {
		itemType := strings.TrimSpace(response.Items[i].Type)
		if (itemType == "" || itemType == "message" || itemType == "agent_message") && strings.TrimSpace(response.Items[i].Text) != "" {
			return true
		}
	}
	return false
}

func ResponseAssistantMessages(response *model.AgentResponse) []model.AgentItem {
	if response == nil {
		return nil
	}
	out := []model.AgentItem{}
	for i := range response.Items {
		item := response.Items[i]
		itemType := strings.TrimSpace(item.Type)
		if (itemType == "" || itemType == "message" || itemType == "agent_message") && strings.TrimSpace(item.Text) != "" {
			out = append(out, item)
		}
	}
	if len(out) == 0 && strings.TrimSpace(response.Message) != "" {
		out = append(out, model.AgentItem{Type: "agent_message", Text: response.Message})
	}
	return out
}

func recordResponseTiming(timing *TimingState, response *model.AgentResponse, now time.Time) {
	if timing == nil || response == nil {
		return
	}
	hasMessage := strings.TrimSpace(response.Message) != ""
	if !hasMessage {
		for i := range response.Items {
			if response.Items[i].Type == "" || response.Items[i].Type == "agent_message" {
				if strings.TrimSpace(response.Items[i].Text) != "" {
					hasMessage = true
					break
				}
			}
		}
	}
	if hasMessage {
		timing.RecordTTFT(now)
		timing.RecordTTFM(now)
	}
}

func timingStreamHandler(timing *TimingState, now func() time.Time) model.ResponsesStreamHandler {
	if timing == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return func(event *model.ResponsesStreamEvent) {
		if responseStreamEventRecordsTTFT(event) {
			timing.RecordTTFT(now())
		}
	}
}

func responseStreamEventRecordsTTFT(event *model.ResponsesStreamEvent) bool {
	if event == nil {
		return false
	}
	switch event.Kind {
	case model.ResponsesStreamEventOutputText,
		model.ResponsesStreamEventReasoningSummaryTextDelta,
		model.ResponsesStreamEventReasoningTextDelta:
		return true
	case model.ResponsesStreamEventOutputAdded, model.ResponsesStreamEventOutputDone:
		return agentItemRecordsTTFT(event.Item)
	default:
		return false
	}
}

func agentItemRecordsTTFT(item *model.AgentItem) bool {
	if item == nil {
		return false
	}
	switch item.Type {
	case "", "message", "agent_message", "reasoning":
		return strings.TrimSpace(item.Text) != ""
	case "function_call", "custom_tool_call", "tool_search_call", "web_search_call":
		return true
	case "image_generation_call":
		return true
	default:
		return false
	}
}

func drainSteer(mailbox *SteerMailbox, request *AgentLoopRequest) *SteerDrainResult {
	if mailbox == nil && request != nil {
		mailbox = request.SteerMailbox
	}
	if mailbox == nil || request == nil {
		return nil
	}
	return mailbox.DrainWithMetadata(&SteerDrainParams{ThreadID: request.ThreadID, TurnID: request.TurnID})
}

func addAgentUsage(left model.AgentUsage, right model.AgentUsage) model.AgentUsage {
	left.InputTokens += right.InputTokens
	left.CachedInputTokens += right.CachedInputTokens
	left.CacheWriteInputTokens += right.CacheWriteInputTokens
	left.OutputTokens += right.OutputTokens
	left.ReasoningOutputTokens += right.ReasoningOutputTokens
	left.TotalTokens += model.AgentUsageTotalTokens(right)
	return left
}

func toolAgentItems(response *model.AgentResponse) []model.AgentItem {
	if response == nil {
		return nil
	}
	out := make([]model.AgentItem, 0, len(response.Items))
	for i := range response.Items {
		if isToolAgentItem(&response.Items[i]) {
			out = append(out, response.Items[i])
		}
	}
	return out
}

func isToolAgentItem(item *model.AgentItem) bool {
	if item == nil {
		return false
	}
	switch item.Type {
	case "function_call", "custom_tool_call", "tool_search_call":
		return true
	default:
		return false
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
