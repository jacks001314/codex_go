package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"codex_go/codexapi"
)

type ResponsesStreamEventKind string

const (
	ResponsesStreamEventHeaders                   ResponsesStreamEventKind = "response.headers"
	ResponsesStreamEventServerModel               ResponsesStreamEventKind = "response.server_model"
	ResponsesStreamEventRateLimits                ResponsesStreamEventKind = "response.rate_limits"
	ResponsesStreamEventModelsETag                ResponsesStreamEventKind = "response.models_etag"
	ResponsesStreamEventReasoning                 ResponsesStreamEventKind = "response.server_reasoning_included"
	ResponsesStreamEventTimingMetrics             ResponsesStreamEventKind = "responsesapi.websocket_timing"
	ResponsesStreamEventModelReroute              ResponsesStreamEventKind = "response.model_reroute"
	ResponsesStreamEventModelVerify               ResponsesStreamEventKind = "response.model_verification"
	ResponsesStreamEventModeration                ResponsesStreamEventKind = "response.turn_moderation_metadata"
	ResponsesStreamEventSafetyBuffer              ResponsesStreamEventKind = "response.safety_buffering"
	ResponsesStreamEventCreated                   ResponsesStreamEventKind = "response.created"
	ResponsesStreamEventOutputAdded               ResponsesStreamEventKind = "response.output_item.added"
	ResponsesStreamEventOutputDone                ResponsesStreamEventKind = "response.output_item.done"
	ResponsesStreamEventOutputText                ResponsesStreamEventKind = "response.output_text.delta"
	ResponsesStreamEventToolInputDelta            ResponsesStreamEventKind = "response.tool_call_input.delta"
	ResponsesStreamEventPlanDelta                 ResponsesStreamEventKind = "response.plan.delta"
	ResponsesStreamEventReasoningSummaryTextDelta ResponsesStreamEventKind = "response.reasoning_summary_text.delta"
	ResponsesStreamEventReasoningTextDelta        ResponsesStreamEventKind = "response.reasoning_text.delta"
	ResponsesStreamEventReasoningSummaryPartAdded ResponsesStreamEventKind = "response.reasoning_summary_part.added"
	ResponsesStreamEventCompleted                 ResponsesStreamEventKind = "response.completed"
	ResponsesStreamEventRetrying                  ResponsesStreamEventKind = "response.retrying"
)

type ResponsesStreamEvent struct {
	Kind               ResponsesStreamEventKind
	RetryAttempt       uint64
	RetryMax           uint64
	RetryError         string
	RetryDelay         time.Duration
	RetryHTTPStatus    *uint16
	ResponseID         string
	RequestID          string
	Model              string
	TurnState          string
	ModelsETag         string
	Headers            map[string]string
	RateLimit          *ResponsesRateLimitSnapshot
	TimingMetrics      map[string]any
	Reasoning          *bool
	Reroute            *ResponsesModelReroute
	Verification       *ResponsesModelVerification
	ModerationMetadata any
	SafetyBuffering    *ResponsesSafetyBuffering
	Item               *AgentItem
	RawItem            json.RawMessage
	Delta              string
	ItemID             string
	CallID             string
	PlanDelta          *ResponsesPlanDelta
	ReasoningDelta     *ResponsesReasoningDelta
	ReasoningPart      *ResponsesReasoningPart
	Usage              *AgentUsage
	EndTurn            *bool
	RawType            string
}

type ResponsesModelReroute struct {
	FromModel string
	ToModel   string
	Reason    string
}

type ResponsesModelVerification struct {
	Verifications []string
}

type ResponsesSafetyBuffering struct {
	Model           string
	UseCases        []string
	Reasons         []string
	ShowBufferingUI bool
	FasterModel     *string
}

type ResponsesPlanDelta struct {
	ItemID string
	Delta  string
}

type ResponsesReasoningDelta struct {
	ItemID       string
	Delta        string
	SummaryIndex *int
	ContentIndex *int
}

type ResponsesReasoningPart struct {
	ItemID       string
	SummaryIndex int
}

type ResponsesStreamHandler func(event *ResponsesStreamEvent)

type ResponsesRateLimitSnapshot struct {
	LimitID              string                    `json:"limitId,omitempty"`
	LimitName            string                    `json:"limitName,omitempty"`
	Primary              *ResponsesRateLimitWindow `json:"primary,omitempty"`
	Secondary            *ResponsesRateLimitWindow `json:"secondary,omitempty"`
	Credits              *ResponsesCreditsSnapshot `json:"credits,omitempty"`
	PlanType             string                    `json:"planType,omitempty"`
	RateLimitReachedType string                    `json:"rateLimitReachedType,omitempty"`
}

type ResponsesRateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins,omitempty"`
	ResetsAt           *int64  `json:"resetsAt,omitempty"`
}

type ResponsesCreditsSnapshot struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance,omitempty"`
}

type responsesSSEEvent struct {
	Event string
	Data  []byte
}

var errResponsesStreamFailed = errors.New("response.failed event received")

type responsesStreamAccumulator struct {
	responseID            string
	serverModel           string
	items                 []AgentItem
	messages              []string
	usage                 AgentUsage
	hasUsage              bool
	timingMetrics         map[string]any
	functionCallArgDeltas map[string]string
	customToolInputDeltas map[string]string
}

func (r *ResponsesAgentRunner) runStreaming(ctx context.Context, request *AgentRequest, apiRequest *responsesAgentRequest) (*AgentResponse, error) {
	maxRetries := r.streamMaxRetries()
	for attempt := uint64(0); ; attempt++ {
		response, err := r.runStreamingOnce(ctx, request, apiRequest)
		if err == nil {
			return response, nil
		}
		if attempt >= maxRetries || !isRetryableResponsesStreamError(err) {
			return nil, err
		}
		delay := responsesRetryDelay(nil, attempt+1)
		emitResponsesStreamEvent(combinedResponsesStreamHandler(r.StreamHandler, request.StreamHandler), &ResponsesStreamEvent{
			Kind:            ResponsesStreamEventRetrying,
			RetryAttempt:    attempt + 1,
			RetryMax:        maxRetries,
			RetryError:      err.Error(),
			RetryDelay:      delay,
			RetryHTTPStatus: responsesStreamErrorHTTPStatus(err),
		})
		if err := sleepWithContext(ctx, delay); err != nil {
			return nil, err
		}
	}
}

