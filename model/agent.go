package model

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"codex_go/codexapi"
	"codex_go/sandbox"
)

var ErrInvalidAgentRequest = errors.New("invalid agent request")

type AgentTaskKind string

const (
	AgentTaskRegular AgentTaskKind = "regular"
	AgentTaskReview  AgentTaskKind = "review"
)

type AgentRequest struct {
	Prompt                       string
	Instructions                 string
	InputItems                   []any
	Tools                        []any
	Model                        string
	ProviderID                   string
	TaskKind                     AgentTaskKind
	ThreadID                     string
	TurnID                       string
	Originator                   string
	Store                        bool
	PreviousResponseID           string
	ParallelToolCalls            bool
	ReasoningEffort              string
	ReasoningSummary             string
	ConcurrentReasoningSummaries bool
	ModelVerbosity               string
	IncludeTimingMetrics         bool
	BetaFeaturesHeader           string
	ItemIDsEnabled               bool
	ServiceTier                  string
	PromptCacheKey               string
	ClientMetadata               map[string]string
	AttestationProvider          codexapi.AttestationProvider
	OutputSchema                 any
	StreamHandler                ResponsesStreamHandler
	DisableHostedImageGeneration bool
	PermissionProfile            *sandbox.PermissionProfile
}

type AgentUsage struct {
	InputTokens           int64
	CachedInputTokens     int64
	CacheWriteInputTokens int64
	OutputTokens          int64
	ReasoningOutputTokens int64
	TotalTokens           int64

	// CodexRolloutBudgetUnits is the provider-reported number of units
	// consumed from the shared rollout budget. It is an internal-only value
	// parsed from the completed Responses API usage and must not be serialized
	// into protocol, JSON schema, or TypeScript representations (mirrors Rust
	// TokenUsage.codex_rollout_budget_units with skip_serializing/skip).
	CodexRolloutBudgetUnits json.Number `json:"-"`
}

type AgentItem struct {
	ID                    string
	Type                  string
	Text                  string
	Name                  string
	Namespace             string
	CallID                string
	Arguments             string
	EncryptedFunctionArgs *[]string `json:"encrypted_function_args,omitempty"`
	Input                 string
	Status                string
	Execution             string
	Search                map[string]any
	Data                  map[string]any

	// Locally observed metadata is intentionally private so JSON input cannot
	// forge warehouse-only executed-tool records.
	executedToolCalls []ExecutedToolCall
}

