package model

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/codexapi"
	"codex_go/eventmap"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

const defaultResponsesEndpoint = "https://api.openai.com/v1"
const defaultResponsesRetryBaseDelay = 200 * time.Millisecond
const responsesLiteHeader = "x-openai-internal-codex-responses-lite"

// ResidencyHeaderName is the managed residency header applied to model
// requests (Rust default_client.rs RESIDENCY_HEADER_NAME, #39645).
const ResidencyHeaderName = "x-openai-internal-codex-residency"
const responsesIncludeTimingMetricsHeader = codexapi.ClientResponsesAPIIncludeTimingMetricsHeader

// ResponsesEndpoint selects the Responses-compatible backend route used for an
// inference request (Rust codex_api::endpoint::ResponsesEndpoint, #40892).
type ResponsesEndpoint int

const (
	// ResponsesEndpointResponses is the standard user-owned model inference route.
	ResponsesEndpointResponses ResponsesEndpoint = iota
	// ResponsesEndpointGuardian routes a full Guardian approval-review agent.
	ResponsesEndpointGuardian
	// ResponsesEndpointGuardianClassifier routes lightweight asynchronous Guardian risk classification.
	ResponsesEndpointGuardianClassifier
)

// Path returns the provider-relative path for this inference surface.
func (e ResponsesEndpoint) Path() string {
	switch e {
	case ResponsesEndpointGuardian:
		return "/guardian"
	case ResponsesEndpointGuardianClassifier:
		return "/guardian-classifier"
	default:
		return "/responses"
	}
}

type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

func responsesRequestedRetryDelay(headers http.Header, now time.Time) (time.Duration, bool) {
	retryAfter := strings.TrimSpace(headers.Get("Retry-After"))
	if retryAfter == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseFloat(retryAfter, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds * float64(time.Second)), true
	}
	if when, err := http.ParseTime(retryAfter); err == nil {
		delay := when.Sub(now)
		if delay > 0 {
			return delay, true
		}
	}
	return 0, false
}

func (e *ResponsesAPIError) RequestedRetryDelay() (time.Duration, bool) {
	if e == nil {
		return 0, false
	}
	return e.retryDelay, e.hasRetryDelay
}

type ResponsesAgentOptions struct {
	Provider                   *APIProvider
	Auth                       *AuthHeaders
	HTTPClient                 HTTPDoer
	ProviderID                 string
	ProviderCapabilities       *ProviderCapabilities
	ProviderRequiresOpenAIAuth bool
	ProviderUsesOwnCredentials bool
	Stream                     bool
	StreamHandler              ResponsesStreamHandler
	CodexHome                  string
	AuthSnapshot               *auth.AuthDotJSON
	AuthIssuer                 string
	StoreOptions               *auth.StoreOptions
	ExternalAuthRefresh        ExternalAuthRefreshFunc
	// WorkloadIdentityRefresh refreshes process-scoped workload identity
	// after a downstream 401 (Rust WorkloadIdentityExternalAuth::refresh,
	// #38188). When nil, the runner lazily builds one from StoreOptions.
	WorkloadIdentityRefresh  WorkloadIdentityRefreshFunc
	AgentIdentity            *AgentIdentityOptions
	ModelsManager            ModelsManager
	EnableRequestCompression bool
	// ContentItemKindsEnabled mirrors Rust's content_item_kinds feature gate:
	// when disabled, content_item_kinds is cleared from every response item
	// carried to the provider (Rust client.rs prepare_response_items_for_request).
	// nil means enabled (the feature default is on), matching the Stable
	// default_enabled: true registration.
	ContentItemKindsEnabled *bool
	IncludeAttestation      bool
	AttestationProvider     codexapi.AttestationProvider
	SupportsWebsockets      bool
	WebsocketConnectTimeout time.Duration
	// UnboundedConnectionRetries keeps active sampling turns alive until a
	// failed network connection recovers (Rust Feature::UnboundedConnectionRetries,
	// default enabled). nil means the feature default (enabled).
	UnboundedConnectionRetries *bool
	// AWS carries the Amazon Bedrock SDK credential configuration when the
	// provider uses the AWS credential chain (Rust #39410).
	AWS *ProviderAWSAuthInfo
}

type ResponsesAgentRunner struct {
	Provider                   *APIProvider
	Auth                       *AuthHeaders
	HTTPClient                 HTTPDoer
	ProviderID                 string
	ProviderCapabilities       ProviderCapabilities
	ProviderRequiresOpenAIAuth bool
	ProviderUsesOwnCredentials bool
	Stream                     bool
	StreamHandler              ResponsesStreamHandler
	CodexHome                  string
	AuthSnapshot               *auth.AuthDotJSON
	AuthIssuer                 string
	StoreOptions               *auth.StoreOptions
	ExternalAuthRefresh        ExternalAuthRefreshFunc
	WorkloadIdentityRefresh    WorkloadIdentityRefreshFunc
	AgentIdentity              *AgentIdentityOptions
	AgentIdentityTelemetry     *codexapi.AgentIdentityTelemetry
	ModelsManager              ModelsManager
	EnableRequestCompression   bool
	ContentItemKindsEnabled    *bool
	IncludeAttestation         bool
	AttestationProvider        codexapi.AttestationProvider
	SupportsWebsockets         bool
	WebsocketConnectTimeout    time.Duration
	UnboundedConnectionRetries *bool
	// AWS carries the Amazon Bedrock SDK credential configuration (Rust
	// #39410) so expired SDK credentials can be refreshed on 401 failures.
	AWS *ProviderAWSAuthInfo
	// Residency, when set, is the managed residency requirement enforced as an
	// authoritative header on model requests (Rust #39645).
	Residency string
	// FreeGuardianEnabled, when set, allows eligible Guardian review inference
	// to route through the dedicated unmetered Codex endpoints (Rust
	// Config::free_guardian_enabled, #40892).
	FreeGuardianEnabled   bool
	providerAuthFetchedAt time.Time
	turnState             *responsesTurnStateCache
	websocketSessions     *responsesWebsocketSessionCache
	agentIdentityTried    bool
	agentIdentityBypass   bool
	// AWSRefreshMu and AWSRefreshInFlight share Bedrock AWS credential
	// refresh state across concurrent failures (Rust shared_state, #39410).
	AWSRefreshMu       sync.Mutex
	AWSRefreshInFlight chan struct{}
	// bedrockAuthRecoveryAttempted bounds provider-owned Bedrock auth recovery
	// to one attempt per request (Rust #39274).
	bedrockAuthRecoveryAttempted bool
}

type responsesTurnStateCache struct {
	mu     sync.Mutex
	turnID string
	value  string
}

type responsesWebsocketSessionCache struct {
	mu       sync.Mutex
	sessions map[string]*responsesWebsocketSession
	disabled bool
}