func responsesStreamErrorHTTPStatus(err error) *uint16 {
	var status int
	var apiError *codexapi.APIError
	if errors.As(err, &apiError) && apiError != nil {
		status = apiError.Status
	}
	var responsesError *ResponsesAPIError
	if status == 0 && errors.As(err, &responsesError) && responsesError != nil {
		status = responsesError.StatusCode
	}
	if status <= 0 || status > 65535 {
		return nil
	}
	value := uint16(status)
	return &value
}

func (r *ResponsesAgentRunner) runStreamingOnce(ctx context.Context, request *AgentRequest, apiRequest *responsesAgentRequest) (*AgentResponse, error) {
	httpResponse, err := r.doResponsesHTTPRequestWithRetry(ctx, request, apiRequest, "text/event-stream", r.requestMaxRetries())
	if err != nil {
		return nil, err
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		responseBody, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, 16<<20))
		if readErr != nil {
			return nil, readErr
		}
		return nil, responsesHTTPError(r.providerName(), httpResponse.StatusCode, httpResponse.Header, responseBody)
	}
	r.rememberTurnStateFromHeaders(request, httpResponse.Header)
	handler := combinedResponsesStreamHandler(r.StreamHandler, request.StreamHandler)
	emitResponsesHeaderEvents(handler, httpResponse.Header)
	response, err := parseResponsesStream(ctx, newIdleTimeoutReader(httpResponse.Body, r.streamIdleTimeout()), request, r.ProviderID, handler)
	if err != nil {
		return nil, err
	}
	return applyResponsesHeaderMetadata(response, httpResponse.Header), nil
}

func isRetryableResponsesStreamError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errResponsesStreamFailed) {
		return false
	}
	var apiError *codexapi.APIError
	if errors.As(err, &apiError) {
		// Context exhaustion is deterministic for the current input. Retrying the
		// same request cannot change the token count and only duplicates work.
		return apiError.Kind != codexapi.ErrorContextWindowExceeded && apiError.Status >= http.StatusInternalServerError
	}
	var apiErr *ResponsesAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusRequestTimeout || apiErr.StatusCode >= 500
	}
	return true
}

func combinedResponsesStreamHandler(handlers ...ResponsesStreamHandler) ResponsesStreamHandler {
	active := make([]ResponsesStreamHandler, 0, len(handlers))
	for _, handler := range handlers {
		if handler != nil {
			active = append(active, handler)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return func(event *ResponsesStreamEvent) {
		for _, handler := range active {
			handler(event)
		}
	}
}

func parseResponsesStream(ctx context.Context, reader io.Reader, request *AgentRequest, providerID string, handler ResponsesStreamHandler) (*AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if reader == nil {
		return nil, errors.New("responses stream body is nil")
	}
	accumulator := &responsesStreamAccumulator{}
	parser := newResponsesSSEParser(reader)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sse, err := parser.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		done, err := accumulator.apply(sse, handler)
		if err != nil {
			return nil, err
		}
		if done {
			return accumulator.agentResponse(request, providerID)
		}
	}
	return nil, errors.New("stream closed before response.completed")
}

type idleTimeoutReader struct {
	reader  io.Reader
	timeout time.Duration
}

func newIdleTimeoutReader(reader io.Reader, timeout time.Duration) io.Reader {
	if reader == nil || timeout <= 0 {
		return reader
	}
	return &idleTimeoutReader{reader: reader, timeout: timeout}
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	type readResult struct {
		n   int
		err error
	}
	done := make(chan readResult, 1)
	go func() {
		n, err := r.reader.Read(p)
		done <- readResult{n: n, err: err}
	}()
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	select {
	case result := <-done:
		return result.n, result.err
	case <-timer.C:
		return 0, fmt.Errorf("responses stream idle timeout after %s", r.timeout)
	}
}

const (
	responsesRequestIDHeader      = "x-request-id"
	responsesOAIRequestIDHeader   = "x-oai-request-id"
	responsesOpenAIModelHeader    = "openai-model"
	responsesXOpenAIModelHeader   = "x-openai-model"
	responsesCodexTurnStateHeader = "x-codex-turn-state"
	responsesModelsETagHeader     = "x-models-etag"
	responsesReasoningHeader      = "x-reasoning-included"
)

func emitResponsesHeaderEvents(handler ResponsesStreamHandler, headers http.Header) {
	emitResponsesStreamEvent(handler, responsesHeadersEvent(headers))
	if headers == nil {
		return
	}
	if model := responseHeaderValue(headers, responsesOpenAIModelHeader, responsesXOpenAIModelHeader); model != "" {
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:    ResponsesStreamEventServerModel,
			Model:   model,
			RawType: string(ResponsesStreamEventServerModel),
		})
	}
	for _, snapshot := range parseResponsesRateLimits(headers) {
		rateLimit := snapshot
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:      ResponsesStreamEventRateLimits,
			RateLimit: &rateLimit,
			RawType:   string(ResponsesStreamEventRateLimits),
		})
	}
	if etag := responseHeaderValue(headers, responsesModelsETagHeader); etag != "" {
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:       ResponsesStreamEventModelsETag,
			ModelsETag: etag,
			RawType:    string(ResponsesStreamEventModelsETag),
		})
	}
	if headerExists(headers, responsesReasoningHeader) {
		included := true
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:      ResponsesStreamEventReasoning,
			Reasoning: &included,
			RawType:   string(ResponsesStreamEventReasoning),
		})
	}
}