func (i *AgentItem) MarshalJSON() ([]byte, error) {
	if i == nil {
		return []byte("null"), nil
	}
	switch i.Type {
	case "function_call":
		return marshalAgentItemWithExecutedToolCalls(i, struct {
			ID                    string    `json:"id,omitempty"`
			Type                  string    `json:"type"`
			Name                  string    `json:"name"`
			Namespace             string    `json:"namespace,omitempty"`
			Arguments             string    `json:"arguments"`
			EncryptedFunctionArgs *[]string `json:"encrypted_function_args,omitempty"`
			CallID                string    `json:"call_id"`
		}{
			ID:                    i.ID,
			Type:                  i.Type,
			Name:                  i.Name,
			Namespace:             i.Namespace,
			Arguments:             i.Arguments,
			EncryptedFunctionArgs: cloneAgentStringSlicePtr(i.EncryptedFunctionArgs),
			CallID:                firstAgentItemValue(i.CallID, i.ID),
		})
	case "custom_tool_call":
		return marshalAgentItemWithExecutedToolCalls(i, struct {
			ID        string `json:"id,omitempty"`
			Type      string `json:"type"`
			Status    string `json:"status,omitempty"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Namespace string `json:"namespace,omitempty"`
			Input     string `json:"input"`
		}{
			ID:        i.ID,
			Type:      i.Type,
			Status:    i.Status,
			CallID:    firstAgentItemValue(i.CallID, i.ID),
			Name:      i.Name,
			Namespace: i.Namespace,
			Input:     i.Input,
		})
	case "tool_search_call":
		return marshalAgentItemWithExecutedToolCalls(i, struct {
			ID        string  `json:"id,omitempty"`
			Type      string  `json:"type"`
			CallID    *string `json:"call_id"`
			Status    string  `json:"status,omitempty"`
			Execution string  `json:"execution"`
			Arguments any     `json:"arguments"`
		}{
			ID:        i.ID,
			Type:      i.Type,
			CallID:    optionalAgentItemString(firstAgentItemValue(i.CallID, i.ID)),
			Status:    i.Status,
			Execution: firstAgentItemValue(i.Execution, "client"),
			Arguments: agentItemSearchArguments(i),
		})
	case "web_search_call":
		return marshalAgentItemWithExecutedToolCalls(i, struct {
			ID     string         `json:"id,omitempty"`
			Type   string         `json:"type"`
			Status string         `json:"status,omitempty"`
			Action map[string]any `json:"action,omitempty"`
		}{
			ID:     i.ID,
			Type:   i.Type,
			Status: i.Status,
			Action: cloneAgentItemMap(i.Search),
		})
	case "reasoning":
		return marshalAgentItemWithExecutedToolCalls(i, struct {
			ID               string                          `json:"id,omitempty"`
			Type             string                          `json:"type"`
			Summary          []responsesReasoningSummary     `json:"summary"`
			Content          []responsesReasoningTextContent `json:"content,omitempty"`
			EncryptedContent *string                         `json:"encrypted_content"`
		}{
			ID:               i.ID,
			Type:             "reasoning",
			Summary:          agentReasoningSummary(i.Data),
			Content:          agentReasoningContent(i.Data),
			EncryptedContent: agentReasoningEncryptedContent(i.Data),
		})
	case "image_generation_call":
		return marshalAgentItemWithExecutedToolCalls(i, struct {
			ID            string `json:"id,omitempty"`
			Type          string `json:"type"`
			Status        string `json:"status"`
			RevisedPrompt string `json:"revised_prompt,omitempty"`
			Result        string `json:"result"`
		}{
			ID:            i.ID,
			Type:          "image_generation_call",
			Status:        firstAgentItemValue(i.Status, stringValueFromAgentItemMap(i.Data, "status")),
			RevisedPrompt: stringValueFromAgentItemMap(i.Data, "revised_prompt", "revisedPrompt"),
			Result:        firstAgentItemValue(stringValueFromAgentItemMap(i.Data, "result"), i.Text),
		})
	case "", "agent_message":
		phase := stringValueFromAgentItemMap(i.Data, "phase", "messagePhase", "message_phase")
		return marshalAgentItemWithExecutedToolCalls(i, struct {
			ID      string                       `json:"id,omitempty"`
			Type    string                       `json:"type"`
			Role    string                       `json:"role"`
			Phase   string                       `json:"phase,omitempty"`
			Content []responsesAgentContentBlock `json:"content"`
		}{
			ID:    i.ID,
			Type:  "message",
			Role:  "assistant",
			Phase: phase,
			Content: []responsesAgentContentBlock{{
				Type: "output_text",
				Text: i.Text,
			}},
		})
	default:
		return marshalAgentItemWithExecutedToolCalls(i, struct {
			ID   string `json:"id,omitempty"`
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		}{ID: i.ID, Type: i.Type, Text: i.Text})
	}
}

func cloneAgentStringSlicePtr(value *[]string) *[]string {
	if value == nil {
		return nil
	}
	cloned := append([]string{}, (*value)...)
	return &cloned
}

func marshalAgentItemWithExecutedToolCalls(item *AgentItem, value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil || item == nil || len(item.executedToolCalls) == 0 {
		return encoded, err
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, err
	}
	object[internalChatMessageMetadataPassthroughField] = map[string]any{
		executedToolCallsField: item.executedToolCalls,
	}
	return json.Marshal(object)
}

type responsesReasoningSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesReasoningTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type AgentResponse struct {
	ResponseID        string
	Message           string
	Items             []AgentItem
	Usage             AgentUsage
	Model             string
	ProviderID        string
	RequestID         string
	ServerModel       string
	TurnState         string
	ModelsETag        string
	Headers           map[string]string
	RateLimits        []ResponsesRateLimitSnapshot
	ReasoningIncluded *bool
	TimingMetrics     map[string]any
}

func AgentResponseMetadata(response *AgentResponse) map[string]any {
	metadata := map[string]any{}
	if response == nil {
		return metadata
	}
	if strings.TrimSpace(response.Model) != "" {
		metadata["model"] = response.Model
	}
	if strings.TrimSpace(response.ProviderID) != "" {
		metadata["providerId"] = response.ProviderID
		metadata["provider_id"] = response.ProviderID
	}
	if strings.TrimSpace(response.ResponseID) != "" {
		metadata["responseId"] = response.ResponseID
		metadata["response_id"] = response.ResponseID
	}
	if strings.TrimSpace(response.RequestID) != "" {
		metadata["requestId"] = response.RequestID
		metadata["request_id"] = response.RequestID
	}
	if strings.TrimSpace(response.ServerModel) != "" {
		metadata["serverModel"] = response.ServerModel
		metadata["server_model"] = response.ServerModel
	}
	if strings.TrimSpace(response.TurnState) != "" {
		metadata["turnState"] = response.TurnState
		metadata["turn_state"] = response.TurnState
	}
	if strings.TrimSpace(response.ModelsETag) != "" {
		metadata["modelsETag"] = response.ModelsETag
		metadata["models_etag"] = response.ModelsETag
	}
	if len(response.Headers) > 0 {
		headers := cloneStringMap(response.Headers)
		metadata["responseHeaders"] = headers
		metadata["response_headers"] = cloneStringMap(headers)
	}
	if len(response.RateLimits) > 0 {
		rateLimits := append([]ResponsesRateLimitSnapshot(nil), response.RateLimits...)
		metadata["rateLimits"] = rateLimits
		metadata["rate_limits"] = append([]ResponsesRateLimitSnapshot(nil), rateLimits...)
	}
	if response.ReasoningIncluded != nil {
		metadata["reasoningIncluded"] = *response.ReasoningIncluded
		metadata["reasoning_included"] = *response.ReasoningIncluded
	}
	if len(response.TimingMetrics) > 0 {
		metadata["timingMetrics"] = cloneMapAny(response.TimingMetrics)
		metadata["timing_metrics"] = cloneMapAny(response.TimingMetrics)
	}
	return metadata
}

type AgentRunner interface {
	Run(ctx context.Context, request *AgentRequest) (*AgentResponse, error)
}

type UnavailableAgentRunner struct{ Err error }

func (r *UnavailableAgentRunner) Run(ctx context.Context, request *AgentRequest) (*AgentResponse, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if r != nil && r.Err != nil {
		return nil, r.Err
	}
	return nil, errors.New("model-backed agent is unavailable")
}

type LocalAgentRunner struct{}

func NewLocalAgentRunner() *LocalAgentRunner {
	return &LocalAgentRunner{}
}

func (r *LocalAgentRunner) Run(ctx context.Context, request *AgentRequest) (*AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, ErrInvalidAgentRequest
	}
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}
	message := LocalAgentMessage(prompt)
	itemID := "agent-message-1"
	responseID := "resp-local"
	if request.TurnID != "" {
		itemID = "agent-message-" + strings.TrimPrefix(request.TurnID, "turn-")
		responseID = "resp-" + request.TurnID
	}
	inputTokens := estimateTokens(prompt)
	outputTokens := estimateTokens(message)
	return &AgentResponse{
		ResponseID: responseID,
		Message:    message,
		Items: []AgentItem{{
			ID:   itemID,
			Type: "agent_message",
			Text: message,
		}},
		Usage: AgentUsage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  inputTokens + outputTokens,
		},
		Model:      request.Model,
		ProviderID: request.ProviderID,
	}, nil
}

func AgentUsageTotalTokens(usage AgentUsage) int64 {
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.InputTokens + usage.OutputTokens
}

func LocalAgentMessage(prompt string) string {
	firstLine := strings.TrimSpace(prompt)
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = strings.TrimSpace(firstLine[:idx])
	}
	if firstLine == "" {
		firstLine = "prompt"
	}
	return "Go Codex exec stub received: " + firstLine
}

func estimateTokens(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	return int64(len(strings.Fields(value)))
}

func firstAgentItemValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func optionalAgentItemString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func stringValueFromAgentItemMap(values map[string]any, keys ...string) string {
	if values == nil {
		return ""
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		}
	}
	return ""
}

func cloneAgentItemMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func agentItemSearchArguments(item *AgentItem) map[string]any {
	if item == nil {
		return nil
	}
	if item.Search != nil {
		return cloneAgentItemMap(item.Search)
	}
	if strings.TrimSpace(item.Arguments) == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(item.Arguments), &out); err != nil {
		return nil
	}
	return out
}

func agentReasoningSummary(data map[string]any) []responsesReasoningSummary {
	values := agentStringSliceFromData(data, "summary")
	out := make([]responsesReasoningSummary, 0, len(values))
	for _, value := range values {
		out = append(out, responsesReasoningSummary{Type: "summary_text", Text: value})
	}
	if out == nil {
		return []responsesReasoningSummary{}
	}
	return out
}

func agentReasoningContent(data map[string]any) []responsesReasoningTextContent {
	values := agentStringSliceFromData(data, "reasoningContent", "content")
	out := make([]responsesReasoningTextContent, 0, len(values))
	for _, value := range values {
		out = append(out, responsesReasoningTextContent{Type: "reasoning_text", Text: value})
	}
	return out
}

func agentReasoningEncryptedContent(data map[string]any) *string {
	for _, key := range []string{"encrypted_content", "encryptedContent"} {
		if value, ok := data[key].(string); ok {
			return &value
		}
	}
	return nil
}

func agentStringSliceFromData(data map[string]any, keys ...string) []string {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []string:
			return append([]string(nil), typed...)
		case []any:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if text, ok := item.(string); ok {
					out = append(out, text)
				}
			}
			return out
		case string:
			if strings.TrimSpace(typed) != "" {
				return []string{typed}
			}
		}
	}
	return nil
}