type responsesWebsocketSession struct {
	mu   sync.Mutex
	conn *websocket.Conn
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

// WorkloadIdentityRefreshFunc re-exchanges workload identity tokens preserving
// the previously authenticated account.
type WorkloadIdentityRefreshFunc func(ctx context.Context, previousAccountID string) (*auth.AuthDotJSON, error)

type responsesAgentRequest struct {
	Model                string                  `json:"model"`
	Instructions         string                  `json:"instructions,omitempty"`
	Input                []any                   `json:"input"`
	Tools                []any                   `json:"tools,omitempty"`
	ToolChoice           string                  `json:"tool_choice,omitempty"`
	Stream               bool                    `json:"stream"`
	Store                bool                    `json:"store"`
	ParallelToolCalls    bool                    `json:"parallel_tool_calls"`
	Reasoning            *responsesReasoning     `json:"reasoning,omitempty"`
	StreamOptions        *responsesStreamOptions `json:"stream_options,omitempty"`
	Include              []string                `json:"include,omitempty"`
	ServiceTier          string                  `json:"service_tier,omitempty"`
	PromptCacheKey       string                  `json:"prompt_cache_key,omitempty"`
	ClientMetadata       map[string]string       `json:"client_metadata,omitempty"`
	Text                 *responsesTextParam     `json:"text,omitempty"`
	UseResponsesLite     bool                    `json:"-"`
	IncludeTimingMetrics bool                    `json:"-"`
	BetaFeaturesHeader   string                  `json:"-"`
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
	Context string `json:"context,omitempty"`
}

type responsesStreamOptions struct {
	ReasoningSummaryDelivery string `json:"reasoning_summary_delivery"`
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
	ID      string                  `json:"id,omitempty"`
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
	ID                    string                       `json:"id"`
	Type                  string                       `json:"type"`
	Role                  string                       `json:"role"`
	Phase                 string                       `json:"phase"`
	Content               []responsesAgentContentBlock `json:"content"`
	Name                  string                       `json:"name"`
	Namespace             string                       `json:"namespace"`
	CallID                string                       `json:"call_id"`
	Arguments             any                          `json:"arguments"`
	EncryptedFunctionArgs *[]string                    `json:"encrypted_function_args"`
	Input                 string                       `json:"input"`
	Status                string                       `json:"status"`
	Revised               string                       `json:"revised_prompt"`
	Result                string                       `json:"result"`
	Execution             string                       `json:"execution"`
	Search                map[string]any               `json:"search"`
	Action                map[string]any               `json:"action"`
}

type responsesAgentContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesAgentAPIUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	// CodexRolloutBudgetUnits mirrors the Rust ResponseCompletedUsage field:
	// provider-reported units consumed from the shared rollout budget. It can
	// be a fractional number (e.g. 2.5), so json.Number preserves the literal.
	CodexRolloutBudgetUnits json.Number `json:"codex_rollout_budget_units"`
	InputTokensDetails      *struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details,omitempty"`
}

type responsesAgentAPIErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
	// Misalignment carries the optional public explanation and continuation
	// instruction returned with a misalignment block (Rust #40952).
	Misalignment json.RawMessage `json:"misalignment,omitempty"`
}

type ResponsesAPIError struct {
	StatusCode             int
	Message                string
	Body                   string
	RequestID              string
	CFRay                  string
	AuthorizationError     string
	AuthorizationErrorCode string
	retryDelay             time.Duration
	hasRetryDelay          bool
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
	providerCapabilities := DefaultProviderCapabilities()
	if options.ProviderCapabilities != nil {
		providerCapabilities = *options.ProviderCapabilities
	}
	providerRequiresOpenAIAuth := options.ProviderRequiresOpenAIAuth
	if !providerRequiresOpenAIAuth && provider != nil && strings.EqualFold(provider.Name, OpenAIProviderName) {
		providerRequiresOpenAIAuth = true
	}
	return &ResponsesAgentRunner{
		Provider:                   provider,
		Auth:                       cloneAuthHeaders(options.Auth),
		HTTPClient:                 client,
		ProviderID:                 strings.TrimSpace(options.ProviderID),
		ProviderCapabilities:       providerCapabilities,
		ProviderRequiresOpenAIAuth: providerRequiresOpenAIAuth,
		ProviderUsesOwnCredentials: options.ProviderUsesOwnCredentials || provider.Auth != nil,
		Stream:                     options.Stream,
		StreamHandler:              options.StreamHandler,
		CodexHome:                  strings.TrimSpace(options.CodexHome),
		AuthSnapshot:               cloneAuthSnapshot(options.AuthSnapshot),
		AuthIssuer:                 strings.TrimSpace(options.AuthIssuer),
		StoreOptions:               cloneStoreOptions(options.StoreOptions),
		ExternalAuthRefresh:        options.ExternalAuthRefresh,
		WorkloadIdentityRefresh:    options.WorkloadIdentityRefresh,
		AgentIdentity:              cloneAgentIdentityOptions(options.AgentIdentity),
		AgentIdentityTelemetry:     agentIdentityTelemetryFromAuthHeaders(options.Auth),
		ModelsManager:              options.ModelsManager,
		EnableRequestCompression:   options.EnableRequestCompression,
		ContentItemKindsEnabled:    cloneBoolPtrModel(options.ContentItemKindsEnabled),
		IncludeAttestation:         options.IncludeAttestation,
		AttestationProvider:        options.AttestationProvider,
		SupportsWebsockets:         options.SupportsWebsockets,
		WebsocketConnectTimeout:    options.WebsocketConnectTimeout,
		UnboundedConnectionRetries: cloneBoolPtrModel(options.UnboundedConnectionRetries),
		AWS:                        cloneProviderAWSAuthInfo(options.AWS),
		providerAuthFetchedAt:      providerAuthFetchedAt,
		turnState:                  &responsesTurnStateCache{},
		websocketSessions:          &responsesWebsocketSessionCache{sessions: map[string]*responsesWebsocketSession{}},
	}
}

func (r *ResponsesAgentRunner) websocketSession(request *AgentRequest) *responsesWebsocketSession {
	key := responsesWebsocketSessionKey(request)
	r.websocketSessions.mu.Lock()
	defer r.websocketSessions.mu.Unlock()
	session := r.websocketSessions.sessions[key]
	if session == nil {
		session = &responsesWebsocketSession{}
		r.websocketSessions.sessions[key] = session
	}
	return session
}

func (r *ResponsesAgentRunner) websocketsDisabled() bool {
	if r == nil || r.websocketSessions == nil {
		return false
	}
	r.websocketSessions.mu.Lock()
	defer r.websocketSessions.mu.Unlock()
	return r.websocketSessions.disabled
}

func (r *ResponsesAgentRunner) disableWebsockets() {
	if r == nil || r.websocketSessions == nil {
		return
	}
	r.websocketSessions.mu.Lock()
	r.websocketSessions.disabled = true
	r.websocketSessions.sessions = map[string]*responsesWebsocketSession{}
	r.websocketSessions.mu.Unlock()
}

func responsesWebsocketSessionKey(request *AgentRequest) string {
	if request == nil {
		return "default"
	}
	if threadID := strings.TrimSpace(request.ThreadID); threadID != "" {
		return "thread:" + threadID + ":turn:" + strings.TrimSpace(request.TurnID)
	}
	if subagent := strings.TrimSpace(request.ClientMetadata["x-openai-subagent"]); subagent != "" {
		return "subagent:" + subagent
	}
	if request.TaskKind != "" {
		return "task:" + string(request.TaskKind)
	}
	return "default"
}

func closeResponsesWebsocketSession(session *responsesWebsocketSession, reason string) {
	if session == nil || session.conn == nil {
		return
	}
	_ = session.conn.Close(websocket.StatusNormalClosure, reason)
	session.conn = nil
}

