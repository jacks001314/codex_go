package model

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"codex_go/internal/auth"
	"codex_go/internal/codexapi"

	"github.com/klauspost/compress/zstd"
)

const defaultResponsesEndpoint = "https://api.openai.com/v1"
const defaultResponsesRetryBaseDelay = 200 * time.Millisecond
const responsesLiteHeader = "x-openai-internal-codex-responses-lite"
const responsesIncludeTimingMetricsHeader = codexapi.ClientResponsesAPIIncludeTimingMetricsHeader

type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

type ResponsesAgentOptions struct {
	Provider                 *APIProvider
	Auth                     *AuthHeaders
	HTTPClient               HTTPDoer
	ProviderID               string
	Stream                   bool
	StreamHandler            ResponsesStreamHandler
	CodexHome                string
	AuthSnapshot             *auth.AuthDotJSON
	AuthIssuer               string
	StoreOptions             *auth.StoreOptions
	ExternalAuthRefresh      ExternalAuthRefreshFunc
	AgentIdentity            *AgentIdentityOptions
	ModelsManager            ModelsManager
	EnableRequestCompression bool
	IncludeAttestation       bool
	AttestationProvider      codexapi.AttestationProvider
}

type ResponsesAgentRunner struct {
	Provider                 *APIProvider
	Auth                     *AuthHeaders
	HTTPClient               HTTPDoer
	ProviderID               string
	Stream                   bool
	StreamHandler            ResponsesStreamHandler
	CodexHome                string
	AuthSnapshot             *auth.AuthDotJSON
	AuthIssuer               string
	StoreOptions             *auth.StoreOptions
	ExternalAuthRefresh      ExternalAuthRefreshFunc
	AgentIdentity            *AgentIdentityOptions
	AgentIdentityTelemetry   *codexapi.AgentIdentityTelemetry
	ModelsManager            ModelsManager
	EnableRequestCompression bool
	IncludeAttestation       bool
	AttestationProvider      codexapi.AttestationProvider
	providerAuthFetchedAt    time.Time
	turnState                string
	agentIdentityTried       bool
	agentIdentityBypass      bool
}

type AgentIdentityOptions struct {
	Enabled                   bool
	ChatGPTBaseURL            string
	AuthAPIBaseURL            string
	ForcedChatGPTWorkspaceIDs []string
	SessionSource             string
	AgentVersion              string
}

type ExternalAuthRefreshReason string

const ExternalAuthRefreshUnauthorized ExternalAuthRefreshReason = "unauthorized"

type ExternalAuthRefreshRequest struct {
	Reason            ExternalAuthRefreshReason
	PreviousAccountID string
}

type ExternalAuthRefreshResponse struct {
	AccessToken      string
	ChatGPTAccountID string
	ChatGPTPlanType  *string
}

type ExternalAuthRefreshFunc func(ctx context.Context, request *ExternalAuthRefreshRequest) (*ExternalAuthRefreshResponse, error)

type responsesAgentRequest struct {
	Model                string              `json:"model"`
	Instructions         string              `json:"instructions,omitempty"`
	Input                []any               `json:"input"`
	Tools                []any               `json:"tools,omitempty"`
	ToolChoice           string              `json:"tool_choice,omitempty"`
	Stream               bool                `json:"stream"`
	Store                bool                `json:"store"`
	ParallelToolCalls    bool                `json:"parallel_tool_calls"`
	Reasoning            *responsesReasoning `json:"reasoning,omitempty"`
	Include              []string            `json:"include,omitempty"`
	ServiceTier          string              `json:"service_tier,omitempty"`
	PromptCacheKey       string              `json:"prompt_cache_key,omitempty"`
	ClientMetadata       map[string]string   `json:"client_metadata,omitempty"`
	Text                 *responsesTextParam `json:"text,omitempty"`
	UseResponsesLite     bool                `json:"-"`
	IncludeTimingMetrics bool                `json:"-"`
	BetaFeaturesHeader   string              `json:"-"`
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
	Context string `json:"context,omitempty"`
}

type responsesTextParam struct {
	Verbosity string               `json:"verbosity,omitempty"`
	Format    *responsesTextFormat `json:"format,omitempty"`
}

type responsesTextFormat struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Strict bool   `json:"strict"`
	Schema any    `json:"schema"`
}

type responsesInputMessage struct {
	Type    string                  `json:"type,omitempty"`
	Role    string                  `json:"role"`
	Content []responsesInputContent `json:"content"`
}

type responsesInputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesAgentAPIResponse struct {
	ID         string                      `json:"id"`
	Model      string                      `json:"model"`
	OutputText string                      `json:"output_text"`
	Output     []responsesAgentOutputItem  `json:"output"`
	Usage      responsesAgentAPIUsage      `json:"usage"`
	Error      *responsesAgentAPIErrorBody `json:"error,omitempty"`
}

type responsesAgentOutputItem struct {
	ID        string                       `json:"id"`
	Type      string                       `json:"type"`
	Role      string                       `json:"role"`
	Content   []responsesAgentContentBlock `json:"content"`
	Name      string                       `json:"name"`
	Namespace string                       `json:"namespace"`
	CallID    string                       `json:"call_id"`
	Arguments any                          `json:"arguments"`
	Input     string                       `json:"input"`
	Execution string                       `json:"execution"`
	Search    map[string]any               `json:"search"`
}

type responsesAgentContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesAgentAPIUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details,omitempty"`
}

type responsesAgentAPIErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

type ResponsesAPIError struct {
	StatusCode             int
	Message                string
	Body                   string
	RequestID              string
	CFRay                  string
	AuthorizationError     string
	AuthorizationErrorCode string
}