func responsesHeadersEvent(headers http.Header) *ResponsesStreamEvent {
	if len(headers) == 0 {
		return nil
	}
	return &ResponsesStreamEvent{
		Kind:       ResponsesStreamEventHeaders,
		RequestID:  responseHeaderValue(headers, responsesRequestIDHeader, responsesOAIRequestIDHeader),
		Model:      responseHeaderValue(headers, responsesOpenAIModelHeader, responsesXOpenAIModelHeader),
		TurnState:  responseHeaderValue(headers, responsesCodexTurnStateHeader),
		ModelsETag: responseHeaderValue(headers, responsesModelsETagHeader),
		Headers:    cloneResponseHeaders(headers),
		RawType:    string(ResponsesStreamEventHeaders),
	}
}

func (r *ResponsesAgentRunner) rememberTurnStateFromHeaders(request *AgentRequest, headers http.Header) {
	if r == nil || r.turnState == nil {
		return
	}
	turnID := turnStateRequestTurnID(request)
	if turnID == "" {
		return
	}
	turnState := responseHeaderValue(headers, codexapi.ClientCodexTurnStateHeader)
	r.turnState.mu.Lock()
	defer r.turnState.mu.Unlock()
	if r.turnState.turnID != turnID {
		r.turnState.turnID = turnID
		r.turnState.value = ""
	}
	if r.turnState.value == "" && turnState != "" {
		r.turnState.value = turnState
	}
}