func (r *ResponsesAgentRunner) Prewarm(ctx context.Context, request *AgentRequest) (*AgentResponse, error) {
	if r == nil || !r.SupportsWebsockets || r.websocketsDisabled() {
		return nil, nil
	}
	if request == nil {
		return nil, ErrInvalidAgentRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	session := r.websocketSession(request)
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.conn != nil {
		return nil, nil
	}
	modelID := strings.TrimSpace(request.Model)
	if modelID == "" {
		modelID = "gpt-5.5"
	}
	apiRequest := &responsesAgentRequest{
		Model: modelID, Instructions: responsesInstructions(request), Input: r.gateContentItemKinds(responsesInputItemsForProvider(request, r.providerName())), Tools: normalizeResponseToolParameters(cloneAnySlice(request.Tools)), ToolChoice: "auto",
		Stream: true, Store: request.Store, ParallelToolCalls: request.ParallelToolCalls, ClientMetadata: cloneStringMap(request.ClientMetadata),
	}
	apiRequest.Reasoning = responsesReasoningParam(request, &ModelInfo{Slug: modelID, SupportsReasoningSummaries: true})
	apiRequest.StreamOptions = responsesStreamOptionsForRequest(request, apiRequest.Reasoning, r.providerName())
	httpRequest, err := r.newResponsesHTTPRequest(ctx, request, apiRequest, "")
	if err != nil {
		return nil, err
	}
	endpoint, err := websocketURLFromHTTP(httpRequest.URL)
	if err != nil {
		return nil, err
	}
	timeout := r.WebsocketConnectTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, response, err := websocket.Dial(connectCtx, endpoint, &websocket.DialOptions{HTTPHeader: httpRequest.Header})
	if err != nil {
		if response != nil && response.StatusCode == http.StatusUpgradeRequired {
			r.disableWebsockets()
			return nil, nil
		}
		if response != nil {
			return nil, fmt.Errorf("responses websocket handshake failed: HTTP %d: %w", response.StatusCode, err)
		}
		return nil, err
	}
	session.conn = conn
	payload := map[string]any{
		"type": "response.create", "model": apiRequest.Model, "input": apiRequest.Input, "tool_choice": apiRequest.ToolChoice,
		"parallel_tool_calls": apiRequest.ParallelToolCalls, "store": apiRequest.Store, "stream": true, "include": []string{}, "generate": false,
	}
	if apiRequest.Instructions != "" {
		payload["instructions"] = apiRequest.Instructions
	}
	if len(apiRequest.ClientMetadata) > 0 {
		payload["client_metadata"] = apiRequest.ClientMetadata
	}
	if len(apiRequest.Tools) > 0 {
		payload["tools"] = apiRequest.Tools
	}
	if apiRequest.Reasoning != nil {
		payload["reasoning"] = apiRequest.Reasoning
		payload["include"] = apiRequest.Include
	}
	if apiRequest.StreamOptions != nil {
		payload["stream_options"] = apiRequest.StreamOptions
	}
	if apiRequest.Text != nil {
		payload["text"] = apiRequest.Text
	}
	if apiRequest.ServiceTier != "" {
		payload["service_tier"] = apiRequest.ServiceTier
	}
	if apiRequest.PromptCacheKey != "" {
		payload["prompt_cache_key"] = apiRequest.PromptCacheKey
	}
	if err := conn.Write(connectCtx, websocket.MessageText, mustJSONBytes(payload)); err != nil {
		closeResponsesWebsocketSession(session, "prewarm write failed")
		return nil, err
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			closeResponsesWebsocketSession(session, "prewarm read failed")
			return nil, fmt.Errorf("responses websocket closed before response.completed: %w", err)
		}
		var event map[string]any
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("failed to decode responses websocket event: %w", err)
		}
		switch strings.TrimSpace(responseToolString(event["type"])) {
		case "response.completed":
			responseID := responseIDFromWebsocketEvent(event)
			return &AgentResponse{ResponseID: responseID, ProviderID: r.ProviderID}, nil
		case "response.incomplete":
			closeResponsesWebsocketSession(session, "prewarm incomplete")
			return nil, responseIncompleteError(data)
		case "response.failed", "error":
			closeResponsesWebsocketSession(session, "prewarm failed")
			return nil, fmt.Errorf("responses websocket prewarm failed: %s", strings.TrimSpace(responseToolString(event["error"])))
		}
	}
}

func (r *ResponsesAgentRunner) RunWebSocket(ctx context.Context, request *AgentRequest) (*AgentResponse, error) {
	return r.runWebSocket(ctx, request, false, false)
}

func (r *ResponsesAgentRunner) runWebSocket(ctx context.Context, request *AgentRequest, authRetried, transportRetried bool) (*AgentResponse, error) {
	if r == nil || !r.SupportsWebsockets || r.websocketsDisabled() {
		return r.Run(ctx, request)
	}
	if request == nil {
		return nil, ErrInvalidAgentRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	session := r.websocketSession(request)
	session.mu.Lock()
	locked := true
	defer func() {
		if locked {
			session.mu.Unlock()
		}
	}()
	modelID := strings.TrimSpace(request.Model)
	if modelID == "" {
		modelID = "gpt-5.5"
	}
	modelInfo := r.modelInfoForRequest(modelID)
	apiRequest := &responsesAgentRequest{
		Model: modelID, Instructions: responsesInstructions(request), Input: r.gateContentItemKinds(responsesInputItemsForProvider(request, r.providerName())), Tools: normalizeResponseToolParameters(cloneAnySlice(request.Tools)), ToolChoice: "auto",
		Stream: true, Store: request.Store, ParallelToolCalls: request.ParallelToolCalls && !modelInfo.UseResponsesLite,
		ServiceTier: ServiceTierForRequest(&modelInfo, request.ServiceTier), PromptCacheKey: strings.TrimSpace(request.PromptCacheKey),
		ClientMetadata: cloneStringMap(request.ClientMetadata), Text: responsesTextParamForRequest(request.OutputSchema, request.ModelVerbosity, &modelInfo),
	}
	apiRequest.Reasoning = responsesReasoningParam(request, &modelInfo)
	apiRequest.StreamOptions = responsesStreamOptionsForRequest(request, apiRequest.Reasoning, r.providerName())
	if apiRequest.Reasoning != nil {
		apiRequest.Include = []string{"reasoning.encrypted_content"}
	}
	httpRequest, err := r.newResponsesHTTPRequest(ctx, request, apiRequest, "")
	if err != nil {
		return nil, err
	}
	endpoint, err := websocketURLFromHTTP(httpRequest.URL)
	if err != nil {
		return nil, err
	}
	timeout := r.WebsocketConnectTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	conn := session.conn
	if conn == nil {
		connectCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var response *http.Response
		conn, response, err = websocket.Dial(connectCtx, endpoint, &websocket.DialOptions{HTTPHeader: httpRequest.Header})
		if err != nil {
			if response != nil && response.StatusCode == http.StatusUpgradeRequired {
				r.disableWebsockets()
				return r.Run(ctx, request)
			}
			if response != nil && response.StatusCode == http.StatusUnauthorized && !authRetried {
				if refreshErr := r.refreshAuthAfterUnauthorized(ctx); refreshErr == nil {
					session.mu.Unlock()
					locked = false
					return r.runWebSocket(ctx, request, true, transportRetried)
				}
			}
			if response != nil {
				return nil, fmt.Errorf("responses websocket handshake failed: HTTP %d: %w", response.StatusCode, err)
			}
			return nil, err
		}
		session.conn = conn
	}
	payload := websocketResponseCreatePayload(apiRequest, request.PreviousResponseID, nil)
	if err := conn.Write(ctx, websocket.MessageText, mustJSONBytes(payload)); err != nil {
		closeResponsesWebsocketSession(session, "response write failed")
		if !transportRetried {
			session.mu.Unlock()
			locked = false
			return r.runWebSocket(ctx, request, authRetried, true)
		}
		r.disableWebsockets()
		return r.Run(ctx, request)
	}
	handler := combinedResponsesStreamHandler(r.StreamHandler, request.StreamHandler)
	accumulator := newResponsesStreamAccumulator(request)
	var outputText strings.Builder
	receivedEvent := false
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			closeResponsesWebsocketSession(session, "response read failed")
			if !receivedEvent && !transportRetried {
				session.mu.Unlock()
				locked = false
				return r.runWebSocket(ctx, request, authRetried, true)
			}
			if !receivedEvent {
				r.disableWebsockets()
				return r.Run(ctx, request)
			}
			return nil, fmt.Errorf("responses websocket closed before response.completed: %w", err)
		}
		receivedEvent = true
		var event map[string]any
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("failed to decode responses websocket event: %w", err)
		}
		rawType := strings.TrimSpace(responseToolString(event["type"]))
		switch rawType {
		case "response.output_text.delta":
			outputText.WriteString(responseToolString(event["delta"]))
		case "response.output_text.done":
			if outputText.Len() == 0 {
				outputText.WriteString(responseToolString(event["text"]))
			}
		case "error":
			closeResponsesWebsocketSession(session, "response failed")
			if websocketEventErrorCode(event) == "previous_response_not_found" && strings.TrimSpace(request.PreviousResponseID) != "" && !transportRetried {
				clone := *request
				clone.PreviousResponseID = ""
				session.mu.Unlock()
				locked = false
				return r.runWebSocket(ctx, &clone, authRetried, true)
			}
			return nil, fmt.Errorf("responses websocket request failed: %s", websocketEventError(event))
		}
		completed, err := accumulator.apply(&responsesSSEEvent{Event: rawType, Data: data}, handler)
		if err != nil {
			return nil, err
		}
		if !completed {
			continue
		}
		if len(accumulator.items) == 0 && strings.TrimSpace(outputText.String()) != "" {
			accumulator.recordAgentItem(&AgentItem{Type: "agent_message", Text: outputText.String()})
		}
		return accumulator.agentResponse(request, r.ProviderID)
	}
}