func (e *ResponsesAPIError) Error() string {
	if e == nil {
		return "responses API request failed"
	}
	parts := []string{fmt.Sprintf("responses API request failed with status %d: %s", e.StatusCode, e.Message)}
	if e.RequestID != "" {
		parts = append(parts, "request_id: "+e.RequestID)
	}
	if e.CFRay != "" {
		parts = append(parts, "cf_ray: "+e.CFRay)
	}
	if e.AuthorizationError != "" {
		parts = append(parts, "auth_error: "+e.AuthorizationError)
	}
	if e.AuthorizationErrorCode != "" {
		parts = append(parts, "auth_error_code: "+e.AuthorizationErrorCode)
	}
	return strings.Join(parts, ", ")
}

func NewResponsesAgentRunner(options *ResponsesAgentOptions) *ResponsesAgentRunner {
	if options == nil {
		options = &ResponsesAgentOptions{}
	}
	provider := options.Provider
	if provider == nil {
		provider = defaultAPIProvider()
	} else {
		provider = cloneAPIProvider(provider)
	}
	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	var providerAuthFetchedAt time.Time
	if provider.Auth != nil && options.Auth != nil {
		providerAuthFetchedAt = time.Now()
	}
	return &ResponsesAgentRunner{
		Provider:                 provider,
		Auth:                     cloneAuthHeaders(options.Auth),
		HTTPClient:               client,
		ProviderID:               strings.TrimSpace(options.ProviderID),
		Stream:                   options.Stream,
		StreamHandler:            options.StreamHandler,
		CodexHome:                strings.TrimSpace(options.CodexHome),
		AuthSnapshot:             cloneAuthSnapshot(options.AuthSnapshot),
		AuthIssuer:               strings.TrimSpace(options.AuthIssuer),
		StoreOptions:             cloneStoreOptions(options.StoreOptions),
		ExternalAuthRefresh:      options.ExternalAuthRefresh,
		AgentIdentity:            cloneAgentIdentityOptions(options.AgentIdentity),
		AgentIdentityTelemetry:   agentIdentityTelemetryFromAuthHeaders(options.Auth),
		ModelsManager:            options.ModelsManager,
		EnableRequestCompression: options.EnableRequestCompression,
		IncludeAttestation:       options.IncludeAttestation,
		AttestationProvider:      options.AttestationProvider,
		providerAuthFetchedAt:    providerAuthFetchedAt,
	}
}

func NewResponsesAgentRunnerFromRuntimeProvider(providerID string, runtimeProvider RuntimeProvider, httpClient HTTPDoer) (*ResponsesAgentRunner, error) {
	return NewResponsesAgentRunnerFromRuntimeProviderWithAuth(providerID, runtimeProvider, httpClient, "", nil)
}

func NewResponsesAgentRunnerFromRuntimeProviderWithAuth(providerID string, runtimeProvider RuntimeProvider, httpClient HTTPDoer, codexHome string, snapshot *auth.AuthDotJSON) (*ResponsesAgentRunner, error) {
	if runtimeProvider == nil {
		return nil, errors.New("runtime provider is nil")
	}
	apiProvider, err := runtimeProvider.APIProvider()
	if err != nil {
		return nil, err
	}
	authHeaders, err := runtimeProvider.APIAuth()
	if err != nil {
		return nil, err
	}
	return NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:           &apiProvider,
		Auth:               &authHeaders,
		HTTPClient:         httpClient,
		ProviderID:         providerID,
		CodexHome:          codexHome,
		AuthSnapshot:       snapshot,
		ModelsManager:      runtimeProvider.ModelsManager(nil),
		IncludeAttestation: runtimeProvider.SupportsAttestation(),
	}), nil
}

func (r *ResponsesAgentRunner) WithStreamHandler(handler ResponsesStreamHandler) *ResponsesAgentRunner {
	if r == nil {
		return nil
	}
	clone := *r
	clone.Stream = true
	clone.StreamHandler = handler
	clone.Auth = cloneAuthHeaders(r.Auth)
	clone.AuthSnapshot = cloneAuthSnapshot(r.AuthSnapshot)
	clone.StoreOptions = cloneStoreOptions(r.StoreOptions)
	clone.AgentIdentity = cloneAgentIdentityOptions(r.AgentIdentity)
	clone.AgentIdentityTelemetry = cloneAgentIdentityTelemetry(r.AgentIdentityTelemetry)
	clone.ModelsManager = r.ModelsManager
	clone.AttestationProvider = r.AttestationProvider
	return &clone
}