func headerExists(headers http.Header, name string) bool {
	if headers == nil {
		return false
	}
	_, ok := headers[http.CanonicalHeaderKey(name)]
	if ok {
		return true
	}
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func responseHeaderValue(headers http.Header, names ...string) string {
	for _, name := range names {
		value := strings.TrimSpace(headers.Get(name))
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneResponseHeaders(headers http.Header) map[string]string {
	out := map[string]string{}
	for key, values := range headers {
		name := strings.ToLower(strings.TrimSpace(key))
		value := responseHeaderValues(values)
		if name != "" && value != "" {
			out[name] = value
		}
	}
	return out
}

func responseHeaderValues(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, ", ")
}

func parseResponsesRateLimits(headers http.Header) []ResponsesRateLimitSnapshot {
	if headers == nil {
		return nil
	}
	snapshots := []ResponsesRateLimitSnapshot{}
	if snapshot := parseResponsesRateLimit(headers, "codex"); snapshot != nil {
		snapshots = append(snapshots, *snapshot)
	}
	limitIDs := responseRateLimitIDs(headers)
	for _, limitID := range limitIDs {
		if limitID == "codex" {
			continue
		}
		snapshot := parseResponsesRateLimit(headers, limitID)
		if snapshot != nil && snapshot.hasData() {
			snapshots = append(snapshots, *snapshot)
		}
	}
	return snapshots
}

func parseResponsesRateLimit(headers http.Header, limitID string) *ResponsesRateLimitSnapshot {
	normalized := normalizeResponsesLimitID(limitID)
	if normalized == "" {
		normalized = "codex"
	}
	prefix := "x-" + strings.ReplaceAll(normalized, "_", "-")
	snapshot := &ResponsesRateLimitSnapshot{
		LimitID:   normalized,
		LimitName: responseHeaderValue(headers, prefix+"-limit-name"),
		Primary:   parseResponsesRateLimitWindow(headers, prefix+"-primary"),
		Secondary: parseResponsesRateLimitWindow(headers, prefix+"-secondary"),
		Credits:   parseResponsesCredits(headers),
	}
	return snapshot
}

func (s *ResponsesRateLimitSnapshot) hasData() bool {
	return s != nil && (s.Primary != nil || s.Secondary != nil || s.Credits != nil || s.LimitName != "" || s.PlanType != "" || s.RateLimitReachedType != "")
}

func parseResponsesRateLimitWindow(headers http.Header, prefix string) *ResponsesRateLimitWindow {
	used, ok := responseHeaderFloat(headers, prefix+"-used-percent")
	if !ok {
		return nil
	}
	windowDuration := responseHeaderInt64Pointer(headers, prefix+"-window-minutes")
	resetsAt := responseHeaderInt64Pointer(headers, prefix+"-reset-at")
	hasData := used != 0 || windowDuration != nil || resetsAt != nil
	if !hasData {
		return nil
	}
	return &ResponsesRateLimitWindow{
		UsedPercent:        used,
		WindowDurationMins: windowDuration,
		ResetsAt:           resetsAt,
	}
}

func parseResponsesCredits(headers http.Header) *ResponsesCreditsSnapshot {
	hasCredits, ok := responseHeaderBool(headers, "x-codex-credits-has-credits")
	if !ok {
		return nil
	}
	unlimited, ok := responseHeaderBool(headers, "x-codex-credits-unlimited")
	if !ok {
		return nil
	}
	var balance *string
	if value := responseHeaderValue(headers, "x-codex-credits-balance"); value != "" {
		balance = &value
	}
	return &ResponsesCreditsSnapshot{
		HasCredits: hasCredits,
		Unlimited:  unlimited,
		Balance:    balance,
	}
}

func responseRateLimitIDs(headers http.Header) []string {
	seen := map[string]bool{}
	for key := range headers {
		name := strings.ToLower(strings.TrimSpace(key))
		if !strings.HasSuffix(name, "-primary-used-percent") || !strings.HasPrefix(name, "x-") {
			continue
		}
		limitName := strings.TrimPrefix(strings.TrimSuffix(name, "-primary-used-percent"), "x-")
		limitID := normalizeResponsesLimitID(limitName)
		if limitID != "" {
			seen[limitID] = true
		}
	}
	out := make([]string, 0, len(seen))
	for limitID := range seen {
		out = append(out, limitID)
	}
	sort.Strings(out)
	return out
}

func normalizeResponsesLimitID(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", "_")
}

func responseHeaderFloat(headers http.Header, name string) (float64, bool) {
	raw := responseHeaderValue(headers, name)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, false
	}
	return value, true
}

func responseHeaderInt64Pointer(headers http.Header, name string) *int64 {
	raw := responseHeaderValue(headers, name)
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &value
}

func responseHeaderBool(headers http.Header, name string) (bool, bool) {
	raw := responseHeaderValue(headers, name)
	switch {
	case strings.EqualFold(raw, "true") || raw == "1":
		return true, true
	case strings.EqualFold(raw, "false") || raw == "0":
		return false, true
	default:
		return false, false
	}
}

func newResponsesSSEParser(reader io.Reader) *responsesSSEParser {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
	return &responsesSSEParser{scanner: scanner}
}

type responsesSSEParser struct {
	scanner *bufio.Scanner
	event   string
	data    bytes.Buffer
}

func (p *responsesSSEParser) Next() (*responsesSSEEvent, error) {
	for p.scanner.Scan() {
		line := strings.TrimRight(p.scanner.Text(), "\r")
		if line == "" {
			if event := p.flush(); event != nil {
				return event, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, hasValue := strings.Cut(line, ":")
		if hasValue {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			p.event = value
		case "data":
			if p.data.Len() > 0 {
				p.data.WriteByte('\n')
			}
			p.data.WriteString(value)
		}
	}
	if err := p.scanner.Err(); err != nil {
		return nil, err
	}
	if event := p.flush(); event != nil {
		return event, nil
	}
	return nil, io.EOF
}

func (p *responsesSSEParser) flush() *responsesSSEEvent {
	if p.event == "" && p.data.Len() == 0 {
		return nil
	}
	event := &responsesSSEEvent{
		Event: p.event,
		Data:  append([]byte(nil), p.data.Bytes()...),
	}
	p.event = ""
	p.data.Reset()
	return event
}

func (a *responsesStreamAccumulator) apply(sse *responsesSSEEvent, handler ResponsesStreamHandler) (bool, error) {
	if sse == nil {
		return false, nil
	}
	rawType := strings.TrimSpace(sse.Event)
	if rawType == "" && len(bytes.TrimSpace(sse.Data)) > 0 {
		rawType = jsonStringField(sse.Data, "type")
	}
	if rawType == "" || rawType == "[DONE]" {
		return false, nil
	}
	if len(bytes.TrimSpace(sse.Data)) == 0 {
		if rawType == string(ResponsesStreamEventCreated) {
			emitResponsesStreamEvent(handler, &ResponsesStreamEvent{Kind: ResponsesStreamEventCreated, RawType: rawType})
		}
		return false, nil
	}
	a.emitServerModelMetadata(sse.Data, handler)
	if rawType == "response.metadata" {
		emitResponsesMetadataEvents(sse.Data, handler)
	}
	switch rawType {
	case string(ResponsesStreamEventTimingMetrics):
		metrics := responsesTimingMetricsFromEventData(sse.Data)
		if len(metrics) > 0 {
			a.timingMetrics = cloneMapAny(metrics)
			emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
				Kind:          ResponsesStreamEventTimingMetrics,
				TimingMetrics: cloneMapAny(metrics),
				RawType:       rawType,
			})
		}
	case "response.created":
		responseID := responseIDFromEventData(sse.Data)
		a.responseID = firstNonEmptyResponseValue(a.responseID, responseID)
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:       ResponsesStreamEventCreated,
			ResponseID: a.responseID,
			RawType:    rawType,
		})
	case "response.output_item.added":
		item, err := agentItemFromStreamEventData(sse.Data, len(a.items), true)
		if err != nil {
			return false, err
		}
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:       ResponsesStreamEventOutputAdded,
			ResponseID: a.responseID,
			Item:       item,
			ItemID:     agentItemID(item),
			CallID:     agentItemCallID(item),
			RawType:    rawType,
		})
	case "response.output_item.done":
		item, err := agentItemFromStreamEventData(sse.Data, len(a.items), false)
		if err != nil {
			return false, err
		}
		a.applyToolInputDeltas(item)
		if item != nil {
			a.recordAgentItem(item)
		}
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:       ResponsesStreamEventOutputDone,
			ResponseID: a.responseID,
			Item:       item,
			RawItem:    rawResponseItemFromStreamEventData(sse.Data),
			ItemID:     agentItemID(item),
			CallID:     agentItemCallID(item),
			RawType:    rawType,
		})
	case "response.output_text.delta":
		delta := jsonStringField(sse.Data, "delta")
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:       ResponsesStreamEventOutputText,
			ResponseID: a.responseID,
			Delta:      delta,
			ItemID:     jsonStringField(sse.Data, "item_id"),
			RawType:    rawType,
		})
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		delta, itemID, callID := a.appendToolInputDelta(rawType, sse.Data)
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:       ResponsesStreamEventToolInputDelta,
			ResponseID: a.responseID,
			Delta:      delta,
			ItemID:     itemID,
			CallID:     callID,
			RawType:    rawType,
		})
	case "response.plan.delta", "response.plan_text.delta":
		plan := planDeltaFromStreamEventData(sse.Data)
		if plan == nil {
			return false, nil
		}
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:       ResponsesStreamEventPlanDelta,
			ResponseID: a.responseID,
			Delta:      plan.Delta,
			ItemID:     plan.ItemID,
			PlanDelta:  plan,
			RawType:    rawType,
		})
	case "response.reasoning_summary_text.delta":
		reasoning := reasoningSummaryDeltaFromStreamEventData(sse.Data)
		if reasoning == nil {
			return false, nil
		}
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:           ResponsesStreamEventReasoningSummaryTextDelta,
			ResponseID:     a.responseID,
			Delta:          reasoning.Delta,
			ItemID:         reasoning.ItemID,
			ReasoningDelta: reasoning,
			RawType:        rawType,
		})
	case "response.reasoning_text.delta":
		reasoning := reasoningTextDeltaFromStreamEventData(sse.Data)
		if reasoning == nil {
			return false, nil
		}
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:           ResponsesStreamEventReasoningTextDelta,
			ResponseID:     a.responseID,
			Delta:          reasoning.Delta,
			ItemID:         reasoning.ItemID,
			ReasoningDelta: reasoning,
			RawType:        rawType,
		})
	case "response.reasoning_summary_part.added":
		part := reasoningPartFromStreamEventData(sse.Data)
		if part == nil {
			return false, nil
		}
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:          ResponsesStreamEventReasoningSummaryPartAdded,
			ResponseID:    a.responseID,
			ItemID:        part.ItemID,
			ReasoningPart: part,
			RawType:       rawType,
		})
	case "response.completed":
		items, err := completedAgentItemsFromStreamEventData(sse.Data, len(a.items))
		if err != nil {
			return false, err
		}
		for i := range items {
			a.applyToolInputDeltas(&items[i])
			a.recordAgentItem(&items[i])
		}
		usage, hasUsage := usageFromStreamEventData(sse.Data)
		a.responseID = firstNonEmptyResponseValue(a.responseID, responseIDFromEventData(sse.Data))
		if hasUsage {
			a.usage = usage
			a.hasUsage = true
		}
		endTurn := endTurnFromStreamEventData(sse.Data)
		event := &ResponsesStreamEvent{
			Kind:       ResponsesStreamEventCompleted,
			ResponseID: a.responseID,
			RawType:    rawType,
			EndTurn:    endTurn,
		}
		if hasUsage {
			usageCopy := usage
			event.Usage = &usageCopy
		}
		emitResponsesStreamEvent(handler, event)
		return true, nil
	case "response.failed":
		return false, responseFailedError(sse.Data)
	}
	return false, nil
}