func websocketResponseCreatePayload(apiRequest *responsesAgentRequest, previousResponseID string, generate *bool) map[string]any {
	payload := map[string]any{
		"type": "response.create", "model": apiRequest.Model, "input": apiRequest.Input, "tool_choice": apiRequest.ToolChoice,
		"parallel_tool_calls": apiRequest.ParallelToolCalls, "store": apiRequest.Store, "stream": true, "include": apiRequest.Include,
	}
	if strings.TrimSpace(previousResponseID) != "" {
		payload["previous_response_id"] = strings.TrimSpace(previousResponseID)
	}
	if generate != nil {
		payload["generate"] = *generate
	}
	if apiRequest.Instructions != "" {
		payload["instructions"] = apiRequest.Instructions
	}
	if len(apiRequest.ClientMetadata) > 0 {
		payload["client_metadata"] = apiRequest.ClientMetadata
	}
	if len(apiRequest.Tools) > 0 {
		payload["tools"] = apiRequest.Tools
	}
	if apiRequest.Reasoning != nil {
		payload["reasoning"] = apiRequest.Reasoning
	}
	if apiRequest.StreamOptions != nil {
		payload["stream_options"] = apiRequest.StreamOptions
	}
	if apiRequest.Text != nil {
		payload["text"] = apiRequest.Text
	}
	if apiRequest.ServiceTier != "" {
		payload["service_tier"] = apiRequest.ServiceTier
	}
	if apiRequest.PromptCacheKey != "" {
		payload["prompt_cache_key"] = apiRequest.PromptCacheKey
	}
	return payload
}

func websocketEventError(event map[string]any) string {
	if value, ok := event["error"].(map[string]any); ok {
		return firstAgentItemValue(responseToolString(value["message"]), responseToolString(value["code"]), "unknown error")
	}
	return firstAgentItemValue(responseToolString(event["message"]), "unknown error")
}

func websocketEventErrorCode(event map[string]any) string {
	if value, ok := event["error"].(map[string]any); ok {
		return strings.TrimSpace(responseToolString(value["code"]))
	}
	return strings.TrimSpace(responseToolString(event["code"]))
}

func websocketURLFromHTTP(value *url.URL) (string, error) {
	if value == nil {
		return "", errors.New("responses websocket URL is nil")
	}
	copy := *value
	switch copy.Scheme {
	case "http":
		copy.Scheme = "ws"
	case "https":
		copy.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported responses websocket scheme %q", copy.Scheme)
	}
	return copy.String(), nil
}

func responseIDFromWebsocketEvent(event map[string]any) string {
	if response, ok := event["response"].(map[string]any); ok {
		return strings.TrimSpace(responseToolString(response["id"]))
	}
	return strings.TrimSpace(responseToolString(event["response_id"]))
}