func (r *ResponsesAgentRunner) Run(ctx context.Context, request *AgentRequest) (*AgentResponse, error) {
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
	if prompt == "" && len(request.InputItems) == 0 {
		return nil, errors.New("prompt or input items are required")
	}
	if err := r.resolveAgentIdentityAuth(ctx); err != nil {
		return nil, err
	}
	modelID := strings.TrimSpace(request.Model)
	if modelID == "" {
		modelID = "gpt-5.5"
	}
	modelInfo := r.modelInfoForRequest(modelID)
	instructions := responsesInstructions(request)
	inputItems := responsesInputItems(request)
	tools := cloneAnySlice(request.Tools)
	parallelToolCalls := request.ParallelToolCalls && !modelInfo.UseResponsesLite
	if modelInfo.UseResponsesLite {
		inputItems = responsesLiteInputItems(inputItems, tools, instructions)
		instructions = ""
		tools = nil
	}
	// Only Responses WebSocket v2 supports previous_response_id. This HTTP/SSE
	// runner carries conversation context by sending full history in input.
	apiRequest := &responsesAgentRequest{
		Model:                modelID,
		Instructions:         instructions,
		Input:                inputItems,
		Tools:                tools,
		ToolChoice:           "auto",
		Stream:               r.Stream,
		Store:                request.Store,
		ParallelToolCalls:    parallelToolCalls,
		ServiceTier:          ServiceTierForRequest(&modelInfo, request.ServiceTier),
		PromptCacheKey:       strings.TrimSpace(request.PromptCacheKey),
		ClientMetadata:       cloneStringMap(request.ClientMetadata),
		Text:                 responsesTextParamForRequest(request.OutputSchema, request.ModelVerbosity, &modelInfo),
		UseResponsesLite:     modelInfo.UseResponsesLite,
		IncludeTimingMetrics: request.IncludeTimingMetrics,
		BetaFeaturesHeader:   strings.TrimSpace(request.BetaFeaturesHeader),
	}
	apiRequest.Reasoning = responsesReasoningParam(request, &modelInfo)
	if apiRequest.Reasoning != nil {
		apiRequest.Include = []string{"reasoning.encrypted_content"}
	}
	if apiRequest.Stream {
		return r.runStreaming(ctx, request, apiRequest)
	}
	httpResponse, err := r.doResponsesHTTPRequestWithRetry(ctx, request, apiRequest, "application/json", r.requestMaxRetries())
	if err != nil {
		return nil, err
	}
	defer httpResponse.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return nil, responsesHTTPError(r.providerName(), httpResponse.StatusCode, httpResponse.Header, responseBody)
	}
	r.rememberTurnStateFromHeaders(httpResponse.Header)
	var apiResponse responsesAgentAPIResponse
	if err := json.Unmarshal(responseBody, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode responses API response: %w", err)
	}
	return agentResponseFromResponses(&apiResponse, request, r.ProviderID, httpResponse.Header)
}

func responsesReasoningParam(request *AgentRequest, info *ModelInfo) *responsesReasoning {
	if info == nil {
		return nil
	}
	if !info.SupportsReasoningSummaries {
		return nil
	}
	effort := strings.TrimSpace(request.ReasoningEffort)
	if effort == "" {
		effort = strings.TrimSpace(info.DefaultReasoningLevel)
	}
	if effort == "ultra" {
		effort = "max"
	}
	summary := strings.TrimSpace(request.ReasoningSummary)
	if summary == "" {
		summary = strings.TrimSpace(info.DefaultReasoningSummary)
	}
	reasoning := &responsesReasoning{Effort: effort}
	if !strings.EqualFold(summary, "none") {
		reasoning.Summary = summary
	}
	if info.UseResponsesLite {
		reasoning.Context = "all_turns"
	}
	return reasoning
}

func (r *ResponsesAgentRunner) modelInfoForRequest(modelID string) ModelInfo {
	manager := ModelsManager(nil)
	if r != nil {
		manager = r.ModelsManager
	}
	if manager == nil {
		manager = NewStaticModelsManager(BundledModelsResponse())
	}
	return manager.GetModelInfo(strings.TrimSpace(modelID), nil)
}

func responsesTextParamForRequest(schema any, verbosity string, info *ModelInfo) *responsesTextParam {
	text := &responsesTextParam{}
	if info != nil && info.SupportVerbosity {
		text.Verbosity = strings.TrimSpace(verbosity)
		if text.Verbosity == "" {
			text.Verbosity = strings.TrimSpace(info.DefaultVerbosity)
		}
	}
	if schema == nil {
		if text.Verbosity == "" {
			return nil
		}
		return text
	}
	text.Format = &responsesTextFormat{
		Name:   "codex_output_schema",
		Type:   "json_schema",
		Strict: true,
		Schema: cloneAny(schema),
	}
	return text
}

func responsesTextParamForOutputSchema(schema any) *responsesTextParam {
	if schema == nil {
		return nil
	}
	return responsesTextParamForRequest(schema, "", nil)
}

func (r *ResponsesAgentRunner) newResponsesHTTPRequest(ctx context.Context, request *AgentRequest, apiRequest *responsesAgentRequest, accept string) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.ensureFreshProviderCommandAuth(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(apiRequest)
	if err != nil {
		return nil, err
	}
	requestBody := body
	contentEncoding := ""
	if r.shouldCompressResponsesRequest() {
		requestBody, err = zstdCompressResponsesBody(body)
		if err != nil {
			return nil, err
		}
		contentEncoding = "zstd"
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, r.responsesURL(), bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(accept) != "" {
		httpRequest.Header.Set("Accept", accept)
	}
	addResponsesLiteHeader(httpRequest.Header, apiRequest.UseResponsesLite)
	addIncludeTimingMetricsHeader(httpRequest.Header, apiRequest.IncludeTimingMetrics)
	addTurnStateHeader(httpRequest.Header, r.turnState)
	addBetaFeaturesHeader(httpRequest.Header, apiRequest.BetaFeaturesHeader)
	addOriginatorHeader(httpRequest.Header, requestOriginator(request))
	addCompatibilityMetadataHeaders(httpRequest.Header, apiRequest.ClientMetadata)
	addHeaders(httpRequest.Header, r.providerHeaders())
	if err := r.addAttestationHeader(ctx, httpRequest.Header, request); err != nil {
		return nil, err
	}
	if contentEncoding != "" {
		httpRequest.Header.Set("Content-Encoding", contentEncoding)
	}
	if r.Auth != nil {
		if err := r.Auth.Apply(ctx, httpRequest, requestBody); err != nil {
			return nil, err
		}
	}
	return httpRequest, nil
}

func (r *ResponsesAgentRunner) addAttestationHeader(ctx context.Context, headers http.Header, request *AgentRequest) error {
	if headers == nil || r == nil || !r.IncludeAttestation {
		return nil
	}
	provider := r.AttestationProvider
	if request != nil && request.AttestationProvider != nil {
		provider = request.AttestationProvider
	}
	if provider == nil {
		return nil
	}
	threadID := ""
	if request != nil {
		threadID = strings.TrimSpace(request.ThreadID)
	}
	value, ok, err := provider.HeaderForRequest(ctx, &codexapi.AttestationContext{ThreadID: threadID})
	if err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	if !ok || value == "" || strings.ContainsAny(value, "\r\n") {
		return nil
	}
	headers.Set(codexapi.AttestationHeader, value)
	return nil
}

func (r *ResponsesAgentRunner) shouldCompressResponsesRequest() bool {
	if r == nil || !r.EnableRequestCompression {
		return false
	}
	if r.providerName() != OpenAIProviderName {
		return false
	}
	return authSnapshotUsesCodexBackend(r.AuthSnapshot)
}

func authSnapshotUsesCodexBackend(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token", "agent-identity":
		return true
	default:
		return false
	}
}

