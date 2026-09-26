package model

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"codex_go/codexapi"
)

// Rust 5a0d0929e2: connection failures during sampling are retried with
// exponential delays from 5s to 60s so the stream stays alive while the
// provider becomes reachable again.
const initialConnectionRetryDelay = 5 * time.Second
const maxConnectionRetryDelay = 60 * time.Second

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
	RetryStatus        string
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
	UsageMetadata      *ResponseUsageMetadata
	EndTurn            *bool
	RawType            string
}

// flexUnavailableStreamError recognizes Rust's `parse_flex_unavailable`
// (codex-api/src/error.rs, #47967): an error object whose `code` is
// `flex_unavailable`, either as the SSE event's top-level `error` field or as
// the `response.error` payload of a `response.failed` event.
func flexUnavailableStreamError(data []byte) (string, bool) {
	var payload struct {
		Error    *responsesAgentAPIErrorBody `json:"error"`
		Response struct {
			Error *responsesAgentAPIErrorBody `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false
	}
	for _, body := range []*responsesAgentAPIErrorBody{payload.Error, payload.Response.Error} {
		if body == nil {
			continue
		}
		if responseErrorCode(body) == "flex_unavailable" {
			return strings.TrimSpace(body.Message), true
		}
	}
	return "", false
}

// ResponseUsageMetadata mirrors Rust protocol::ResponseUsageMetadata (#41087):
// per-response usage metadata reported by the upstream service. Amount stays a
// string so high-precision values survive without numeric conversion.
type ResponseUsageMetadata struct {
	Amount   *string         `json:"amount"`
	Metadata json.RawMessage `json:"metadata"`
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
	declaredCustomTools   map[string]struct{}
	// safetyBufferingTreatment carries the response-side safety-buffering
	// headers: the HTTP response headers for a streamed response, or the JSON
	// `headers` of each WebSocket event (Rust's `safety_buffering_for_event`).
	safetyBufferingTreatment safetyBufferingTreatment
}

func newResponsesStreamAccumulator(request *AgentRequest) *responsesStreamAccumulator {
	return &responsesStreamAccumulator{declaredCustomTools: declaredCustomResponseTools(request)}
}

func (r *ResponsesAgentRunner) runStreaming(ctx context.Context, request *AgentRequest, apiRequest *responsesAgentRequest) (*AgentResponse, error) {
	maxRetries := r.streamMaxRetries()
	connectionRetryDelay := initialConnectionRetryDelay
	connectionRetries := uint64(0)
	for attempt := uint64(0); ; attempt++ {
		fields := responsesRequestDiagnosticFields(request, apiRequest)
		fields["stream_attempt"] = attempt + 1
		fields["stream_max_retries"] = maxRetries
		responsesDiagnostic("sampling.start", fields)
		response, err := r.runStreamingOncePreemptible(ctx, request, apiRequest)
		if err != nil && errors.Is(err, errResponsesSamplingPreempted) {
			// Rust #48141: a signaled step yields the request instead of
			// consuming a retry, and the connection state is dropped so the
			// replacement request sends full history.
			r.dropConnectionForRequest(request)
			return preemptedAgentResponse(request), nil
		}
		if err == nil {
			responsesDiagnostic("sampling.completed", map[string]any{"thread_id": request.ThreadID, "turn_id": request.TurnID, "stream_attempt": attempt + 1, "response_id": response.ResponseID})
			return response, nil
		}
		// Rust 5a0d0929e2 + da898490fc (#38601): connection failures get an
		// unbounded reconnect window (5-60s exponential) that does not consume
		// the bounded stream retry budget, for regular sampling on non-Bedrock
		// providers while the unbounded_connection_retries feature is enabled.
		if r.unboundedConnectionRetriesEnabled() && isResponsesConnectionFailure(err) && !r.providerIsAmazonBedrock() {
			connectionRetries++
			responsesDiagnostic("sampling.connection_retry", map[string]any{"thread_id": request.ThreadID, "turn_id": request.TurnID, "delay_ms": connectionRetryDelay.Milliseconds()})
			recordResponsesRetry("sampling", connectionRetries, connectionRetryDelay, "stream")
			emitResponsesStreamEvent(combinedResponsesStreamHandler(r.StreamHandler, request.StreamHandler), &ResponsesStreamEvent{
				Kind:        ResponsesStreamEventRetrying,
				RetryError:  "Reconnecting... waiting for network",
				RetryDelay:  connectionRetryDelay,
				RetryMax:    maxRetries,
				RetryStatus: "connection_failed",
			})
			if preempted, err := sleepWithContextPreemptible(ctx, request.Preempt, connectionRetryDelay); err != nil {
				return nil, err
			} else if preempted {
				r.dropConnectionForRequest(request)
				return preemptedAgentResponse(request), nil
			}
			connectionRetryDelay *= 2
			if connectionRetryDelay > maxConnectionRetryDelay {
				connectionRetryDelay = maxConnectionRetryDelay
			}
			continue
		}
		retryable := isRetryableResponsesStreamError(err)
		responsesDiagnostic("sampling.failed", map[string]any{"thread_id": request.ThreadID, "turn_id": request.TurnID, "stream_attempt": attempt + 1, "error": err.Error(), "error_kind": responsesDiagnosticErrorKind(err), "retryable": retryable, "retry_budget_remaining": attempt < maxRetries})
		if attempt >= maxRetries || !retryable {
			return nil, err
		}
		delay, requested := codexapi.RetryDelayInfo(err)
		if !requested {
			delay = responsesRetryDelay(nil, attempt+1)
		}
		recordResponsesRetry("sampling", attempt+1, delay, "stream")
		emitResponsesStreamEvent(combinedResponsesStreamHandler(r.StreamHandler, request.StreamHandler), &ResponsesStreamEvent{
			Kind:            ResponsesStreamEventRetrying,
			RetryAttempt:    attempt + 1,
			RetryMax:        maxRetries,
			RetryError:      err.Error(),
			RetryDelay:      delay,
			RetryHTTPStatus: responsesStreamErrorHTTPStatus(err),
		})
		if preempted, err := sleepWithContextPreemptible(ctx, request.Preempt, delay); err != nil {
			return nil, err
		} else if preempted {
			// Rust #48141: interrupting the retry backoff preserves the original
			// input for the replacement request.
			r.dropConnectionForRequest(request)
			return preemptedAgentResponse(request), nil
		}
	}
}

// errResponsesSamplingPreempted is the internal marker for a sampling request
// that new user input interrupted (Rust #48141). It never escapes the runner:
// the preempted step becomes an empty, "needs follow-up" response.
var errResponsesSamplingPreempted = errors.New("responses sampling preempted")

// preemptedAgentResponse mirrors Rust's signaled-step result: no assistant
// output, and the turn continues with the queued input.
func preemptedAgentResponse(request *AgentRequest) *AgentResponse {
	response := &AgentResponse{Preempted: true}
	if request != nil {
		response.ProviderID = strings.TrimSpace(request.ProviderID)
		response.Model = strings.TrimSpace(request.Model)
	}
	return response
}

// runStreamingOncePreemptible mirrors Rust #48141's `try_run_sampling_request`:
// the stream future is raced against the request's preemption signal, and a
// preemption cancels the in-flight request so its transport is abandoned.
func (r *ResponsesAgentRunner) runStreamingOncePreemptible(ctx context.Context, request *AgentRequest, apiRequest *responsesAgentRequest) (*AgentResponse, error) {
	if request == nil || request.Preempt == nil {
		return r.runStreamingOnce(ctx, request, apiRequest)
	}
	select {
	case <-request.Preempt:
		return nil, errResponsesSamplingPreempted
	default:
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		response *AgentResponse
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		response, err := r.runStreamingOnce(streamCtx, request, apiRequest)
		done <- outcome{response: response, err: err}
	}()
	select {
	case result := <-done:
		return result.response, result.err
	case <-request.Preempt:
		cancel()
		return nil, errResponsesSamplingPreempted
	}
}

// sleepWithContextPreemptible waits for the retry backoff, reporting true when
// new user input interrupted it (Rust #48141's retry-backoff preemption).
func sleepWithContextPreemptible(ctx context.Context, preempt <-chan struct{}, delay time.Duration) (bool, error) {
	if preempt == nil {
		return false, sleepWithContext(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-preempt:
		return true, nil
	case <-timer.C:
		return false, nil
	}
}

// isResponsesConnectionFailure reports whether err is a transport-level
// connection failure (dial refused, no route, DNS, TLS handshake), mirroring
// Rust TransportError::Connection. It never inspects request URLs.
func isResponsesConnectionFailure(err error) bool {
	if err == nil {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		switch opErr.Op {
		case "dial", "connect":
			return true
		}
		if opErr.Err != nil {
			return errors.Is(opErr.Err, syscall.ECONNREFUSED) ||
				errors.Is(opErr.Err, syscall.ECONNRESET) ||
				errors.Is(opErr.Err, syscall.EHOSTUNREACH) ||
				errors.Is(opErr.Err, syscall.ENETUNREACH) ||
				errors.Is(opErr.Err, syscall.ETIMEDOUT)
		}
	}
	return errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		isTLSCertificateError(err)
}

func isTLSCertificateError(err error) bool {
	if err == nil {
		return false
	}
	var certErr x509.UnknownAuthorityError
	if errors.As(err, &certErr) {
		return true
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return true
	}
	return false
}

func (r *ResponsesAgentRunner) providerIsAmazonBedrock() bool {
	return r != nil && r.Provider != nil && strings.EqualFold(r.Provider.Name, AmazonBedrockProviderName)
}

// unboundedConnectionRetriesEnabled reports whether connection failures may
// keep sampling alive with the unbounded reconnect window. nil (unset) means
// the stable feature default: enabled.
func (r *ResponsesAgentRunner) unboundedConnectionRetriesEnabled() bool {
	if r == nil || r.UnboundedConnectionRetries == nil {
		return true
	}
	return *r.UnboundedConnectionRetries
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

// requestServiceTier reports the service tier the request carries, which Rust
// reads from the session telemetry's inference-request metadata.
func requestServiceTier(request *AgentRequest) string {
	if request == nil {
		return ""
	}
	return strings.TrimSpace(request.ServiceTier)
}

// requestReasoningEffort reports the reasoning effort the request carries, which
// Rust reads from the session telemetry's inference-request metadata.
func requestReasoningEffort(request *AgentRequest) string {
	if request == nil {
		return ""
	}
	return strings.TrimSpace(request.ReasoningEffort)
}

func (r *ResponsesAgentRunner) runStreamingOnce(ctx context.Context, request *AgentRequest, apiRequest *responsesAgentRequest) (*AgentResponse, error) {
	// Rust instruments the client's stream call with `stream_request`
	// (core/src/session/turn.rs); the span carries the request's diagnostics
	// while the stream is opened.
	streamCtx, streamSpan := r.telemetryTracerFor().StartSpan(ctx, nil, StreamRequestSpanName, nil)
	defer streamSpan.End()
	httpResponse, err := r.doResponsesHTTPRequestWithRetry(streamCtx, request, apiRequest, "text/event-stream", r.requestMaxRetries())
	if err != nil {
		return nil, err
	}
	defer httpResponse.Body.Close()
	responsesDiagnostic("sampling.headers", map[string]any{
		"thread_id":    request.ThreadID,
		"turn_id":      request.TurnID,
		"http_status":  httpResponse.StatusCode,
		"request_id":   responseHeaderValue(httpResponse.Header, responsesRequestIDHeader, responsesOAIRequestIDHeader),
		"trace_id":     responseHeaderValue(httpResponse.Header, "x-trace-id"),
		"server_model": responseHeaderValue(httpResponse.Header, responsesOpenAIModelHeader, responsesXOpenAIModelHeader),
	})
	// The handler is built before the status check because a usage-limit error
	// still refreshes the client's rate-limit snapshot from its headers.
	handler := combinedResponsesStreamHandler(r.StreamHandler, request.StreamHandler)
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		responseBody, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, 16<<20))
		if readErr != nil {
			return nil, readErr
		}
		apiErr := responsesHTTPError(r.providerName(), httpResponse.StatusCode, httpResponse.Header, responseBody)
		setResponsesAPIErrorURL(apiErr, httpResponse)
		emitUsageLimitErrorHeaderEvents(handler, httpResponse.Header, apiErr)
		return nil, apiErr
	}
	r.rememberTurnStateFromHeaders(request, httpResponse.Header)
	emitResponsesHeaderEvents(handler, httpResponse.Header)
	// Rust derives the safety-buffering treatment from the response headers once
	// per stream and threads it into every event.
	treatment, _ := safetyBufferingTreatmentFromHeaders(httpResponse.Header)
	response, err := parseResponsesStreamWithTreatment(streamCtx, newIdleTimeoutReader(httpResponse.Body, r.streamIdleTimeout()), request, r.ProviderID, handler, r.Metrics, r.Telemetry, treatment)
	if err != nil {
		return nil, err
	}
	return applyResponsesHeaderMetadata(response, httpResponse.Header), nil
}

func isRetryableResponsesStreamError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiError *codexapi.APIError
	if errors.As(err, &apiError) {
		details := apiError.Details()
		switch details.Kind {
		case codexapi.ErrorRetryable, codexapi.ErrorServerOverloaded:
			return true
		case codexapi.ErrorRateLimitExceeded:
			// Rust #45602: `slow_down` (and `rate_limit_exceeded`) are retryable
			// rate limits, so the stream retries them even without a 5xx status.
			return true
		case codexapi.ErrorContextWindowExceeded, codexapi.ErrorQuotaExceeded,
			codexapi.ErrorUsageNotIncluded, codexapi.ErrorInvalidRequest,
			codexapi.ErrorCyberPolicy, codexapi.ErrorBioPolicy,
			codexapi.ErrorFlexUnavailable:
			return false
		default:
			return details.Status >= http.StatusInternalServerError
		}
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
	return parseResponsesStreamWithMetrics(ctx, reader, request, providerID, handler, nil, nil)
}

// parseResponsesStreamWithMetrics mirrors parseResponsesStream and also records
// the per-event codex.sse_event metrics and diagnostic records (Rust's
// SessionTelemetry::log_sse_event with the watcher's per-event duration).
func parseResponsesStreamWithMetrics(ctx context.Context, reader io.Reader, request *AgentRequest, providerID string, handler ResponsesStreamHandler, metrics MetricsSink, telemetrySink SessionTelemetrySink) (*AgentResponse, error) {
	return parseResponsesStreamWithTreatment(ctx, reader, request, providerID, handler, metrics, telemetrySink, safetyBufferingTreatment{})
}

// parseResponsesStreamWithTreatment is `parseResponsesStreamWithMetrics` with the
// response's safety-buffering treatment (Rust threads the same value into
// `process_sse_with_treatment`).
func parseResponsesStreamWithTreatment(ctx context.Context, reader io.Reader, request *AgentRequest, providerID string, handler ResponsesStreamHandler, metrics MetricsSink, telemetrySink SessionTelemetrySink, treatment safetyBufferingTreatment) (*AgentResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if reader == nil {
		return nil, errors.New("responses stream body is nil")
	}
	accumulator := newResponsesStreamAccumulator(request)
	accumulator.safetyBufferingTreatment = treatment
	parser := newResponsesSSEParser(reader)
	// Rust's turn loop wraps the streamed events in `receiving_stream`, with one
	// `handle_responses` (the per-event span record_responses names) and one
	// `receiving` span per event.
	tracer := telemetryTracerFor(telemetrySink)
	streamCtx, receivingStreamSpan := tracer.StartSpan(ctx, nil, ReceivingStreamSpanName, nil)
	defer receivingStreamSpan.End()
	// Rust measures the response's time to first token from the streaming
	// consumer's start to the first output item (client.rs `ttft_ms`).
	streamStartedAt := time.Now()
	var ttftMillis *int64
	for {
		if err := streamCtx.Err(); err != nil {
			return nil, err
		}
		eventStartedAt := time.Now()
		eventCtx, handleResponsesSpan := tracer.StartSpan(streamCtx, receivingStreamSpan, HandleResponsesSpanName, handleResponsesSpanAttributes(request))
		receivingCtx, receivingSpan := tracer.StartSpan(eventCtx, handleResponsesSpan, ReceivingSpanName, nil)
		sse, err := parser.Next()
		if err != nil {
			receivingSpan.End()
			if errors.Is(err, io.EOF) {
				handleResponsesSpan.End()
				break
			}
			// Rust's failed branch reports the unknown kind when the event never
			// parsed (including the idle timeout).
			recordSSEEvent(metrics, telemetrySink, receivingCtx, sseEventTelemetry{
				Kind:     sseUnknownKind,
				Duration: time.Since(eventStartedAt),
				Err:      err,
			})
			// Rust's streaming consumer reports the same failure once on the
			// `response.completed` event kind (see_event_completed_failed).
			if telemetrySink != nil {
				telemetrySink.RecordSSEEventCompletedFailed(receivingCtx, err.Error())
			}
			handleResponsesSpan.End()
			return nil, err
		}
		var streamedEvent *ResponsesStreamEvent
		done, err := accumulator.apply(sse, func(event *ResponsesStreamEvent) {
			streamedEvent = event
			if handler != nil {
				handler(event)
			}
		})
		// Rust's log_sse_event routes by event content, so the failure record
		// carries the parsed payload (response.failed) or a fixed parse message
		// (an unparsable response.output_item.done) instead of Go's error text.
		recordKind, recordKindKnown := sseEventRecordKind(sse)
		record := sseEventTelemetry{
			Kind:      recordKind,
			KindKnown: recordKindKnown,
			Success:   err == nil,
			Duration:  time.Since(eventStartedAt),
			Err:       err,
		}
		if message := rustSSEEventFailureMessage(sse, err); message != "" {
			record.Success = false
			record.Message = message
		}
		recordSSEEvent(metrics, telemetrySink, receivingCtx, record)
		recordResponsesSpan(handleResponsesSpan, streamedEvent)
		if streamedEvent != nil && streamedEvent.Kind == ResponsesStreamEventOutputAdded && ttftMillis == nil {
			elapsed := time.Since(streamStartedAt).Milliseconds()
			ttftMillis = &elapsed
		}
		if streamedEvent != nil && streamedEvent.Kind == ResponsesStreamEventCompleted &&
			streamedEvent.Usage != nil && telemetrySink != nil {
			telemetrySink.RecordSSEEventCompleted(ctx, SSECompletedRecord{
				Usage:           *streamedEvent.Usage,
				TTFTMillis:      ttftMillis,
				ServiceTier:     requestServiceTier(request),
				ReasoningEffort: requestReasoningEffort(request),
			})
		}
		receivingSpan.End()
		handleResponsesSpan.End()
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

// emitUsageLimitErrorHeaderEvents mirrors Rust's `map_api_error`: a 429
// usage-limit error keeps the response's rate-limit headers on
// `UsageLimitReachedError.rate_limits`, and the turn loop then refreshes the
// session's snapshot from them (`sess.update_rate_limits`). Go has no error-side
// carrier, so it emits the same header-derived events its success path emits.
func emitUsageLimitErrorHeaderEvents(handler ResponsesStreamHandler, headers http.Header, err error) {
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorRateLimit {
		return
	}
	for _, snapshot := range parseResponsesRateLimits(headers) {
		rateLimit := snapshot
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:      ResponsesStreamEventRateLimits,
			RateLimit: &rateLimit,
			RawType:   string(ResponsesStreamEventRateLimits),
		})
	}
}

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
		Kind:      ResponsesStreamEventHeaders,
		RequestID: responseHeaderValue(headers, responsesRequestIDHeader, responsesOAIRequestIDHeader),
		Model:     responseHeaderValue(headers, responsesOpenAIModelHeader, responsesXOpenAIModelHeader),
		TurnState: responseHeaderValue(headers, responsesCodexTurnStateHeader),
		Headers:   cloneResponseHeaders(headers),
		RawType:   string(ResponsesStreamEventHeaders),
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
	if rawType == "codex.response.metadata" {
		if etag := modelsETagFromCodexResponseMetadata(sse.Data); etag != "" {
			emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
				Kind:       ResponsesStreamEventModelsETag,
				ModelsETag: etag,
				RawType:    string(ResponsesStreamEventModelsETag),
			})
		}
	}
	if rawType == "response.metadata" {
		a.emitMetadataEvents(sse.Data, handler)
	}
	// Rust calls `safety_buffering_for_event` for every event: a payload can carry
	// its own top-level `safety_buffering` field on any event kind, not only on a
	// metadata event.
	if buffering := safetyBufferingFromStreamMetadata(sse.Data, a.safetyBufferingTreatment); buffering != nil {
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:            ResponsesStreamEventSafetyBuffer,
			SafetyBuffering: buffering,
			RawType:         string(ResponsesStreamEventSafetyBuffer),
		})
	}
	switch rawType {
	case "error":
		// Rust #47967: a streamed `error` event whose code is
		// `flex_unavailable` is a terminal Flex-capacity failure; it ends the
		// turn without retries instead of being buffered as a generic stream
		// error.
		if message, ok := flexUnavailableStreamError(sse.Data); ok {
			return false, &codexapi.APIError{
				Kind:    codexapi.ErrorFlexUnavailable,
				Status:  http.StatusTooManyRequests,
				Message: message,
			}
		}
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
		a.applyToolInputDeltas(item)
		if item != nil && item.EncryptedFunctionArgs != nil {
			a.recordAgentItem(item)
		}
		emitResponsesStreamEvent(handler, &ResponsesStreamEvent{
			Kind:       ResponsesStreamEventOutputAdded,
			ResponseID: a.responseID,
			Item:       item,
			RawItem:    rawResponseItemFromStreamEventData(sse.Data),
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
		usageMetadata, hasUsageMetadata := usageMetadataFromStreamEventData(sse.Data)
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
		if hasUsageMetadata {
			event.UsageMetadata = usageMetadata
		}
		emitResponsesStreamEvent(handler, event)
		return true, nil
	case "response.incomplete":
		return false, responseIncompleteError(sse.Data)
	case "response.failed":
		return false, responseFailedError(sse.Data)
	}
	return false, nil
}

func responseIncompleteError(data []byte) error {
	reason := "unknown"
	var payload struct {
		Response struct {
			IncompleteDetails struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &payload) == nil {
		if value := strings.TrimSpace(payload.Response.IncompleteDetails.Reason); value != "" {
			reason = value
		}
	}
	return fmt.Errorf("Incomplete response returned, reason: %s", reason)
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
			if isGeneratedAgentMessageID(existingKey) || isGeneratedAgentMessageID(key) || key == "" || existingKey == "" {
				merged := mergeStreamAgentItem(a.items[i], *item)
				if isGeneratedAgentMessageID(existingKey) && key != "" && !isGeneratedAgentMessageID(key) {
					merged.ID = item.ID
				}
				a.items[i] = merged
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
	case "function_call", "custom_tool_call", "tool_search_call", "web_search_call":
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
	if merged.EncryptedFunctionArgs == nil {
		merged.EncryptedFunctionArgs = cloneAgentStringSlicePtr(existing.EncryptedFunctionArgs)
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
	a.restoreDeclaredCustomToolCall(item)
	switch item.Type {
	case "function_call":
		if accumulated := accumulatedToolInputDelta(a.functionCallArgDeltas, item.ID, item.CallID); accumulated != "" {
			item.Arguments = accumulated
		} else if item.Name == "apply_patch" {
			item.Arguments = firstNonEmptyResponseValue(
				accumulatedToolInputDelta(a.customToolInputDeltas, item.ID, item.CallID),
				soleAccumulatedToolInputDelta(a.customToolInputDeltas),
				soleAccumulatedToolInputDelta(a.functionCallArgDeltas),
			)
		}
	case "custom_tool_call":
		if accumulated := accumulatedToolInputDelta(a.customToolInputDeltas, item.ID, item.CallID); accumulated != "" {
			item.Input = accumulated
		} else if item.Name == "apply_patch" || a.isDeclaredCustomTool(item) {
			item.Input = firstNonEmptyResponseValue(
				item.Input,
				accumulatedToolInputDelta(a.functionCallArgDeltas, item.ID, item.CallID),
				soleAccumulatedToolInputDelta(a.customToolInputDeltas),
				soleAccumulatedToolInputDelta(a.functionCallArgDeltas),
			)
		}
	}
}

func (a *responsesStreamAccumulator) restoreDeclaredCustomToolCall(item *AgentItem) {
	if item == nil || item.Type != "function_call" || !a.isDeclaredCustomTool(item) {
		return
	}
	item.Type = "custom_tool_call"
	item.Input = firstNonEmptyResponseValue(
		accumulatedToolInputDelta(a.customToolInputDeltas, item.ID, item.CallID),
		item.Input,
		item.Arguments,
		accumulatedToolInputDelta(a.functionCallArgDeltas, item.ID, item.CallID),
		soleAccumulatedToolInputDelta(a.customToolInputDeltas),
		soleAccumulatedToolInputDelta(a.functionCallArgDeltas),
	)
	item.Arguments = ""
}

func (a *responsesStreamAccumulator) isDeclaredCustomTool(item *AgentItem) bool {
	if a == nil || item == nil || len(a.declaredCustomTools) == 0 {
		return false
	}
	_, ok := a.declaredCustomTools[responseToolDeclarationKey(item.Namespace, item.Name)]
	return ok
}

func declaredCustomResponseTools(request *AgentRequest) map[string]struct{} {
	if request == nil {
		return nil
	}
	declared := map[string]struct{}{}
	collectCustomResponseTools(declared, request.Tools)
	for _, input := range request.InputItems {
		item, ok := responseToolDefinitionMap(input)
		if !ok || !strings.EqualFold(strings.TrimSpace(responseToolString(item["type"])), "additional_tools") {
			continue
		}
		collectCustomResponseTools(declared, responseToolDefinitionSlice(item["tools"]))
	}
	if len(declared) == 0 {
		return nil
	}
	return declared
}

func collectCustomResponseTools(declared map[string]struct{}, tools []any) {
	for _, value := range tools {
		item, ok := responseToolDefinitionMap(value)
		if !ok || !strings.EqualFold(strings.TrimSpace(responseToolString(item["type"])), "custom") {
			continue
		}
		key := responseToolDeclarationKey(responseToolString(item["namespace"]), responseToolString(item["name"]))
		if key != "" {
			declared[key] = struct{}{}
		}
	}
}

func responseToolDefinitionMap(value any) (map[string]any, bool) {
	if item, ok := value.(map[string]any); ok {
		return item, true
	}
	normalized, ok := normalizeResponsesInputValue(value)
	if !ok {
		return nil, false
	}
	item, ok := normalized.(map[string]any)
	return item, ok
}

func responseToolDefinitionSlice(value any) []any {
	if tools, ok := value.([]any); ok {
		return tools
	}
	normalized, ok := normalizeResponsesInputValue(value)
	if !ok {
		return nil
	}
	tools, _ := normalized.([]any)
	return tools
}

func responseToolDeclarationKey(namespace string, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return strings.TrimSpace(namespace) + "\x00" + name
}

func soleAccumulatedToolInputDelta(values map[string]string) string {
	unique := ""
	for _, value := range values {
		if value == "" || value == unique {
			continue
		}
		if unique != "" {
			return ""
		}
		unique = value
	}
	return unique
}

func accumulatedToolInputDelta(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := values[key]; value != "" {
			return value
		}
	}
	return ""
}

func (a *responsesStreamAccumulator) emitMetadataEvents(data []byte, handler ResponsesStreamHandler) {
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

func safetyBufferingFromStreamMetadata(data []byte, treatment safetyBufferingTreatment) *ResponsesSafetyBuffering {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	// Rust 9558d830f6: a present top-level `safety_buffering` field is the
	// authoritative value, including when it is null or malformed.
	if value, ok := payload["safety_buffering"]; ok {
		parsed, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		return safetyBufferingFromPayload(parsed, treatment)
	}
	// Fallback: typed `response.metadata` events whose metadata object declares
	// `"type": "safety_buffering"` carry the payload directly on the metadata.
	metadata := streamMetadataObject(payload)
	if metadata == nil {
		return nil
	}
	if text, _ := metadata["type"].(string); text != "safety_buffering" {
		return nil
	}
	return safetyBufferingFromPayload(metadata, treatment)
}

// safetyBufferingFromPayload mirrors Rust's `SafetyBuffering` deserialization
// plus `ResponsesStreamEvent::safety_buffering`: both wire arrays are required,
// the payload's own wire `retry_model` wins, and the response's header treatment
// supplies the model only when the payload omits it. Rust marks
// `show_buffering_ui` as `#[serde(skip)]` and always sets it, so a delivered
// payload always asks the UI to show.
func safetyBufferingFromPayload(payload map[string]any, treatment safetyBufferingTreatment) *ResponsesSafetyBuffering {
	useCases, useCasesOK := jsonStringArrayField(payload, "use_cases", "useCases")
	reasons, reasonsOK := jsonStringArrayField(payload, "reasons")
	if !useCasesOK || !reasonsOK {
		return nil
	}
	fasterModel, fasterModelPresent, fasterModelOK := jsonOptionalStringField(payload, "retry_model", "faster_model", "fasterModel")
	if !fasterModelOK {
		return nil
	}
	if !fasterModelPresent {
		fasterModel = cloneStringPointer(treatment.fasterModel)
	}
	buffering := &ResponsesSafetyBuffering{
		Model:           stringFromAnyMap(payload, "model"),
		UseCases:        useCases,
		Reasons:         reasons,
		ShowBufferingUI: true,
		FasterModel:     fasterModel,
	}
	return buffering
}

// jsonStringArrayField mirrors a required `Vec<String>` field: the key must be
// present and hold an array whose elements are all strings.
func jsonStringArrayField(payload map[string]any, keys ...string) ([]string, bool) {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		values, ok := value.([]any)
		if !ok {
			return nil, false
		}
		out := make([]string, 0, len(values))
		for _, item := range values {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	}
	return nil, false
}

// jsonOptionalStringField mirrors an `Option<String>` field: absent means the
// field was omitted, present with null means an explicit null (which suppresses
// the treatment fallback), and any other non-string value makes the whole
// payload invalid.
func jsonOptionalStringField(payload map[string]any, keys ...string) (*string, bool, bool) {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		if value == nil {
			return nil, true, true
		}
		text, ok := value.(string)
		if !ok {
			return nil, true, false
		}
		return &text, true, true
	}
	return nil, false, true
}

func streamMetadataObject(payload map[string]any) map[string]any {
	if metadata, ok := payload["metadata"].(map[string]any); ok {
		return metadata
	}
	if response, ok := payload["response"].(map[string]any); ok {
		if metadata, ok := response["metadata"].(map[string]any); ok {
			return metadata
		}
	}
	return nil
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

func usageMetadataFromStreamEventData(data []byte) (*ResponseUsageMetadata, bool) {
	var payload struct {
		Response struct {
			UsageMetadata *ResponseUsageMetadata `json:"usage_metadata"`
			Usage         json.RawMessage        `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, false
	}
	metadata := payload.Response.UsageMetadata
	if len(payload.Response.Usage) > 0 && !bytes.Equal(payload.Response.Usage, []byte("null")) {
		if metadata == nil {
			metadata = &ResponseUsageMetadata{}
		}
		metadata.Metadata = payload.Response.Usage
	}
	if metadata == nil {
		return nil, false
	}
	return metadata, true
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

// modelsETagFromCodexResponseMetadata reads the model catalog ETag from a
// codex.response.metadata WebSocket event (Rust #38251).
func modelsETagFromCodexResponseMetadata(data []byte) string {
	var payload struct {
		Headers map[string]any `json:"headers"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	return stringFromHeaderMap(payload.Headers, responsesModelsETagHeader)
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

// responsesFailedErrorBody mirrors Rust's `sse::responses_error::Error` (#48229):
// the `response.error` object of a `response.failed` event. Every field keeps
// Rust's type - including the ones this classification does not read - because a
// malformed value anywhere makes Rust's `serde_json::from_value::<Error>` fail
// and the whole event degrade to the stream-error fallback.
type responsesFailedErrorBody struct {
	Type         *string         `json:"type"`
	Code         *string         `json:"code"`
	Message      *string         `json:"message"`
	PlanType     *string         `json:"plan_type"`
	ResetsAt     *int64          `json:"resets_at"`
	Misalignment json.RawMessage `json:"misalignment"`
}

func responseFailedError(data []byte) error {
	var payload struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return errResponsesStreamFailed
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	// A missing, null or non-object `response` carries no `error` field, which is
	// Rust's `Value::get("error")` returning None.
	if len(payload.Response) == 0 || json.Unmarshal(payload.Response, &envelope) != nil {
		return errResponsesStreamFailed
	}
	rawError := bytes.TrimSpace(envelope.Error)
	if len(rawError) == 0 || string(rawError) == "null" {
		return errResponsesStreamFailed
	}
	// Rust checks the Flex shape with `Value::get("code")` before decoding the
	// strict error body, so a Flex-capacity failure ends the turn even when
	// another field is malformed (Rust #47967, #48229).
	if _, message, ok := flexUnavailableErrorBody(rawError); ok {
		return &codexapi.APIError{
			Kind:    codexapi.ErrorFlexUnavailable,
			Status:  http.StatusTooManyRequests,
			Message: message,
		}
	}
	var errBody responsesFailedErrorBody
	if err := json.Unmarshal(rawError, &errBody); err != nil {
		return errResponsesStreamFailed
	}
	code := ""
	if errBody.Code != nil {
		code = strings.TrimSpace(*errBody.Code)
	}
	message := ""
	if errBody.Message != nil {
		message = strings.TrimSpace(*errBody.Message)
	}
	responsesDiagnostic("response.failed", map[string]any{"code": code, "message": message})
	switch code {
	case "context_length_exceeded":
		return &codexapi.APIError{
			Kind:    codexapi.ErrorContextWindowExceeded,
			Status:  http.StatusBadRequest,
			Message: message,
		}
	case "insufficient_quota", "credit_balance_exhausted", "organization_spend_limit_exceeded", "project_spend_limit_exceeded":
		// Rust #45602: exhausted credit balances and spend limits are quota
		// exhaustion, so they terminate without retries like insufficient_quota.
		return &codexapi.APIError{Kind: codexapi.ErrorQuotaExceeded, Message: message}
	case "usage_not_included":
		return &codexapi.APIError{Kind: codexapi.ErrorUsageNotIncluded, Message: message}
	case "cyber_policy":
		return &codexapi.APIError{Kind: codexapi.ErrorCyberPolicy, Message: fallbackPolicyMessage(message, CyberPolicyFallbackMessage)}
	case "bio_policy":
		// Rust #46306: streaming bio-policy failures keep their own
		// classification, with the server message preserved and a
		// biological-risk fallback when it is missing or blank.
		if strings.TrimSpace(message) == "" {
			message = BioPolicyFallbackMessage
		}
		return &codexapi.APIError{Kind: codexapi.ErrorBioPolicy, Status: http.StatusBadRequest, Message: message}
	case "misalignment_policy_violation":
		if strings.TrimSpace(message) == "" {
			message = "This request was blocked due to a misalignment policy violation."
		}
		return &codexapi.APIError{Kind: codexapi.ErrorMisalignmentPolicyViolation, Status: http.StatusBadRequest, Message: message, Misalignment: parseMisalignmentDetails(errBody.Misalignment)}
	case "invalid_prompt":
		if errBody.Message == nil {
			message = "Invalid request."
		}
		return &codexapi.APIError{Kind: codexapi.ErrorInvalidRequest, Message: message}
	case "server_is_overloaded":
		return &codexapi.APIError{Kind: codexapi.ErrorServerOverloaded, Message: message}
	}
	// Rust #45602: `slow_down` is a retryable rate limit, not a terminal server
	// overload.
	if code == "rate_limit_exceeded" || code == "slow_down" {
		retryable := &codexapi.APIError{Kind: codexapi.ErrorRateLimitExceeded, Message: message}
		if delay, ok := responseFailedRetryDelay(code, message); ok {
			return retryable.WithRetryDelay(delay)
		}
		return retryable
	}
	// Rust's fallback arm reports an unclassified failure as retryable; a
	// malformed payload never reaches here (it degrades to the sentinel above).
	retryable := &codexapi.APIError{Kind: codexapi.ErrorRetryable, Message: message}
	if delay, ok := responseFailedRetryDelay(code, message); ok {
		return retryable.WithRetryDelay(delay)
	}
	return retryable
}

// flexUnavailableErrorBody recognizes Rust's `parse_flex_unavailable`
// (`Value::get("code") == "flex_unavailable"`) without requiring the rest of the
// error object to decode.
func flexUnavailableErrorBody(rawError []byte) (string, string, bool) {
	var body struct {
		Code    *string `json:"code"`
		Message *string `json:"message"`
	}
	if err := json.Unmarshal(rawError, &body); err != nil || body.Code == nil || *body.Code != "flex_unavailable" {
		return "", "", false
	}
	if body.Message == nil {
		return "", "", true
	}
	return *body.Code, strings.TrimSpace(*body.Message), true
}

func parseMisalignmentDetails(raw json.RawMessage) *codexapi.MisalignmentDetails {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || string(raw) == "null" {
		return nil
	}
	var details struct {
		ErrorType           *string `json:"error_type"`
		DetailedExplanation *string `json:"detailed_explanation"`
		Steer               *struct {
			Message string `json:"message"`
		} `json:"steer"`
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		return nil
	}
	out := &codexapi.MisalignmentDetails{ErrorType: details.ErrorType, DetailedExplanation: details.DetailedExplanation}
	if details.Steer != nil {
		out.Steer = &codexapi.MisalignmentSteer{Message: details.Steer.Message}
	}
	return out
}

var responseFailedRetryDelayPattern = regexp.MustCompile(`(?i)try again in\s*(\d+(?:\.\d+)?)\s*(s|ms|seconds?)`)

func responseFailedRetryDelay(code, message string) (time.Duration, bool) {
	// Rust #45602: `slow_down` carries retry timing like a rate limit.
	if code != "rate_limit_exceeded" && code != "slow_down" {
		return 0, false
	}
	matches := responseFailedRetryDelayPattern.FindStringSubmatch(message)
	if len(matches) != 3 {
		return 0, false
	}
	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, false
	}
	if strings.EqualFold(matches[2], "ms") {
		return time.Duration(value * float64(time.Millisecond)), true
	}
	return time.Duration(value * float64(time.Second)), true
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