func mustJSONBytes(value any) []byte {
	data, _ := json.Marshal(value)
	return data
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
	modelsManager := runtimeProvider.ModelsManager(nil)
	providerInfo := runtimeProvider.Info()
	if remote, ok := modelsManager.(*RemoteModelsManager); ok && strings.TrimSpace(codexHome) != "" {
		remote.ConfigureCache(filepath.Clean(codexHome))
	}
	return NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:                   &apiProvider,
		Auth:                       &authHeaders,
		HTTPClient:                 httpClient,
		ProviderID:                 providerID,
		ProviderCapabilities:       cloneProviderCapabilities(runtimeProvider.Capabilities()),
		ProviderRequiresOpenAIAuth: providerInfo.RequiresOpenAIAuth,
		ProviderUsesOwnCredentials: strings.TrimSpace(providerInfo.EnvKey) != "" || strings.TrimSpace(providerInfo.ExperimentalBearerToken) != "" || providerInfo.Auth != nil,
		CodexHome:                  codexHome,
		AuthSnapshot:               snapshot,
		ModelsManager:              modelsManager,
		IncludeAttestation:         runtimeProvider.SupportsAttestation(),
		SupportsWebsockets:         providerInfo.SupportsWebsockets,
		WebsocketConnectTimeout: func() time.Duration {
			info := runtimeProvider.Info()
			return (&info).EffectiveWebsocketConnectTimeout()
		}(),
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
	clone.ProviderCapabilities = r.ProviderCapabilities
	clone.ProviderRequiresOpenAIAuth = r.ProviderRequiresOpenAIAuth
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
	inputItems := responsesInputItemsForProvider(request, r.providerName())
	tools := normalizeResponseToolParameters(cloneAnySlice(request.Tools))
	if !request.DisableHostedImageGeneration {
		tools = r.withHostedToolsForRequest(tools, &modelInfo)
	}
	tools = normalizeResponseToolParameters(tools)
	parallelToolCalls := request.ParallelToolCalls && !modelInfo.UseResponsesLite
	if modelInfo.UseResponsesLite {
		inputItems = responsesLiteInputItems(inputItems, tools, instructions, request.ThreadID)
		instructions = ""
		tools = nil
	}
	inputItems = r.gateContentItemKinds(inputItems)
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
	apiRequest.StreamOptions = responsesStreamOptionsForRequest(request, apiRequest.Reasoning, r.providerName())
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
	r.rememberTurnStateFromHeaders(request, httpResponse.Header)
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
	// Rust #40799: keep "persistent" in local settings, but the Responses API
	// calls it "disabled".
	if effort == "persistent" {
		effort = "disabled"
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

func responsesStreamOptionsForRequest(request *AgentRequest, reasoning *responsesReasoning, providerName string) *responsesStreamOptions {
	if request == nil || !request.ConcurrentReasoningSummaries || reasoning == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(providerName), OpenAIProviderName) {
		return nil
	}
	if strings.TrimSpace(reasoning.Summary) == "" {
		return nil
	}
	return &responsesStreamOptions{ReasoningSummaryDelivery: "sequential_cutoff"}
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

func (r *ResponsesAgentRunner) withHostedToolsForRequest(tools []any, info *ModelInfo) []any {
	if !r.shouldAddHostedImageGenerationTool(info) ||
		responsesToolsContainType(tools, "image_generation") ||
		responsesToolsContainNamespaceFunction(tools, "image_gen", "imagegen") {
		return tools
	}
	return append(tools, map[string]any{
		"type":          "image_generation",
		"output_format": "png",
	})
}

func (r *ResponsesAgentRunner) shouldAddHostedImageGenerationTool(info *ModelInfo) bool {
	if r == nil || info == nil {
		return false
	}
	if info.UseResponsesLite {
		return false
	}
	if !r.ProviderCapabilities.ImageGeneration {
		return false
	}
	if !modelInfoSupportsImageInput(info) {
		return false
	}
	return r.imageGenerationAuthEnabled()
}

func (r *ResponsesAgentRunner) imageGenerationAuthEnabled() bool {
	if r == nil {
		return false
	}
	if account := auth.AccountFromAuth(r.AuthSnapshot); account != nil && account.PlanType == auth.PlanFree {
		return false
	}
	if r.providerUsesOpenAIActorAuthorization() {
		return true
	}
	if r.AuthSnapshot == nil {
		return false
	}
	if authSnapshotUsesCodexBackend(r.AuthSnapshot) {
		return r.ProviderRequiresOpenAIAuth || r.providerName() == OpenAIProviderName
	}
	return r.providerName() == OpenAIProviderName && r.AuthSnapshot.Mode() == "api-key"
}

func (r *ResponsesAgentRunner) providerUsesOpenAIActorAuthorization() bool {
	if r == nil || r.Provider == nil {
		return false
	}
	for name, values := range r.Provider.Headers {
		if !strings.EqualFold(name, OpenAIActorAuthorizationHeader) {
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	}
	return false
}

func modelInfoSupportsImageInput(info *ModelInfo) bool {
	if info == nil {
		return false
	}
	for _, modality := range info.InputModalities {
		if strings.EqualFold(strings.TrimSpace(modality), "image") {
			return true
		}
	}
	return false
}

func responsesToolsContainType(tools []any, toolType string) bool {
	toolType = strings.TrimSpace(toolType)
	if toolType == "" {
		return false
	}
	for _, tool := range tools {
		item, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(responseToolString(item["type"])), toolType) {
			return true
		}
	}
	return false
}

func responsesToolsContainNamespaceFunction(tools []any, namespace string, name string) bool {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if namespace == "" || name == "" {
		return false
	}
	for _, tool := range tools {
		item, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(responseToolString(item["type"])), "namespace") ||
			!strings.EqualFold(strings.TrimSpace(responseToolString(item["name"])), namespace) {
			continue
		}
		switch children := item["tools"].(type) {
		case []map[string]any:
			for _, child := range children {
				if strings.EqualFold(strings.TrimSpace(responseToolString(child["name"])), name) {
					return true
				}
			}
		case []any:
			for _, childValue := range children {
				child, ok := childValue.(map[string]any)
				if ok && strings.EqualFold(strings.TrimSpace(responseToolString(child["name"])), name) {
					return true
				}
			}
		}
	}
	return false
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
	endpoint := r.responsesEndpoint(apiRequest.Model, request)
	if endpoint == ResponsesEndpointGuardian {
		// The dedicated Guardian endpoint is unmetered and rejects inference
		// hints / service tier (Rust #40892).
		apiRequest.ServiceTier = ""
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, r.responsesURL(endpoint), bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(accept) != "" {
		httpRequest.Header.Set("Accept", accept)
	}
	addResponsesLiteHeader(httpRequest.Header, apiRequest.UseResponsesLite)
	addIncludeTimingMetricsHeader(httpRequest.Header, apiRequest.IncludeTimingMetrics)
	addTurnStateHeader(httpRequest.Header, r.turnStateForRequest(request))
	addBetaFeaturesHeader(httpRequest.Header, apiRequest.BetaFeaturesHeader)
	addOriginatorHeader(httpRequest.Header, requestOriginator(request))
	// Routing hints are only sent on the standard /responses route; the
	// dedicated Guardian endpoints omit them (Rust #40892).
	if endpoint == ResponsesEndpointResponses {
		if routingHint := r.responsesRoutingHint(apiRequest.Model, apiRequest.ServiceTier); routingHint != "" {
			httpRequest.Header.Set(codexapi.ClientCodexRoutingHintHeader, routingHint)
		}
	}
	addCompatibilityMetadataHeaders(httpRequest.Header, apiRequest.ClientMetadata)
	addMemoryGenerationHeader(httpRequest.Header, apiRequest.ClientMetadata)
	addHeaders(httpRequest.Header, r.providerHeaders())
	if residency := strings.TrimSpace(r.Residency); residency != "" {
		// Managed enforce_residency is authoritative over any provider
		// http_headers value (Rust #39645).
		httpRequest.Header.Set(ResidencyHeaderName, residency)
	}
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

func (r *ResponsesAgentRunner) responsesRoutingHint(modelID, serviceTier string) string {
	if !r.usesCodexBackend() {
		return ""
	}
	modelID = strings.TrimSpace(modelID)
	serviceTier = strings.TrimSpace(serviceTier)
	if modelID == "" || strings.ContainsAny(modelID, "\r\n") || strings.ContainsAny(serviceTier, "\r\n") {
		return ""
	}
	if serviceTier != "" {
		return fmt.Sprintf("model=%s;tier=%s", modelID, serviceTier)
	}
	return "model=" + modelID
}

// usesCodexBackend reports whether the current provider/auth combination is
// eligible for Codex-backend routing (Rust ModelClient::uses_codex_backend,
// #40892).
func (r *ResponsesAgentRunner) usesCodexBackend() bool {
	if r == nil || !strings.EqualFold(r.providerName(), OpenAIProviderName) || !r.ProviderRequiresOpenAIAuth || r.ProviderUsesOwnCredentials || r.Provider == nil || r.Provider.Auth != nil {
		return false
	}
	return authSnapshotUsesCodexBackend(r.AuthSnapshot)
}

// supportsCodexBackendRoutes reports whether the provider base URL permits the
// dedicated Codex backend routes (Rust
// ModelProviderInfo::supports_codex_backend_routes, #40892).
func (r *ResponsesAgentRunner) supportsCodexBackendRoutes() bool {
	if r == nil || !strings.EqualFold(r.providerName(), OpenAIProviderName) {
		return false
	}
	baseURL := ""
	if r.Provider != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(r.Provider.BaseURL), "/")
	}
	if baseURL == "" {
		return true
	}
	return strings.HasSuffix(baseURL, "/backend-api/codex")
}

// approvalReviewPreferredModel mirrors Rust
// RuntimeProvider::approval_review_preferred_model (#37103 / #40892).
func (r *ResponsesAgentRunner) approvalReviewPreferredModel() string {
	if r.AuthSnapshot != nil && r.AuthSnapshot.Mode() == "api-key" {
		return APIKeyApprovalReviewPreferredModel
	}
	return DefaultApprovalReviewPreferredModel
}

// isGuardianReviewRequest reports whether the agent request is a Guardian
// approval-review inference (Rust guardian session source, #40892). The Go
// reviewer reuses the main ResponsesAgentRunner, so eligibility is derived from
// the request's review task kind / guardian originator.
func isGuardianReviewRequest(request *AgentRequest) bool {
	if request == nil {
		return false
	}
	if request.TaskKind == AgentTaskReview {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(request.Originator), "guardian")
}

// responsesEndpoint selects the Responses-compatible backend route for a
// request (Rust ModelClient::responses_endpoint, #40892). Guardian review
// inference is routed to /guardian only when every eligibility requirement is
// met; all other requests keep the standard /responses route.
func (r *ResponsesAgentRunner) responsesEndpoint(model string, request *AgentRequest) ResponsesEndpoint {
	if r.FreeGuardianEnabled &&
		isGuardianReviewRequest(request) &&
		r.usesCodexBackend() &&
		r.supportsCodexBackendRoutes() &&
		model == r.approvalReviewPreferredModel() {
		return ResponsesEndpointGuardian
	}
	return ResponsesEndpointResponses
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

func (r *ResponsesAgentRunner) turnStateForRequest(request *AgentRequest) string {
	if r == nil || r.turnState == nil {
		return ""
	}
	turnID := turnStateRequestTurnID(request)
	if turnID == "" {
		return ""
	}
	r.turnState.mu.Lock()
	defer r.turnState.mu.Unlock()
	if r.turnState.turnID != turnID {
		return ""
	}
	return r.turnState.value
}

func turnStateRequestTurnID(request *AgentRequest) string {
	if request == nil {
		return ""
	}
	if turnID := strings.TrimSpace(request.TurnID); turnID != "" {
		return turnID
	}
	if turnID := strings.TrimSpace(request.ClientMetadata[codexapi.TurnIDKey]); turnID != "" {
		return turnID
	}
	turnMetadata := strings.TrimSpace(request.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader])
	if turnMetadata == "" {
		turnMetadata = strings.TrimSpace(request.ClientMetadata[codexapi.TurnMetadataHeader])
	}
	if turnMetadata == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(turnMetadata), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(responseToolString(payload[codexapi.TurnIDKey]))
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
		responsesDiagnostic("http.attempt", map[string]any{
			"thread_id":        request.ThreadID,
			"turn_id":          request.TurnID,
			"http_attempt":     attempt + 1,
			"http_max_retries": maxRetries,
			"accept":           accept,
		})
		httpResponse, err := r.doResponsesHTTPRequest(httpRequest)
		shouldRetry := shouldRetryResponsesHTTPRequest(httpResponse, err, retryTooManyRequests)
		status := 0
		requestID := ""
		traceID := ""
		if httpResponse != nil {
			status = httpResponse.StatusCode
			requestID = responseHeaderValue(httpResponse.Header, responsesRequestIDHeader, responsesOAIRequestIDHeader)
			traceID = responseHeaderValue(httpResponse.Header, "x-trace-id")
		}
		responsesDiagnostic("http.result", map[string]any{
			"thread_id":       request.ThreadID,
			"turn_id":         request.TurnID,
			"http_attempt":    attempt + 1,
			"http_status":     status,
			"request_id":      requestID,
			"trace_id":        traceID,
			"transport_error": diagnosticErrorMessage(err),
			"retryable":       shouldRetry,
		})
		if shouldRetry && attempt < maxRetries {
			lastErr = err
			if httpResponse != nil && httpResponse.StatusCode == http.StatusUnauthorized {
				_ = r.refreshAuthAfterUnauthorized(ctx)
			}
			delay := responsesRetryDelay(httpResponse, attempt+1)
			if httpResponse != nil && httpResponse.Body != nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(httpResponse.Body, 4<<10))
				_ = httpResponse.Body.Close()
			}
			recordResponsesRetry("request", attempt+1, delay, "http")
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
	items = BoundExecutedToolCallsForPrompt(items)
	if !request.Store && !request.ItemIDsEnabled {
		items = stripResponseInputItemIDs(items)
	}
	items = prepareResponseInputImages(items)
	return items
}