func zstdCompressResponsesBody(body []byte) ([]byte, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		return nil, err
	}
	defer encoder.Close()
	return encoder.EncodeAll(body, nil), nil
}

func addResponsesLiteHeader(headers http.Header, useResponsesLite bool) {
	if headers == nil || !useResponsesLite {
		return
	}
	headers.Set(responsesLiteHeader, "true")
}

func addIncludeTimingMetricsHeader(headers http.Header, includeTimingMetrics bool) {
	if headers == nil || !includeTimingMetrics {
		return
	}
	headers.Set(responsesIncludeTimingMetricsHeader, "true")
}

func addTurnStateHeader(headers http.Header, turnState string) {
	turnState = strings.TrimSpace(turnState)
	if headers == nil || turnState == "" || strings.ContainsAny(turnState, "\r\n") {
		return
	}
	headers.Set(codexapi.ClientCodexTurnStateHeader, turnState)
}

func addBetaFeaturesHeader(headers http.Header, betaFeaturesHeader string) {
	betaFeaturesHeader = strings.TrimSpace(betaFeaturesHeader)
	if headers == nil || betaFeaturesHeader == "" || strings.ContainsAny(betaFeaturesHeader, "\r\n") {
		return
	}
	headers.Set(codexapi.ClientCodexBetaFeaturesHeader, betaFeaturesHeader)
}

func (r *ResponsesAgentRunner) doResponsesHTTPRequest(httpRequest *http.Request) (*http.Response, error) {
	client := r.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(httpRequest)
}

func (r *ResponsesAgentRunner) doResponsesHTTPRequestWithRetry(ctx context.Context, request *AgentRequest, apiRequest *responsesAgentRequest, accept string, maxRetries uint64) (*http.Response, error) {
	var lastErr error
	retryTooManyRequests := apiRequest != nil && apiRequest.Stream
	for attempt := uint64(0); attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		httpRequest, err := r.newResponsesHTTPRequest(ctx, request, apiRequest, accept)
		if err != nil {
			return nil, err
		}
		httpResponse, err := r.doResponsesHTTPRequest(httpRequest)
		if shouldRetryResponsesHTTPRequest(httpResponse, err, retryTooManyRequests) && attempt < maxRetries {
			lastErr = err
			if httpResponse != nil && httpResponse.StatusCode == http.StatusUnauthorized {
				_ = r.refreshAuthAfterUnauthorized(ctx)
			}
			delay := responsesRetryDelay(httpResponse, attempt+1)
			if httpResponse != nil && httpResponse.Body != nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(httpResponse.Body, 4<<10))
				_ = httpResponse.Body.Close()
			}
			if err := sleepWithContext(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		return httpResponse, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("responses API retry limit reached")
}

func responsesUserMessage(prompt string) responsesInputMessage {
	return responsesInputMessage{
		Type: "message",
		Role: "user",
		Content: []responsesInputContent{{
			Type: "input_text",
			Text: prompt,
		}},
	}
}

func responsesInputItems(request *AgentRequest) []any {
	if request == nil {
		return nil
	}
	items := make([]any, 0, 1+len(request.InputItems))
	for i := range request.InputItems {
		if request.InputItems[i] != nil {
			items = append(items, request.InputItems[i])
		}
	}
	if strings.TrimSpace(request.Prompt) != "" {
		items = append(items, responsesUserMessage(strings.TrimSpace(request.Prompt)))
	}
	if !request.Store && !request.ItemIDsEnabled {
		items = stripResponseInputItemIDs(items)
	}
	return items
}

func stripResponseInputItemIDs(items []any) []any {
	out := make([]any, 0, len(items))
	for i := range items {
		out = append(out, stripResponseInputItemID(items[i]))
	}
	return out
}

func stripResponseInputItemID(item any) any {
	switch typed := item.(type) {
	case *AgentItem:
		if typed == nil {
			return nil
		}
		clone := *typed
		clone.ID = ""
		clone.Data = cloneAgentItemMap(typed.Data)
		clone.Search = cloneAgentItemMap(typed.Search)
		return &clone
	case AgentItem:
		clone := typed
		clone.ID = ""
		clone.Data = cloneAgentItemMap(typed.Data)
		clone.Search = cloneAgentItemMap(typed.Search)
		return clone
	case map[string]any:
		clone := cloneMapAny(typed)
		delete(clone, "id")
		return clone
	default:
		return item
	}
}

func responsesLiteInputItems(inputItems []any, tools []any, instructions string) []any {
	if tools == nil {
		tools = []any{}
	}
	prefix := []any{map[string]any{
		"type":  "additional_tools",
		"role":  "developer",
		"tools": tools,
	}}
	if strings.TrimSpace(instructions) != "" {
		prefix = append(prefix, responsesInputMessage{
			Type: "message",
			Role: "developer",
			Content: []responsesInputContent{{
				Type: "input_text",
				Text: strings.TrimSpace(instructions),
			}},
		})
	}
	items := make([]any, 0, len(prefix)+len(inputItems))
	items = append(items, prefix...)
	for i := range inputItems {
		items = append(items, stripResponsesLiteImageDetails(inputItems[i]))
	}
	return items
}

