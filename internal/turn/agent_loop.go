package turn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"codex_go/internal/codexapi"
	"codex_go/internal/model"
)

const DefaultAgentLoopMaxIterations = 8

type AgentLoopOptions struct {
	Agent        model.AgentRunner
	Dispatcher   *ToolDispatcher
	SteerMailbox *SteerMailbox
	MaxTurns     int
	Now          func() time.Time
}

type AgentLoop struct {
	agent        model.AgentRunner
	dispatcher   *ToolDispatcher
	steerMailbox *SteerMailbox
	maxTurns     int
	now          func() time.Time
}

type AgentLoopRequest struct {
	Prompt               string
	Instructions         string
	Model                string
	ProviderID           string
	TaskKind             model.AgentTaskKind
	ThreadID             string
	TurnID               string
	Originator           string
	InputItems           []any
	SteerMailbox         *SteerMailbox
	Tools                []any
	Store                bool
	PreviousResponseID   string
	ParallelToolCalls    bool
	ReasoningEffort      string
	ReasoningSummary     string
	ModelVerbosity       string
	IncludeTimingMetrics bool
	BetaFeaturesHeader   string
	ItemIDsEnabled       bool
	ServiceTier          string
	PromptCacheKey       string
	ClientMetadata       map[string]string
	AttestationProvider  codexapi.AttestationProvider
	OutputSchema         any
	PostToolInputItems   ToolPostExecutionInputItems
	Timing               *TimingState
}

type AgentLoopResult struct {
	Response       *model.AgentResponse
	Responses      []*model.AgentResponse
	ToolExecutions []ToolExecutionResult
	InputItems     []any
	Usage          model.AgentUsage
	Iterations     int
	TimingProfile  *Profile
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
	maxTurns := options.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultAgentLoopMaxIterations
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &AgentLoop{
		agent:        options.Agent,
		dispatcher:   options.Dispatcher,
		steerMailbox: options.SteerMailbox,
		maxTurns:     maxTurns,
		now:          now,
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
	for iteration := 0; iteration < l.maxTurns; iteration++ {
		if items := drainSteerInputItems(l.steerMailbox, request); len(items) > 0 {
			result.InputItems = append(result.InputItems, items...)
		}
		inputItems := append([]any(nil), result.InputItems...)
		if iteration > 0 && len(inputItems) == 0 {
			if userMessage := model.UserMessageInputItem(prompt); userMessage != nil {
				inputItems = append(inputItems, userMessage)
			}
		}
		sampling := timing.BeginSampling(l.now())
		response, err := l.agent.Run(ctx, &model.AgentRequest{
			Prompt:               prompt,
			Instructions:         request.Instructions,
			InputItems:           inputItems,
			Tools:                append([]any(nil), request.Tools...),
			Model:                request.Model,
			ProviderID:           request.ProviderID,
			TaskKind:             request.TaskKind,
			ThreadID:             request.ThreadID,
			TurnID:               request.TurnID,
			Originator:           request.Originator,
			Store:                request.Store,
			PreviousResponseID:   previousResponseID,
			ParallelToolCalls:    request.ParallelToolCalls,
			ReasoningEffort:      request.ReasoningEffort,
			ReasoningSummary:     request.ReasoningSummary,
			ModelVerbosity:       request.ModelVerbosity,
			IncludeTimingMetrics: request.IncludeTimingMetrics,
			BetaFeaturesHeader:   request.BetaFeaturesHeader,
			ItemIDsEnabled:       request.ItemIDsEnabled,
			ServiceTier:          request.ServiceTier,
			PromptCacheKey:       request.PromptCacheKey,
			ClientMetadata:       cloneStringMap(request.ClientMetadata),
			AttestationProvider:  request.AttestationProvider,
			OutputSchema:         request.OutputSchema,
			StreamHandler:        timingStreamHandler(timing, l.now),
		})
		sampling.CloseAt(l.now())
		if err != nil {
			return nil, err
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
	}
	return nil, fmt.Errorf("agent tool loop exceeded %d iterations", l.maxTurns)
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
	case "function_call", "custom_tool_call", "tool_search_call":
		return true
	default:
		return false
	}
}

func drainSteerInputItems(mailbox *SteerMailbox, request *AgentLoopRequest) []any {
	if mailbox == nil && request != nil {
		mailbox = request.SteerMailbox
	}
	if mailbox == nil || request == nil {
		return nil
	}
	return mailbox.Drain(&SteerDrainParams{ThreadID: request.ThreadID, TurnID: request.TurnID})
}

func addAgentUsage(left model.AgentUsage, right model.AgentUsage) model.AgentUsage {
	left.InputTokens += right.InputTokens
	left.CachedInputTokens += right.CachedInputTokens
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