// prepareResponseInputImages runs the image-preparation pipeline over every
// input_image in the request, mirroring Rust prepare_response_items. The Rust
// version processes all response items (messages and tool outputs) at request
// time so images are always re-encoded to a format the Responses API accepts
// (webp/png/jpeg/gif) and unsupported inputs are replaced with a placeholder.
// Go previously only prepared the current user message, so images carried in
// tool outputs (e.g. view_image, which labels bytes application/octet-stream)
// or replayed from history could reach the API and be rejected as an
// unsupported image.
func prepareResponseInputImages(items []any) []any {
	if len(items) == 0 {
		return items
	}
	data, err := json.Marshal(items)
	if err != nil {
		return items
	}
	if !bytes.Contains(data, []byte(`"input_image"`)) {
		return items
	}
	var normalized []any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return items
	}
	prepareResponseInputImageBlocks(normalized)
	return normalized
}

func prepareResponseInputImageBlocks(value any) {
	switch typed := value.(type) {
	case map[string]any:
		prepareResponseInputImageBlock(typed)
		md, _ := typed["internal_chat_message_metadata_passthrough"].(map[string]any)
		kinds, _ := md["content_item_kinds"].([]any)
		for _, key := range []string{"content", "output", "content_items"} {
			if child, ok := typed[key]; ok {
				if key == "content" && kinds != nil {
					prepareResponseInputImageContentWithKinds(child, kinds)
				} else {
					prepareResponseInputImageBlocks(child)
				}
			}
		}
	case []any:
		for _, child := range typed {
			prepareResponseInputImageBlocks(child)
		}
	}
}

// prepareResponseInputImageContentWithKinds processes a message content array
// while updating content_item_kinds for image-preparation failures, mirroring
// Rust image_preparation.rs which tags failed images as images.preparation_error
// (#40281).
func prepareResponseInputImageContentWithKinds(value any, kinds []any) {
	content, ok := value.([]any)
	if !ok {
		prepareResponseInputImageBlocks(value)
		return
	}
	for i, block := range content {
		prepareResponseInputImageBlock(block.(map[string]any))
		if i < len(kinds) && kinds[i] == "user.image" {
			if m, ok := block.(map[string]any); ok && m["type"] == "input_text" && blockWasPreparationError(m) {
				kinds[i] = "images.preparation_error"
			}
		}
	}
}

var imagePrepErrorPrefix = "image content omitted because"

// blockWasPreparationError reports whether an input_text block was produced by a
// failed image preparation (a placeholder rather than real user text).
func blockWasPreparationError(block map[string]any) bool {
	text, _ := block["text"].(string)
	return strings.HasPrefix(strings.TrimSpace(text), imagePrepErrorPrefix)
}

func prepareResponseInputImageBlock(block map[string]any) {
	itemType, _ := block["type"].(string)
	if itemType != "input_image" && itemType != "image" {
		return
	}
	imageURL, _ := block["image_url"].(string)
	if strings.TrimSpace(imageURL) == "" {
		return
	}
	detail := eventmap.ImagePrepDetail(responseToolString(block["detail"]))
	prepared, err := eventmap.PrepareImagePrepImage(imageURL, detail)
	if err != nil {
		// Replace an image that cannot be processed with a placeholder text
		// block, matching Rust image_preparation.rs.
		delete(block, "image_url")
		delete(block, "detail")
		block["type"] = "input_text"
		block["text"] = eventmap.ImagePrepPlaceholderForError(err)
		return
	}
	block["image_url"] = prepared
}

// gateContentItemKinds clears content_item_kinds from every request item when
// the content_item_kinds feature is explicitly disabled, mirroring Rust's
// client.rs prepare_response_items_for_request. A nil gate (the feature
// default is enabled) preserves the annotation.
func (r *ResponsesAgentRunner) gateContentItemKinds(items []any) []any {
	if r == nil || r.ContentItemKindsEnabled == nil || *r.ContentItemKindsEnabled {
		return items
	}
	return stripResponseItemContentKinds(items)
}

// stripResponseItemContentKinds removes the content_item_kinds metadata key
// from each message item in place. Items without the passthrough metadata are
// left untouched.
func stripResponseItemContentKinds(items []any) []any {
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		md, ok := m["internal_chat_message_metadata_passthrough"].(map[string]any)
		if !ok {
			continue
		}
		delete(md, "content_item_kinds")
	}
	return items
}