func stripResponsesLiteImageDetails(value any) any {
	if value == nil {
		return nil
	}
	normalized, ok := normalizeResponsesInputValue(value)
	if !ok {
		normalized = cloneAny(value)
	}
	stripResponsesLiteImageDetailsInPlace(normalized)
	return normalized
}

func normalizeResponsesInputValue(value any) (any, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}

func stripResponsesLiteImageDetailsInPlace(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if itemType, _ := typed["type"].(string); itemType == "input_image" || itemType == "image" {
			delete(typed, "detail")
		}
		for _, child := range typed {
			stripResponsesLiteImageDetailsInPlace(child)
		}
	case []any:
		for i := range typed {
			stripResponsesLiteImageDetailsInPlace(typed[i])
		}
	}
}

func agentResponseFromResponses(apiResponse *responsesAgentAPIResponse, request *AgentRequest, providerID string, headers http.Header) (*AgentResponse, error) {
	if apiResponse == nil {
		return nil, errors.New("responses API response is nil")
	}
	items := make([]AgentItem, 0, len(apiResponse.Output))
	var messages []string
	for index, output := range apiResponse.Output {
		if item, ok := toolCallAgentItem(&output, index); ok {
			items = append(items, *item)
			continue
		}
		if output.Type != "" && output.Type != "message" {
			continue
		}
		if output.Role != "" && output.Role != "assistant" {
			continue
		}
		text := strings.TrimSpace(output.text())
		if text == "" {
			continue
		}
		id := output.ID
		if id == "" {
			id = fmt.Sprintf("agent-message-%d", index+1)
		}
		items = append(items, AgentItem{ID: id, Type: "agent_message", Text: text})
		messages = append(messages, text)
	}
	if len(items) > 0 && len(messages) == 0 {
		return applyResponsesHeaderMetadata(&AgentResponse{
			ResponseID: apiResponse.ID,
			Items:      items,
			Usage:      usageFromResponses(&apiResponse.Usage, ""),
			Model:      firstNonEmptyResponseValue(apiResponse.Model, requestModel(request)),
			ProviderID: firstNonEmptyResponseValue(providerID, requestProviderID(request)),
		}, headers), nil
	}
	if len(items) == 0 && strings.TrimSpace(apiResponse.OutputText) != "" {
		text := strings.TrimSpace(apiResponse.OutputText)
		id := apiResponse.ID
		if id == "" {
			id = "agent-message-1"
		}
		items = append(items, AgentItem{ID: id, Type: "agent_message", Text: text})
		messages = append(messages, text)
	}
	message := strings.TrimSpace(strings.Join(messages, "\n\n"))
	if message == "" {
		return nil, errors.New("responses API response did not contain assistant text")
	}
	return applyResponsesHeaderMetadata(&AgentResponse{
		ResponseID: apiResponse.ID,
		Message:    message,
		Items:      items,
		Usage:      usageFromResponses(&apiResponse.Usage, message),
		Model:      firstNonEmptyResponseValue(apiResponse.Model, requestModel(request)),
		ProviderID: firstNonEmptyResponseValue(providerID, requestProviderID(request)),
	}, headers), nil
}

func applyResponsesHeaderMetadata(response *AgentResponse, headers http.Header) *AgentResponse {
	if response == nil || len(headers) == 0 {
		return response
	}
	response.RequestID = responseHeaderValue(headers, responsesRequestIDHeader, responsesOAIRequestIDHeader)
	response.ServerModel = responseHeaderValue(headers, responsesOpenAIModelHeader, responsesXOpenAIModelHeader)
	response.TurnState = responseHeaderValue(headers, responsesCodexTurnStateHeader)
	response.ModelsETag = responseHeaderValue(headers, responsesModelsETagHeader)
	response.Headers = cloneResponseHeaders(headers)
	response.RateLimits = parseResponsesRateLimits(headers)
	if headerExists(headers, responsesReasoningHeader) {
		included := true
		response.ReasoningIncluded = &included
	}
	return response
}

func toolCallAgentItem(output *responsesAgentOutputItem, index int) (*AgentItem, bool) {
	if output == nil {
		return nil, false
	}
	switch output.Type {
	case "function_call", "custom_tool_call", "tool_search_call":
	default:
		return nil, false
	}
	id := output.ID
	if id == "" {
		id = fmt.Sprintf("%s-%d", output.Type, index+1)
	}
	callID := output.CallID
	if callID == "" {
		callID = id
	}
	item := &AgentItem{
		ID:        id,
		Type:      output.Type,
		Name:      output.Name,
		Namespace: output.Namespace,
		CallID:    callID,
		Arguments: responseArgumentsString(output.Arguments),
		Input:     output.Input,
		Execution: output.Execution,
		Search:    cloneResponseSearch(output.Search),
	}
	if item.Type == "tool_search_call" && len(item.Search) == 0 {
		item.Search = responseArgumentsMap(output.Arguments)
	}
	return item, true
}