func (a *responsesStreamAccumulator) recordAgentItem(item *AgentItem) {
	if a == nil || item == nil {
		return
	}
	key := agentItemRecordKey(item)
	if key != "" {
		for i := range a.items {
			if agentItemRecordKey(&a.items[i]) == key {
				a.items[i] = mergeStreamAgentItem(a.items[i], *item)
				a.rebuildMessages()
				return
			}
		}
	}
	if item.Type == "agent_message" && strings.TrimSpace(item.Text) != "" {
		for i := range a.items {
			if a.items[i].Type != "agent_message" || strings.TrimSpace(a.items[i].Text) != strings.TrimSpace(item.Text) {
				continue
			}
			existingKey := agentItemRecordKey(&a.items[i])
			if isGeneratedAgentMessageID(existingKey) || key == "" || existingKey == "" {
				a.items[i] = *item
				a.rebuildMessages()
				return
			}
		}
	}
	a.items = append(a.items, *item)
	if item.Type == "agent_message" && strings.TrimSpace(item.Text) != "" {
		a.messages = append(a.messages, item.Text)
	}
}

func (a *responsesStreamAccumulator) rebuildMessages() {
	if a == nil {
		return
	}
	a.messages = a.messages[:0]
	for i := range a.items {
		if a.items[i].Type == "agent_message" && strings.TrimSpace(a.items[i].Text) != "" {
			a.messages = append(a.messages, a.items[i].Text)
		}
	}
}

func agentItemRecordKey(item *AgentItem) string {
	if item == nil {
		return ""
	}
	if isToolAgentItemType(item.Type) && strings.TrimSpace(item.CallID) != "" {
		return "call:" + strings.TrimSpace(item.CallID)
	}
	return firstNonEmptyResponseValue(item.ID, item.CallID)
}

func isToolAgentItemType(itemType string) bool {
	switch itemType {
	case "function_call", "custom_tool_call", "tool_search_call":
		return true
	default:
		return false
	}
}

func mergeStreamAgentItem(existing AgentItem, incoming AgentItem) AgentItem {
	merged := incoming
	if existing.ID != "" {
		merged.ID = existing.ID
	}
	if merged.CallID == "" {
		merged.CallID = existing.CallID
	}
	if existing.Namespace != "" && merged.Namespace == "" {
		merged.Namespace = existing.Namespace
		merged.Name = existing.Name
	}
	if merged.Name == "" {
		merged.Name = existing.Name
	}
	if merged.Arguments == "" {
		merged.Arguments = existing.Arguments
	}
	if merged.Input == "" {
		merged.Input = existing.Input
	}
	if merged.Text == "" {
		merged.Text = existing.Text
	}
	if merged.Status == "" {
		merged.Status = existing.Status
	}
	if merged.Execution == "" {
		merged.Execution = existing.Execution
	}
	if len(merged.Search) == 0 && len(existing.Search) > 0 {
		merged.Search = cloneResponseSearch(existing.Search)
	}
	if len(merged.Data) == 0 && len(existing.Data) > 0 {
		merged.Data = cloneMapAny(existing.Data)
	}
	return merged
}