func responsesInputItemsForProvider(request *AgentRequest, providerName string) []any {
	items := responsesInputItems(request)
	if strings.EqualFold(strings.TrimSpace(providerName), OpenAIProviderName) {
		return items
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case *AgentItem:
			if typed == nil {
				out = append(out, nil)
				continue
			}
			clone := *typed
			clone.EncryptedFunctionArgs = nil
			out = append(out, &clone)
		case AgentItem:
			clone := typed
			clone.EncryptedFunctionArgs = nil
			out = append(out, clone)
		case map[string]any:
			clone := cloneMapAny(typed)
			delete(clone, "encrypted_function_args")
			out = append(out, clone)
		default:
			out = append(out, item)
		}
	}
	return out
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
		if !isPrefixedResponseItemID(clone.ID) {
			clone.ID = ""
		}
		clone.Data = cloneAgentItemMap(typed.Data)
		clone.Search = cloneAgentItemMap(typed.Search)
		return &clone
	case AgentItem:
		clone := typed
		if !isPrefixedResponseItemID(clone.ID) {
			clone.ID = ""
		}
		clone.Data = cloneAgentItemMap(typed.Data)
		clone.Search = cloneAgentItemMap(typed.Search)
		return clone
	case map[string]any:
		clone := cloneMapAny(typed)
		if !isPrefixedResponseItemID(responseToolString(clone["id"])) {
			delete(clone, "id")
		}
		return clone
	default:
		return item
	}
}

func isPrefixedResponseItemID(value string) bool {
	value = strings.TrimSpace(value)
	prefix, suffix, ok := strings.Cut(value, "_")
	return ok && prefix != "" && suffix != ""
}

func responsesLiteInputItems(inputItems []any, tools []any, instructions string, threadID string) []any {
	if tools == nil {
		tools = []any{}
	}
	// Responses Lite rebuilds its tools and base-instruction prefix on every
	// request. Derive deterministic item IDs from the thread ID plus each
	// item's serialized payload so an unchanged prefix keeps its identity
	// across retries and resumed sessions (Rust client.rs #40962). Changing the
	// thread or the payload changes the IDs.
	prefixItems := []any{responsesLiteAdditionalToolsItem(tools, threadID)}
	if strings.TrimSpace(instructions) != "" {
		prefixItems = append(prefixItems, responsesInputMessage{
			ID:   responsesLiteItemID("msg", []byte(strings.TrimSpace(instructions)), threadID),
			Type: "message",
			Role: "developer",
			Content: []responsesInputContent{{
				Type: "input_text",
				Text: strings.TrimSpace(instructions),
			}},
		})
	}
	items := make([]any, 0, len(prefixItems)+len(inputItems))
	items = append(items, prefixItems...)
	for i := range inputItems {
		items = append(items, stripResponsesLiteImageDetails(inputItems[i]))
	}
	return items
}

func responsesLiteAdditionalToolsItem(tools []any, threadID string) map[string]any {
	item := map[string]any{
		"type":  "additional_tools",
		"role":  "developer",
		"tools": tools,
	}
	if payload, err := json.Marshal(tools); err == nil {
		item["id"] = responsesLiteItemID("at", payload, threadID)
	}
	return item
}

func responsesLiteItemID(prefix string, payload []byte, threadID string) string {
	namespace := uuid.NewSHA1(uuid.NameSpaceOID, []byte(threadID))
	return prefix + "_" + uuid.NewSHA1(namespace, payload).String()
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
	accumulator := newResponsesStreamAccumulator(request)
	for index, output := range apiResponse.Output {
		if item, ok := toolCallAgentItem(&output, index); ok {
			accumulator.applyToolInputDeltas(item)
			items = append(items, *item)
			continue
		}
		if item, ok := imageGenerationAgentItem(&output, index); ok {
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
	case "function_call", "custom_tool_call", "tool_search_call", "web_search_call":
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
		ID:                    id,
		Type:                  output.Type,
		Name:                  output.Name,
		Namespace:             output.Namespace,
		CallID:                callID,
		Arguments:             responseArgumentsString(output.Arguments),
		EncryptedFunctionArgs: cloneAgentStringSlicePtr(output.EncryptedFunctionArgs),
		Input:                 output.Input,
		Execution:             output.Execution,
		Search:                cloneResponseSearch(output.Search),
		Status:                output.Status,
	}
	if item.Type == "tool_search_call" && len(item.Search) == 0 {
		item.Search = responseArgumentsMap(output.Arguments)
	}
	if item.Type == "web_search_call" {
		item.Search = cloneResponseSearch(output.Action)
	}
	return item, true
}

func imageGenerationAgentItem(output *responsesAgentOutputItem, index int) (*AgentItem, bool) {
	if output == nil || output.Type != "image_generation_call" {
		return nil, false
	}
	id := output.ID
	if id == "" {
		id = fmt.Sprintf("image-generation-%d", index+1)
	}
	status := NormalizeImageGenerationStatus(output.Status, output.Result)
	data := map[string]any{
		"status": status,
		"result": output.Result,
	}
	if strings.TrimSpace(output.Revised) != "" {
		data["revisedPrompt"] = output.Revised
		data["revised_prompt"] = output.Revised
	}
	return &AgentItem{
		ID:     id,
		Type:   "image_generation_call",
		Text:   output.Result,
		Status: status,
		Data:   data,
	}, true
}

func NormalizeImageGenerationStatus(status string, result string) string {
	status = strings.TrimSpace(status)
	hasResult := strings.TrimSpace(result) != ""
	if hasResult && (status == "" ||
		strings.EqualFold(status, "generating") ||
		strings.EqualFold(status, "in_progress") ||
		strings.EqualFold(status, "running")) {
		return "completed"
	}
	if status == "" {
		if hasResult {
			return "completed"
		}
		return "in_progress"
	}
	if strings.EqualFold(status, "generating") {
		return "in_progress"
	}
	return status
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
		if key == codexapi.ClientCodexTurnMetadataHeader {
			value = boundedCompatibilityTurnMetadata(value)
		}
		if value == "" || strings.ContainsAny(value, "\r\n") {
			continue
		}
		headers.Set(key, value)
	}
}

func addMemoryGenerationHeader(headers http.Header, metadata map[string]string) {
	if headers == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(metadata[codexapi.ClientOpenAISubagentHeader]), "memory_consolidation") {
		headers.Set(codexapi.ClientOpenAIMemgenRequestHeader, "true")
	}
}