func responseArgumentsString(arguments any) string {
	switch value := arguments.(type) {
	case nil:
		return ""
	case string:
		return value
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func responseArgumentsMap(arguments any) map[string]any {
	switch value := arguments.(type) {
	case map[string]any:
		return cloneResponseSearch(value)
	case nil:
		return nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil
		}
		return out
	}
}

func cloneResponseSearch(search map[string]any) map[string]any {
	if search == nil {
		return nil
	}
	out := make(map[string]any, len(search))
	for key, value := range search {
		out[key] = value
	}
	return out
}

func firstNonEmptyResponseValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func uniqueNonEmptyResponseValues(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func responsesInstructions(request *AgentRequest) string {
	if request != nil && strings.TrimSpace(request.Instructions) != "" {
		return strings.TrimSpace(request.Instructions)
	}
	return BaseInstructions
}

func requestModel(request *AgentRequest) string {
	if request == nil {
		return ""
	}
	return request.Model
}

func requestProviderID(request *AgentRequest) string {
	if request == nil {
		return ""
	}
	return request.ProviderID
}

func requestOriginator(request *AgentRequest) string {
	if request == nil {
		return ""
	}
	return strings.TrimSpace(request.Originator)
}

func addOriginatorHeader(headers http.Header, originator string) {
	originator = strings.TrimSpace(originator)
	if headers == nil || originator == "" || strings.ContainsAny(originator, "\r\n") {
		return
	}
	headers.Set("originator", originator)
}

func addCompatibilityMetadataHeaders(headers http.Header, metadata map[string]string) {
	if headers == nil || len(metadata) == 0 {
		return
	}
	for _, key := range []string{
		codexapi.ClientCodexWindowIDHeader,
		codexapi.ClientCodexTurnMetadataHeader,
		codexapi.ClientCodexParentThreadIDHeader,
		codexapi.ClientOpenAISubagentHeader,
	} {
		value := strings.TrimSpace(metadata[key])
		if value == "" || strings.ContainsAny(value, "\r\n") {
			continue
		}
		headers.Set(key, value)
	}
}

func (i *responsesAgentOutputItem) text() string {
	if i == nil {
		return ""
	}
	var parts []string
	for _, content := range i.Content {
		switch content.Type {
		case "output_text", "text", "":
			if strings.TrimSpace(content.Text) != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	return strings.Join(parts, "")
}

func usageFromResponses(usage *responsesAgentAPIUsage, fallbackText string) AgentUsage {
	if usage == nil {
		outputTokens := estimateTokens(fallbackText)
		return AgentUsage{OutputTokens: outputTokens, TotalTokens: outputTokens}
	}
	out := AgentUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
	}
	if usage.InputTokensDetails != nil {
		out.CachedInputTokens = usage.InputTokensDetails.CachedTokens
	}
	if usage.OutputTokensDetails != nil {
		out.ReasoningOutputTokens = usage.OutputTokensDetails.ReasoningTokens
	}
	if out.OutputTokens == 0 {
		out.OutputTokens = estimateTokens(fallbackText)
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = AgentUsageTotalTokens(out)
	}
	return out
}

func (r *ResponsesAgentRunner) responsesURL() string {
	provider := r.Provider
	if provider == nil {
		provider = &APIProvider{BaseURL: defaultResponsesEndpoint}
	}
	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultResponsesEndpoint
	}
	raw := baseURL + "/responses"
	if len(provider.QueryParams) == 0 {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	query := parsed.Query()
	for key, value := range provider.QueryParams {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (r *ResponsesAgentRunner) providerHeaders() http.Header {
	if r == nil || r.Provider == nil {
		return nil
	}
	return r.Provider.Headers
}

func (r *ResponsesAgentRunner) providerName() string {
	if r == nil || r.Provider == nil {
		return ""
	}
	return r.Provider.Name
}

func (r *ResponsesAgentRunner) authHeaders() http.Header {
	if r == nil || r.Auth == nil {
		return nil
	}
	return r.Auth.Headers
}

func responsesHTTPError(providerName string, statusCode int, headers http.Header, body []byte) error {
	var payload struct {
		Error *responsesAgentAPIErrorBody `json:"error"`
	}
	bodyText := strings.TrimSpace(string(body))
	message := bodyText
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
		message = strings.TrimSpace(payload.Error.Message)
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}
	message = mapProviderAPIErrorMessage(providerName, statusCode, bodyText, message)
	return &ResponsesAPIError{
		StatusCode:             statusCode,
		Message:                message,
		Body:                   bodyText,
		RequestID:              responseHeaderValue(headers, responsesRequestIDHeader, responsesOAIRequestIDHeader),
		CFRay:                  responseHeaderValue(headers, "cf-ray"),
		AuthorizationError:     responseHeaderValue(headers, "x-openai-authorization-error"),
		AuthorizationErrorCode: responseAuthorizationErrorCode(headers),
	}
}

const bedrockExpiredSignatureMessage = "Amazon Bedrock rejected the request because its AWS signature has expired. Refresh your AWS credentials and retry. If `AWS_BEARER_TOKEN_BEDROCK` is set, update or unset it, then restart Codex"

func mapProviderAPIErrorMessage(providerName string, statusCode int, bodyText string, message string) string {
	if providerName == AmazonBedrockProviderName && statusCode == http.StatusUnauthorized && strings.Contains(bodyText, "Signature expired:") {
		return bedrockExpiredSignatureMessage
	}
	return message
}

func responseAuthorizationErrorCode(headers http.Header) string {
	encoded := strings.TrimSpace(responseHeaderValue(headers, "x-error-json"))
	if encoded == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Error.Code)
}

func (r *ResponsesAgentRunner) requestMaxRetries() uint64 {
	if r == nil || r.Provider == nil {
		return DefaultRequestMaxRetries
	}
	return r.Provider.RequestMaxRetries
}

func (r *ResponsesAgentRunner) streamMaxRetries() uint64 {
	if r == nil || r.Provider == nil {
		return DefaultStreamMaxRetries
	}
	return r.Provider.StreamMaxRetries
}

func (r *ResponsesAgentRunner) streamIdleTimeout() time.Duration {
	if r == nil || r.Provider == nil || r.Provider.StreamIdleTimeout <= 0 {
		return time.Duration(DefaultStreamIdleTimeoutMS) * time.Millisecond
	}
	return r.Provider.StreamIdleTimeout
}

func shouldRetryResponsesHTTPRequest(response *http.Response, err error, retryTooManyRequests bool) bool {
	if err != nil {
		return true
	}
	if response == nil {
		return false
	}
	return response.StatusCode == http.StatusUnauthorized || (retryTooManyRequests && response.StatusCode == http.StatusTooManyRequests) || response.StatusCode >= 500
}

func (r *ResponsesAgentRunner) refreshProviderCommandAuth(ctx context.Context) error {
	if r == nil || r.Provider == nil || r.Provider.Auth == nil {
		return nil
	}
	headers, err := ResolveProviderCommandAuth(ctx, r.Provider.Auth)
	if err != nil {
		return err
	}
	r.Auth = &headers
	r.providerAuthFetchedAt = time.Now()
	return nil
}

func (r *ResponsesAgentRunner) ensureFreshProviderCommandAuth(ctx context.Context) error {
	if r == nil || r.Provider == nil || r.Provider.Auth == nil {
		return nil
	}
	if r.Auth == nil {
		return r.refreshProviderCommandAuth(ctx)
	}
	interval := providerAuthRefreshInterval(r.Provider.Auth)
	if interval <= 0 {
		return nil
	}
	if !r.providerAuthFetchedAt.IsZero() && time.Since(r.providerAuthFetchedAt) < interval {
		return nil
	}
	return r.refreshProviderCommandAuth(ctx)
}

func providerAuthRefreshInterval(info *ProviderAuthInfo) time.Duration {
	if info == nil || info.RefreshIntervalMS == 0 {
		return 0
	}
	return time.Duration(info.RefreshIntervalMS) * time.Millisecond
}

func (r *ResponsesAgentRunner) refreshAuthAfterUnauthorized(ctx context.Context) error {
	if err := r.refreshExternalChatGPTAuth(ctx); err == nil {
		return nil
	}
	if err := r.refreshManagedChatGPTAuth(ctx); err == nil {
		return nil
	}
	return r.refreshProviderCommandAuth(ctx)
}

func (r *ResponsesAgentRunner) resolveAgentIdentityAuth(ctx context.Context) error {
	if r == nil || r.agentIdentityBypass || r.agentIdentityTried || r.AgentIdentity == nil || !r.AgentIdentity.Enabled {
		return nil
	}
	r.agentIdentityTried = true
	if r.AuthSnapshot == nil || r.AuthSnapshot.Mode() != "chatgpt" {
		return nil
	}
	httpClient, _ := r.HTTPClient.(*http.Client)
	resolved, err := auth.BootstrapManagedAgentIdentity(ctx, &auth.AgentIdentityBootstrapOptions{
		CodexHome:                 r.CodexHome,
		AuthSnapshot:              r.AuthSnapshot,
		StoreOptions:              r.StoreOptions,
		HTTPClient:                httpClient,
		ChatGPTBaseURL:            r.AgentIdentity.ChatGPTBaseURL,
		AgentIdentityAuthAPIURL:   r.AgentIdentity.AuthAPIBaseURL,
		ForcedChatGPTWorkspaceIDs: append([]string(nil), r.AgentIdentity.ForcedChatGPTWorkspaceIDs...),
		SessionSource:             r.AgentIdentity.SessionSource,
		AgentVersion:              r.AgentIdentity.AgentVersion,
	})
	if err != nil {
		var bootstrapUnavailable *auth.AgentIdentityBootstrapUnavailableError
		if errors.As(err, &bootstrapUnavailable) {
			r.agentIdentityBypass = true
			return nil
		}
		return err
	}
	if resolved == nil {
		return nil
	}
	headers, err := AuthHeadersFromAuth(*resolved)
	if err != nil {
		return err
	}
	r.AuthSnapshot = cloneAuthSnapshot(resolved)
	r.Auth = &headers
	r.AgentIdentityTelemetry = agentIdentityTelemetryFromAuthHeaders(&headers)
	return nil
}

func (r *ResponsesAgentRunner) refreshExternalChatGPTAuth(ctx context.Context) error {
	if r == nil || r.AuthSnapshot == nil || r.AuthSnapshot.Mode() != "chatgptAuthTokens" {
		return errors.New("external chatgpt auth refresh is not available")
	}
	if r.ExternalAuthRefresh == nil {
		return errors.New("external chatgpt auth refresh callback is not configured")
	}
	response, err := r.ExternalAuthRefresh(ctx, &ExternalAuthRefreshRequest{
		Reason:            ExternalAuthRefreshUnauthorized,
		PreviousAccountID: accountIDFromMap(r.AuthSnapshot.Tokens),
	})
	if err != nil {
		return err
	}
	if response == nil || strings.TrimSpace(response.AccessToken) == "" || strings.TrimSpace(response.ChatGPTAccountID) == "" {
		return errors.New("external chatgpt auth refresh response omitted accessToken or chatgptAccountId")
	}
	snapshot := auth.FromChatGPTAuthTokens(response.AccessToken, response.ChatGPTAccountID, response.ChatGPTPlanType)
	headers, err := AuthHeadersFromAuth(snapshot)
	if err != nil {
		return err
	}
	r.AuthSnapshot = cloneAuthSnapshot(&snapshot)
	r.Auth = &headers
	r.AgentIdentityTelemetry = agentIdentityTelemetryFromAuthHeaders(&headers)
	return nil
}

func (r *ResponsesAgentRunner) refreshManagedChatGPTAuth(ctx context.Context) error {
	if r == nil || r.AuthSnapshot == nil || !authHasChatGPTAccount(r.AuthSnapshot) {
		return errors.New("chatgpt auth refresh is not available")
	}
	if r.AuthSnapshot.Mode() == "chatgptAuthTokens" {
		return errors.New("managed chatgpt auth refresh is not available for external tokens")
	}
	if strings.TrimSpace(r.CodexHome) == "" {
		return errors.New("codex home is required for chatgpt auth refresh")
	}
	httpClient, _ := r.HTTPClient.(*http.Client)
	refreshed, err := auth.RefreshChatGPTTokens(ctx, &auth.RefreshChatGPTTokenOptions{
		CodexHome:    r.CodexHome,
		Issuer:       r.AuthIssuer,
		HTTPClient:   httpClient,
		RefreshToken: stringFromAny(r.AuthSnapshot.Tokens, "refresh_token"),
		AuthSnapshot: r.AuthSnapshot,
		StoreOptions: r.StoreOptions,
	})
	if err != nil {
		return err
	}
	headers, err := AuthHeadersFromAuth(*refreshed)
	if err != nil {
		return err
	}
	r.AuthSnapshot = cloneAuthSnapshot(refreshed)
	r.Auth = &headers
	r.AgentIdentityTelemetry = agentIdentityTelemetryFromAuthHeaders(&headers)
	return nil
}

func responsesRetryDelay(response *http.Response, attempt uint64) time.Duration {
	if response != nil {
		if retryAfter := strings.TrimSpace(response.Header.Get("Retry-After")); retryAfter != "" {
			if seconds, err := strconv.ParseFloat(retryAfter, 64); err == nil && seconds >= 0 {
				return time.Duration(seconds * float64(time.Second))
			}
			if when, err := http.ParseTime(retryAfter); err == nil {
				delay := time.Until(when)
				if delay > 0 {
					return delay
				}
			}
		}
	}
	if attempt == 0 {
		attempt = 1
	}
	delay := defaultResponsesRetryBaseDelay
	for i := uint64(1); i < attempt; i++ {
		delay *= 2
		if delay > 5*time.Second {
			return 5 * time.Second
		}
	}
	return delay
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func addHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if strings.TrimSpace(key) == "" {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func cloneAPIProvider(provider *APIProvider) *APIProvider {
	if provider == nil {
		return nil
	}
	return normalizeAPIProvider(&APIProvider{
		Name:              provider.Name,
		BaseURL:           provider.BaseURL,
		QueryParams:       cloneStringMap(provider.QueryParams),
		Headers:           cloneHTTPHeader(provider.Headers),
		Auth:              cloneProviderAuthInfo(provider.Auth),
		RequestMaxRetries: provider.RequestMaxRetries,
		StreamMaxRetries:  provider.StreamMaxRetries,
		StreamIdleTimeout: provider.StreamIdleTimeout,
	})
}

func defaultAPIProvider() *APIProvider {
	return &APIProvider{
		Name:              OpenAIProviderName,
		BaseURL:           defaultResponsesEndpoint,
		RequestMaxRetries: DefaultRequestMaxRetries,
		StreamMaxRetries:  DefaultStreamMaxRetries,
		StreamIdleTimeout: time.Duration(DefaultStreamIdleTimeoutMS) * time.Millisecond,
	}
}

func normalizeAPIProvider(provider *APIProvider) *APIProvider {
	if provider == nil {
		return nil
	}
	out := *provider
	out.QueryParams = cloneStringMap(out.QueryParams)
	out.Headers = cloneHTTPHeader(out.Headers)
	if out.BaseURL == "" {
		out.BaseURL = defaultResponsesEndpoint
	}
	return &out
}

func cloneAuthHeaders(headers *AuthHeaders) *AuthHeaders {
	if headers == nil {
		return nil
	}
	return &AuthHeaders{
		Headers:                cloneHTTPHeader(headers.Headers),
		SignRequest:            headers.SignRequest,
		AgentIdentityTelemetry: cloneAgentIdentityTelemetry(headers.AgentIdentityTelemetry),
	}
}

func agentIdentityTelemetryFromAuthHeaders(headers *AuthHeaders) *codexapi.AgentIdentityTelemetry {
	if headers == nil {
		return nil
	}
	return cloneAgentIdentityTelemetry(headers.AgentIdentityTelemetry)
}

func cloneAgentIdentityTelemetry(value *codexapi.AgentIdentityTelemetry) *codexapi.AgentIdentityTelemetry {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneAuthSnapshot(snapshot *auth.AuthDotJSON) *auth.AuthDotJSON {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	if snapshot.Tokens != nil {
		clone.Tokens = cloneMapAny(snapshot.Tokens)
	}
	clone.AgentIdentity = cloneAgentIdentitySnapshot(snapshot.AgentIdentity)
	return &clone
}

func cloneAgentIdentitySnapshot(value any) any {
	switch record := value.(type) {
	case *auth.AgentIdentityAuthRecord:
		if record == nil {
			return nil
		}
		clone := *record
		clone.Email = cloneStringPtrModel(record.Email)
		clone.TaskID = cloneStringPtrModel(record.TaskID)
		return &clone
	case auth.AgentIdentityAuthRecord:
		clone := record
		clone.Email = cloneStringPtrModel(record.Email)
		clone.TaskID = cloneStringPtrModel(record.TaskID)
		return clone
	case map[string]any:
		return cloneMapAny(record)
	default:
		return value
	}
}

func cloneStringPtrModel(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneStoreOptions(options *auth.StoreOptions) *auth.StoreOptions {
	if options == nil {
		return nil
	}
	clone := *options
	return &clone
}

func cloneAgentIdentityOptions(options *AgentIdentityOptions) *AgentIdentityOptions {
	if options == nil {
		return nil
	}
	clone := *options
	clone.ForcedChatGPTWorkspaceIDs = append([]string(nil), options.ForcedChatGPTWorkspaceIDs...)
	return &clone
}

func cloneHTTPHeader(headers http.Header) http.Header {
	if headers == nil {
		return nil
	}
	out := http.Header{}
	for key, values := range headers {
		out[key] = append([]string(nil), values...)
	}
	return out
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