func isGeneratedAgentMessageID(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "agent-message-") {
		return false
	}
	suffix := strings.TrimPrefix(value, "agent-message-")
	if suffix == "" {
		return false
	}
	for _, ch := range suffix {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func (a *responsesStreamAccumulator) appendToolInputDelta(rawType string, data []byte) (string, string, string) {
	delta := jsonStringField(data, "delta")
	itemID := firstNonEmptyResponseValue(jsonStringField(data, "item_id"), jsonStringField(data, "itemId"))
	callID := firstNonEmptyResponseValue(jsonStringField(data, "call_id"), jsonStringField(data, "callId"))
	if a == nil || delta == "" {
		return delta, itemID, callID
	}
	keys := uniqueNonEmptyResponseValues(itemID, callID)
	if len(keys) == 0 {
		return delta, itemID, callID
	}
	switch rawType {
	case "response.function_call_arguments.delta":
		if a.functionCallArgDeltas == nil {
			a.functionCallArgDeltas = map[string]string{}
		}
		for _, key := range keys {
			a.functionCallArgDeltas[key] += delta
		}
	case "response.custom_tool_call_input.delta":
		if a.customToolInputDeltas == nil {
			a.customToolInputDeltas = map[string]string{}
		}
		for _, key := range keys {
			a.customToolInputDeltas[key] += delta
		}
	}
	return delta, itemID, callID
}

func (a *responsesStreamAccumulator) applyToolInputDeltas(item *AgentItem) {
	if a == nil || item == nil {
		return
	}
	switch item.Type {
	case "function_call":
		if item.Arguments == "" {
			item.Arguments = accumulatedToolInputDelta(a.functionCallArgDeltas, item.ID, item.CallID)
		}
	case "custom_tool_call":
		if item.Input == "" {
			item.Input = accumulatedToolInputDelta(a.customToolInputDeltas, item.ID, item.CallID)
		}
	}
}

func accumulatedToolInputDelta(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := values[key]; value != "" {
			return value
		}
	}
	return ""
}

func emitResponsesMetadataEvents(data []byte, handler ResponsesStreamHandler) {
	if reroute := modelRerouteFromStreamMetadata(data); reroute != nil {
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:    ResponsesStreamEventModelReroute,
			Reroute: reroute,
			RawType: string(ResponsesStreamEventModelReroute),
		})
	}
	if verification := modelVerificationFromStreamMetadata(data); verification != nil {
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:         ResponsesStreamEventModelVerify,
			Verification: verification,
			RawType:      string(ResponsesStreamEventModelVerify),
		})
	}
	if metadata, ok := turnModerationMetadataFromStreamMetadata(data); ok {
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:               ResponsesStreamEventModeration,
			ModerationMetadata: metadata,
			RawType:            string(ResponsesStreamEventModeration),
		})
	}
	if buffering := safetyBufferingFromStreamMetadata(data); buffering != nil {
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:            ResponsesStreamEventSafetyBuffer,
			SafetyBuffering: buffering,
			RawType:         string(ResponsesStreamEventSafetyBuffer),
		})
	}
}

func (a *responsesStreamAccumulator) emitServerModelMetadata(data []byte, handler ResponsesStreamHandler) {
	model := serverModelFromStreamEventData(data)
	if model == "" || model == a.serverModel {
		return
	}
	a.serverModel = model
	emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
		Kind:    ResponsesStreamEventServerModel,
		Model:   model,
		RawType: string(ResponsesStreamEventServerModel),
	})
}

func modelRerouteFromStreamMetadata(data []byte) *ResponsesModelReroute {
	value := streamMetadataValue(data, "model_reroute", "modelReroute", "reroute")
	payload, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	fromModel := stringFromAnyMap(payload, "from_model", "fromModel")
	toModel := stringFromAnyMap(payload, "to_model", "toModel")
	reason := stringFromAnyMap(payload, "reason")
	if fromModel == "" && toModel == "" && reason == "" {
		return nil
	}
	return &ResponsesModelReroute{FromModel: fromModel, ToModel: toModel, Reason: reason}
}

func modelVerificationFromStreamMetadata(data []byte) *ResponsesModelVerification {
	value := streamMetadataValue(data, "model_verification", "modelVerification", "verification")
	if value == nil {
		value = streamMetadataValue(data, "verifications")
	}
	verifications := stringSliceFromAny(value)
	if len(verifications) == 0 {
		if payload, ok := value.(map[string]any); ok {
			verifications = stringSliceFromAny(firstAnyFromMap(payload, "verifications", "verification"))
		}
	}
	if len(verifications) == 0 {
		return nil
	}
	return &ResponsesModelVerification{Verifications: verifications}
}

func turnModerationMetadataFromStreamMetadata(data []byte) (any, bool) {
	value := streamMetadataValue(data, "turn_moderation_metadata", "turnModerationMetadata", "moderation_metadata", "moderationMetadata")
	if value == nil {
		return nil, false
	}
	return value, true
}

func safetyBufferingFromStreamMetadata(data []byte) *ResponsesSafetyBuffering {
	value := streamMetadataValue(data, "safety_buffering", "safetyBuffering", "model_safety_buffering", "modelSafetyBuffering")
	payload, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	buffering := &ResponsesSafetyBuffering{
		Model:           stringFromAnyMap(payload, "model"),
		UseCases:        stringSliceFromAny(firstAnyFromMap(payload, "use_cases", "useCases")),
		Reasons:         stringSliceFromAny(firstAnyFromMap(payload, "reasons")),
		ShowBufferingUI: boolFromAny(firstAnyFromMap(payload, "show_buffering_ui", "showBufferingUi", "showBufferingUI")),
		FasterModel:     stringPtrFromAny(firstAnyFromMap(payload, "faster_model", "fasterModel")),
	}
	if buffering.Model == "" && len(buffering.UseCases) == 0 && len(buffering.Reasons) == 0 && !buffering.ShowBufferingUI && buffering.FasterModel == nil {
		return nil
	}
	return buffering
}

func (a *responsesStreamAccumulator) agentResponse(request *AgentRequest, providerID string) (*AgentResponse, error) {
	if a == nil {
		return nil, errors.New("responses stream accumulator is nil")
	}
	message := strings.TrimSpace(strings.Join(a.messages, "\n\n"))
	if message == "" && len(a.items) == 0 {
		return nil, errors.New("responses stream did not contain assistant text or tool calls")
	}
	usage := a.usage
	if !a.hasUsage {
		outputTokens := estimateTokens(message)
		usage = AgentUsage{OutputTokens: outputTokens, TotalTokens: outputTokens}
	}
	return &AgentResponse{
		ResponseID:    a.responseID,
		Message:       message,
		Items:         append([]AgentItem(nil), a.items...),
		Usage:         usage,
		Model:         requestModel(request),
		ProviderID:    firstNonEmptyResponseValue(providerID, requestProviderID(request)),
		TimingMetrics: cloneMapAny(a.timingMetrics),
	}, nil
}