func boundedCompatibilityTurnMetadata(value string) string {
	if value == "" {
		return ""
	}
	var metadata map[string]any
	if json.Unmarshal([]byte(value), &metadata) != nil || metadata == nil {
		return value
	}
	delete(metadata, codexapi.CodeModeToolNamesKey)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return value
	}
	return string(encoded)
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
	out.CodexRolloutBudgetUnits = usage.CodexRolloutBudgetUnits
	if usage.InputTokensDetails != nil {
		out.CachedInputTokens = usage.InputTokensDetails.CachedTokens
		out.CacheWriteInputTokens = usage.InputTokensDetails.CacheWriteTokens
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

func (r *ResponsesAgentRunner) responsesURL(endpoint ResponsesEndpoint) string {
	provider := r.Provider
	if provider == nil {
		provider = &APIProvider{BaseURL: defaultResponsesEndpoint}
	}
	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultResponsesEndpoint
	}
	raw := baseURL + endpoint.Path()
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
	// Rust #38682: misalignment policy violations from HTTP 400/403 responses
	// surface as a typed, non-retryable error.
	if (statusCode == http.StatusBadRequest || statusCode == http.StatusForbidden) &&
		payload.Error != nil &&
		responseErrorCode(payload.Error) == "misalignment_policy_violation" {
		if strings.TrimSpace(message) == "" {
			message = "This request was blocked due to a misalignment policy violation."
		}
		return &codexapi.APIError{Kind: codexapi.ErrorMisalignmentPolicyViolation, Status: statusCode, Message: message}
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}
	message = mapProviderAPIErrorMessage(providerName, statusCode, bodyText, message)
	apiError := &ResponsesAPIError{
		StatusCode:             statusCode,
		Message:                message,
		Body:                   bodyText,
		RequestID:              responseHeaderValue(headers, responsesRequestIDHeader, responsesOAIRequestIDHeader),
		CFRay:                  responseHeaderValue(headers, "cf-ray"),
		AuthorizationError:     responseHeaderValue(headers, "x-openai-authorization-error"),
		AuthorizationErrorCode: responseAuthorizationErrorCode(headers),
	}
	if delay, ok := responsesRequestedRetryDelay(headers, time.Now()); ok {
		apiError.retryDelay = delay
		apiError.hasRetryDelay = true
	}
	return apiError
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

// refreshWorkloadIdentityAuth re-exchanges workload identity after a
// downstream 401 (Rust AuthManager refresh_external_auth + WorkloadIdentity
// ExternalAuth::refresh, #38188): when the process selected workload identity,
// the refresh preserves the previously authenticated account instead of
// falling through to OAuth token refresh.
func (r *ResponsesAgentRunner) refreshWorkloadIdentityAuth(ctx context.Context) error {
	if r == nil || r.AuthSnapshot == nil {
		return errors.New("workload identity refresh is not available")
	}
	if r.WorkloadIdentityRefresh != nil {
		refreshed, err := r.WorkloadIdentityRefresh(ctx, accountIDFromMap(r.AuthSnapshot.Tokens))
		if err != nil {
			return err
		}
		if refreshed == nil {
			return errors.New("workload identity refresh returned no auth")
		}
		return r.applyRefreshedAuth(refreshed)
	}
	if !auth.IsWorkloadIdentitySelected() {
		return errors.New("workload identity is not selected")
	}
	options := r.workloadIdentityOptions()
	if options == nil {
		return errors.New("workload identity options are not configured")
	}
	authProvider, err := auth.WorkloadIdentityAuthForProcess(options)
	if err != nil {
		return err
	}
	if authProvider == nil {
		return errors.New("workload identity is not selected")
	}
	refreshed, err := authProvider.RefreshAuth(ctx, accountIDFromMap(r.AuthSnapshot.Tokens))
	if err != nil {
		return err
	}
	if refreshed == nil {
		return errors.New("workload identity refresh returned no auth")
	}
	return r.applyRefreshedAuth(refreshed)
}

func (r *ResponsesAgentRunner) applyRefreshedAuth(refreshed *auth.AuthDotJSON) error {
	headers, err := AuthHeadersFromAuth(*refreshed)
	if err != nil {
		return err
	}
	r.AuthSnapshot = cloneAuthSnapshot(refreshed)
	r.Auth = &headers
	r.AgentIdentityTelemetry = agentIdentityTelemetryFromAuthHeaders(&headers)
	return nil
}

func (r *ResponsesAgentRunner) workloadIdentityOptions() *auth.WorkloadIdentityAuthOptions {
	if r == nil {
		return nil
	}
	if r.StoreOptions != nil && r.StoreOptions.WorkloadIdentity != nil {
		return r.StoreOptions.WorkloadIdentity
	}
	if strings.TrimSpace(r.AuthIssuer) == "" {
		return nil
	}
	return &auth.WorkloadIdentityAuthOptions{ChatGPTBaseURL: r.AuthIssuer}
}

func providerAuthRefreshInterval(info *ProviderAuthInfo) time.Duration {
	if info == nil || info.RefreshIntervalMS == 0 {
		return 0
	}
	return time.Duration(info.RefreshIntervalMS) * time.Millisecond
}

func (r *ResponsesAgentRunner) refreshAuthAfterUnauthorized(ctx context.Context) error {
	if err := r.refreshBedrockAWSCredentials(ctx); err == nil {
		return nil
	}
	if err := r.refreshWorkloadIdentityAuth(ctx); err == nil {
		return nil
	}
	if err := r.refreshExternalChatGPTAuth(ctx); err == nil {
		return nil
	}
	if err := r.refreshManagedChatGPTAuth(ctx); err == nil {
		return nil
	}
	return r.refreshProviderCommandAuth(ctx)
}

// refreshBedrockAWSCredentials mirrors Rust #39410: when a Bedrock session
// using the AWS SDK credential chain reports expired credentials (e.g.
// "Signature expired"), run the configured aws.auth_refresh command once,
// reload the SDK credentials, and re-sign subsequent requests.
func (r *ResponsesAgentRunner) refreshBedrockAWSCredentials(ctx context.Context) error {
	if r == nil || r.Provider == nil || r.Provider.Name != AmazonBedrockProviderName || r.AWS == nil || r.AWS.AuthRefresh == nil {
		return errors.New("bedrock aws auth refresh is not configured")
	}
	// Rust #39274: at most one provider-owned recovery attempt per request.
	if r.bedrockAuthRecoveryAttempted {
		return errors.New("bedrock aws auth refresh already attempted for this request")
	}
	r.bedrockAuthRecoveryAttempted = true
	refresh := r.AWS.AuthRefresh
	if strings.TrimSpace(refresh.Command) == "" {
		return errors.New("bedrock aws auth refresh command is empty")
	}
	// Share refresh state across matching provider configurations so
	// concurrent failures invoke the command only once (Rust shared_state).
	r.AWSRefreshMu.Lock()
	if r.AWSRefreshInFlight != nil {
		ch := r.AWSRefreshInFlight
		r.AWSRefreshMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
		return nil
	}
	ch := make(chan struct{})
	r.AWSRefreshInFlight = ch
	r.AWSRefreshMu.Unlock()

	runErr := r.runBedrockAWSRefreshCommand(ctx, refresh)

	r.AWSRefreshMu.Lock()
	r.AWSRefreshInFlight = nil
	close(ch)
	r.AWSRefreshMu.Unlock()
	if runErr != nil {
		return runErr
	}
	// Reload the SDK credential chain and re-sign future requests.
	if r.AWS != nil {
		awsContext, err := auth.LoadAWSAuthContext(&auth.AWSAuthConfig{
			Profile: strings.TrimSpace(r.AWS.Profile),
			Region:  strings.TrimSpace(r.AWS.Region),
			Service: AmazonBedrockMantleServiceName,
		})
		if err != nil {
			return fmt.Errorf("failed to reload Amazon Bedrock auth after refresh: %w", err)
		}
		if r.Auth == nil {
			r.Auth = &AuthHeaders{Headers: http.Header{}}
		}
		r.Auth.SignRequest = func(ctx context.Context, request *http.Request, body []byte) (*SignedRequest, error) {
			return signBedrockMantleRequest(awsContext, request, body)
		}
	}
	return nil
}

func (r *ResponsesAgentRunner) runBedrockAWSRefreshCommand(ctx context.Context, refresh *ProviderAuthRefreshInfo) error {
	if refresh == nil || strings.TrimSpace(refresh.Command) == "" {
		return errors.New("bedrock aws auth refresh command is empty")
	}
	timeoutMS := refresh.TimeoutMS
	if timeoutMS == 0 {
		timeoutMS = DefaultProviderAuthTimeoutMS
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	args := append([]string(nil), refresh.Args...)
	cmd := exec.CommandContext(execCtx, refresh.Command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("bedrock aws auth refresh failed: %s", message)
	}
	return nil
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
		if delay, ok := responsesRequestedRetryDelay(response.Header, time.Now()); ok {
			return delay
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
	// Match Rust's backoff jitter range so reconnecting clients do not retry in lockstep.
	jitter := 0.9 + rand.Float64()*0.2
	return time.Duration(float64(delay) * jitter)
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

func cloneProviderCapabilities(capabilities ProviderCapabilities) *ProviderCapabilities {
	clone := capabilities
	return &clone
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

func cloneBoolPtrModel(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneProviderAWSAuthInfo(value *ProviderAWSAuthInfo) *ProviderAWSAuthInfo {
	if value == nil {
		return nil
	}
	clone := *value
	if value.AuthRefresh != nil {
		refresh := *value.AuthRefresh
		refresh.Args = append([]string(nil), value.AuthRefresh.Args...)
		clone.AuthRefresh = &refresh
	}
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