func responsesTimingMetricsFromEventData(data []byte) map[string]any {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	value := firstAnyFromMap(payload, "timing_metrics", "timingMetrics")
	metrics, ok := value.(map[string]any)
	if !ok || len(metrics) == 0 {
		return nil
	}
	return cloneMapAny(metrics)
}

func agentItemFromStreamEventData(data []byte, index int, allowEmptyMessage bool) (*AgentItem, error) {
	var payload struct {
		Item *responsesAgentOutputItem `json:"item"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to decode responses stream output item: %w", err)
	}
	if payload.Item == nil {
		return nil, nil
	}
	rawItem := rawResponseItemFromStreamEventData(data)
	return agentItemFromResponseOutput(payload.Item, rawItem, index, allowEmptyMessage)
}

func completedAgentItemsFromStreamEventData(data []byte, index int) ([]AgentItem, error) {
	var payload struct {
		Response struct {
			Output []json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to decode responses completed output: %w", err)
	}
	if len(payload.Response.Output) == 0 {
		return nil, nil
	}
	items := make([]AgentItem, 0, len(payload.Response.Output))
	for i, rawItem := range payload.Response.Output {
		if len(rawItem) == 0 {
			continue
		}
		var output responsesAgentOutputItem
		if err := json.Unmarshal(rawItem, &output); err != nil {
			return nil, fmt.Errorf("failed to decode responses completed output item: %w", err)
		}
		item, err := agentItemFromResponseOutput(&output, rawItem, index+i, false)
		if err != nil {
			return nil, err
		}
		if item != nil {
			items = append(items, *item)
		}
	}
	return items, nil
}

func agentItemFromResponseOutput(output *responsesAgentOutputItem, rawItem json.RawMessage, index int, allowEmptyMessage bool) (*AgentItem, error) {
	if output == nil {
		return nil, nil
	}
	if item, ok := reasoningAgentItemFromRaw(rawItem, index); ok {
		return item, nil
	}
	if item, ok := toolCallAgentItem(output, index); ok {
		return item, nil
	}
	if item, ok := imageGenerationAgentItem(output, index); ok {
		return item, nil
	}
	if output.Type != "" && output.Type != "message" {
		return nil, nil
	}
	if output.Role != "" && output.Role != "assistant" {
		return nil, nil
	}
	text := strings.TrimSpace(output.text())
	if text == "" && !allowEmptyMessage {
		return nil, nil
	}
	id := output.ID
	if id == "" {
		id = fmt.Sprintf("agent-message-%d", index+1)
	}
	data := map[string]any{}
	if phase := strings.TrimSpace(output.Phase); phase != "" {
		data["phase"] = phase
	}
	return &AgentItem{ID: id, Type: "agent_message", Text: text, Data: data}, nil
}

func rawResponseItemFromStreamEventData(data []byte) json.RawMessage {
	var payload struct {
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || len(payload.Item) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), payload.Item...)
}

func reasoningAgentItemFromRaw(raw json.RawMessage, index int) (*AgentItem, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, false
	}
	if stringFromAnyMap(item, "type") != "reasoning" {
		return nil, false
	}
	id := stringFromAnyMap(item, "id")
	if id == "" {
		id = fmt.Sprintf("reasoning-%d", index+1)
	}
	summary := reasoningTextsFromAny(item["summary"])
	content := reasoningTextsFromAny(item["content"])
	data := map[string]any{}
	if summary != nil {
		data["summary"] = summary
	}
	if content != nil {
		data["reasoningContent"] = content
		data["content"] = content
	}
	if encrypted, ok := item["encrypted_content"].(string); ok {
		data["encryptedContent"] = encrypted
		data["encrypted_content"] = encrypted
	}
	text := strings.TrimSpace(strings.Join(append(append([]string{}, summary...), content...), "\n"))
	return &AgentItem{ID: id, Type: "reasoning", Text: text, Data: data}, true
}

func reasoningTextsFromAny(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, entry := range typed {
			switch value := entry.(type) {
			case string:
				if strings.TrimSpace(value) != "" {
					out = append(out, value)
				}
			case map[string]any:
				if text := stringFromAnyMap(value, "text"); text != "" {
					out = append(out, text)
				}
			}
		}
		if out == nil {
			return []string{}
		}
		return out
	default:
		return nil
	}
}

func planDeltaFromStreamEventData(data []byte) *ResponsesPlanDelta {
	delta := jsonStringField(data, "delta")
	if delta == "" {
		return nil
	}
	return &ResponsesPlanDelta{
		ItemID: firstNonEmptyResponseValue(jsonStringField(data, "item_id"), jsonStringField(data, "itemId")),
		Delta:  delta,
	}
}

func reasoningSummaryDeltaFromStreamEventData(data []byte) *ResponsesReasoningDelta {
	delta := jsonStringField(data, "delta")
	index, ok := jsonIntField(data, "summary_index", "summaryIndex")
	if delta == "" || !ok {
		return nil
	}
	return &ResponsesReasoningDelta{
		ItemID:       firstNonEmptyResponseValue(jsonStringField(data, "item_id"), jsonStringField(data, "itemId")),
		Delta:        delta,
		SummaryIndex: &index,
	}
}

func reasoningTextDeltaFromStreamEventData(data []byte) *ResponsesReasoningDelta {
	delta := jsonStringField(data, "delta")
	index, ok := jsonIntField(data, "content_index", "contentIndex")
	if delta == "" || !ok {
		return nil
	}
	return &ResponsesReasoningDelta{
		ItemID:       firstNonEmptyResponseValue(jsonStringField(data, "item_id"), jsonStringField(data, "itemId")),
		Delta:        delta,
		ContentIndex: &index,
	}
}

func reasoningPartFromStreamEventData(data []byte) *ResponsesReasoningPart {
	index, ok := jsonIntField(data, "summary_index", "summaryIndex")
	if !ok {
		return nil
	}
	return &ResponsesReasoningPart{
		ItemID:       firstNonEmptyResponseValue(jsonStringField(data, "item_id"), jsonStringField(data, "itemId")),
		SummaryIndex: index,
	}
}

func usageFromStreamEventData(data []byte) (AgentUsage, bool) {
	var payload struct {
		Response struct {
			Usage *responsesAgentAPIUsage `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || payload.Response.Usage == nil {
		return AgentUsage{}, false
	}
	usage := usageFromResponses(payload.Response.Usage, "")
	return usage, true
}

func responseIDFromEventData(data []byte) string {
	var payload struct {
		Response struct {
			ID string `json:"id"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Response.ID)
}

func endTurnFromStreamEventData(data []byte) *bool {
	var payload struct {
		Response struct {
			EndTurn *bool `json:"end_turn"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	return payload.Response.EndTurn
}

func streamMetadataValue(data []byte, keys ...string) any {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	candidates := []any{payload}
	if metadata, ok := payload["metadata"].(map[string]any); ok {
		candidates = append(candidates, metadata)
	}
	if response, ok := payload["response"].(map[string]any); ok {
		candidates = append(candidates, response)
		if metadata, ok := response["metadata"].(map[string]any); ok {
			candidates = append(candidates, metadata)
		}
	}
	for _, candidate := range candidates {
		for _, key := range keys {
			data, ok := candidate.(map[string]any)
			if !ok {
				continue
			}
			value, ok := data[key]
			if ok {
				return value
			}
		}
	}
	return nil
}

func firstAnyFromMap(data map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			return value
		}
	}
	return nil
}

func stringFromAnyMap(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			if text, ok := value.(string); ok {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var decoded []string
		if err := json.Unmarshal(data, &decoded); err == nil {
			return decoded
		}
		return nil
	}
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		text := strings.TrimSpace(typed)
		return strings.EqualFold(text, "true") || text == "1"
	default:
		return false
	}
}

func stringPtrFromAny(value any) *string {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return nil
	}
	text = strings.TrimSpace(text)
	return &text
}

func serverModelFromStreamEventData(data []byte) string {
	var payload struct {
		Headers  map[string]any `json:"headers"`
		Response struct {
			Headers map[string]any `json:"headers"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	if model := stringFromHeaderMap(payload.Response.Headers, responsesOpenAIModelHeader, responsesXOpenAIModelHeader); model != "" {
		return model
	}
	return stringFromHeaderMap(payload.Headers, responsesOpenAIModelHeader, responsesXOpenAIModelHeader)
}

func stringFromHeaderMap(headers map[string]any, names ...string) string {
	if len(headers) == 0 {
		return ""
	}
	for _, name := range names {
		for key, value := range headers {
			if !strings.EqualFold(key, name) {
				continue
			}
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return strings.TrimSpace(typed)
				}
			case []any:
				for _, item := range typed {
					text, _ := item.(string)
					if strings.TrimSpace(text) != "" {
						return strings.TrimSpace(text)
					}
				}
			}
		}
	}
	return ""
}

func responseFailedError(data []byte) error {
	var payload struct {
		Response struct {
			Error *responsesAgentAPIErrorBody `json:"error"`
		} `json:"response"`
		Error *responsesAgentAPIErrorBody `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return errResponsesStreamFailed
	}
	errBody := payload.Error
	if errBody == nil {
		errBody = payload.Response.Error
	}
	if responseErrorCode(errBody) == "context_length_exceeded" {
		return &codexapi.APIError{
			Kind:    codexapi.ErrorContextWindowExceeded,
			Status:  http.StatusBadRequest,
			Message: strings.TrimSpace(errBody.Message),
		}
	}
	if errBody != nil && strings.TrimSpace(errBody.Message) != "" {
		return fmt.Errorf("%w: %s", errResponsesStreamFailed, strings.TrimSpace(errBody.Message))
	}
	return errResponsesStreamFailed
}

func responseErrorCode(errBody *responsesAgentAPIErrorBody) string {
	if errBody == nil || errBody.Code == nil {
		return ""
	}
	switch code := errBody.Code.(type) {
	case string:
		return strings.TrimSpace(code)
	default:
		return strings.TrimSpace(fmt.Sprint(code))
	}
}

func jsonStringField(data []byte, key string) string {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	value, _ := payload[key].(string)
	return value
}

func jsonIntField(data []byte, keys ...string) (int, bool) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0, false
	}
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			if typed < 0 || typed > float64(int(^uint(0)>>1)) || math.Trunc(typed) != typed {
				return 0, false
			}
			return int(typed), true
		case int:
			if typed < 0 {
				return 0, false
			}
			return typed, true
		case json.Number:
			parsed, err := strconv.ParseInt(string(typed), 10, 0)
			if err != nil || parsed < 0 {
				return 0, false
			}
			return int(parsed), true
		case string:
			parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 0)
			if err != nil || parsed < 0 {
				return 0, false
			}
			return int(parsed), true
		}
	}
	return 0, false
}

func agentItemID(item *AgentItem) string {
	if item == nil {
		return ""
	}
	return item.ID
}

func agentItemCallID(item *AgentItem) string {
	if item == nil {
		return ""
	}
	return item.CallID
}

func emitResponsesStreamEvent(handler ResponsesStreamHandler, event *ResponsesStreamEvent) {
	if handler != nil && event != nil {
		handler(event)
	}
}
