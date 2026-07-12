package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	analyticsEventsQueueSize = 256
	analyticsEventsTimeout   = 10 * time.Second
)

type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

type AnalyticsAuthorizeRequestFunc func(ctx context.Context, request *http.Request, body []byte) (bool, error)

type TrackEventsRequest struct {
	Events []json.RawMessage `json:"events"`
}

type AnalyticsEventsClientOptions struct {
	BaseURL          string
	AuthHeaders      http.Header
	AuthorizeRequest AnalyticsAuthorizeRequestFunc
	HTTPClient       HTTPDoer
	AnalyticsEnabled *bool
	QueueSize        int
	Timeout          time.Duration
}

type AnalyticsEventsClient struct {
	exporter *HTTPAnalyticsExporter

	mu      sync.RWMutex
	queue   chan any
	cancel  context.CancelFunc
	done    chan struct{}
	closed  bool
	closeMu sync.Once
}

type HTTPAnalyticsExporter struct {
	url              string
	authHeaders      http.Header
	authorizeRequest AnalyticsAuthorizeRequestFunc
	httpClient       HTTPDoer
	timeout          time.Duration
}

func NewAnalyticsEventsClient(options AnalyticsEventsClientOptions) *AnalyticsEventsClient {
	if options.AnalyticsEnabled != nil && !*options.AnalyticsEnabled {
		return &AnalyticsEventsClient{}
	}
	exporter := NewHTTPAnalyticsExporter(options)
	if exporter == nil {
		return &AnalyticsEventsClient{}
	}
	queueSize := options.QueueSize
	if queueSize <= 0 {
		queueSize = analyticsEventsQueueSize
	}
	ctx, cancel := context.WithCancel(context.Background())
	client := &AnalyticsEventsClient{
		exporter: exporter,
		queue:    make(chan any, queueSize),
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go client.run(ctx, client.queue)
	return client
}

func DisabledAnalyticsEventsClient() *AnalyticsEventsClient {
	return &AnalyticsEventsClient{}
}

func NewHTTPAnalyticsExporter(options AnalyticsEventsClientOptions) *HTTPAnalyticsExporter {
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	if baseURL == "" {
		return nil
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = analyticsEventsTimeout
	}
	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPAnalyticsExporter{
		url:              baseURL + "/codex/analytics-events/events",
		authHeaders:      cloneHTTPHeader(options.AuthHeaders),
		authorizeRequest: options.AuthorizeRequest,
		httpClient:       client,
		timeout:          timeout,
	}
}

func (c *AnalyticsEventsClient) TrackCodexTurnEvent(ctx context.Context, event CodexTurnEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexThreadInitializedEvent(ctx context.Context, event CodexThreadInitializedEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexTurnSteerEvent(ctx context.Context, event CodexTurnSteerEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexCompactionEvent(ctx context.Context, event CodexCompactionEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexGoalEvent(ctx context.Context, event CodexGoalEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexPluginInstalledEvent(ctx context.Context, event CodexPluginEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexPluginUninstalledEvent(ctx context.Context, event CodexPluginEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexPluginEnabledEvent(ctx context.Context, event CodexPluginEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexPluginDisabledEvent(ctx context.Context, event CodexPluginEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexPluginInstallFailedEvent(ctx context.Context, event CodexPluginInstallFailedEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexOnboardingExternalAgentImportCompleteEvent(ctx context.Context, event CodexOnboardingExternalAgentImportCompleteEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexOnboardingExternalAgentImportFailureEvent(ctx context.Context, event CodexOnboardingExternalAgentImportFailureEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexHookRunEvent(ctx context.Context, event CodexHookRunEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexAcceptedLineFingerprintsEvent(ctx context.Context, event CodexAcceptedLineFingerprintsEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexCommandExecutionEvent(ctx context.Context, event CodexCommandExecutionEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexFileChangeEvent(ctx context.Context, event CodexFileChangeEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexReviewEvent(ctx context.Context, event CodexReviewEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexMCPToolCallEvent(ctx context.Context, event CodexMCPToolCallEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexDynamicToolCallEvent(ctx context.Context, event CodexDynamicToolCallEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexCollabAgentToolCallEvent(ctx context.Context, event CodexCollabAgentToolCallEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexWebSearchEvent(ctx context.Context, event CodexWebSearchEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackCodexImageGenerationEvent(ctx context.Context, event CodexImageGenerationEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) TrackSkillInvocationEvent(ctx context.Context, event SkillInvocationEventRequest) {
	c.trackEvent(event)
}

func (c *AnalyticsEventsClient) trackEvent(event any) {
	if c == nil {
		return
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || c.queue == nil {
		return
	}
	select {
	case c.queue <- event:
	default:
		slog.Warn("dropping analytics event: queue is full")
	}
}

func (c *AnalyticsEventsClient) Close() error {
	if c == nil {
		return nil
	}
	c.closeMu.Do(func() {
		c.mu.Lock()
		c.closed = true
		cancel := c.cancel
		c.cancel = nil
		if c.queue != nil {
			close(c.queue)
			c.queue = nil
		}
		done := c.done
		c.mu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-time.After(time.Second):
				if cancel != nil {
					cancel()
				}
			}
		}
	})
	return nil
}

func (c *AnalyticsEventsClient) Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && c.queue != nil
}

func (c *AnalyticsEventsClient) run(ctx context.Context, queue <-chan any) {
	defer close(c.done)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-queue:
			if !ok {
				return
			}
			if err := c.exporter.SendTrackEvents(ctx, []any{event}); err != nil {
				slog.Warn("failed to send analytics events request", "error", err)
			}
		}
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexTurnEvent(ctx context.Context, event CodexTurnEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexThreadInitializedEvent(ctx context.Context, event CodexThreadInitializedEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexTurnSteerEvent(ctx context.Context, event CodexTurnSteerEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexCompactionEvent(ctx context.Context, event CodexCompactionEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexGoalEvent(ctx context.Context, event CodexGoalEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexPluginInstalledEvent(ctx context.Context, event CodexPluginEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexPluginUninstalledEvent(ctx context.Context, event CodexPluginEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexPluginEnabledEvent(ctx context.Context, event CodexPluginEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexPluginDisabledEvent(ctx context.Context, event CodexPluginEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexPluginInstallFailedEvent(ctx context.Context, event CodexPluginInstallFailedEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexOnboardingExternalAgentImportCompleteEvent(ctx context.Context, event CodexOnboardingExternalAgentImportCompleteEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexOnboardingExternalAgentImportFailureEvent(ctx context.Context, event CodexOnboardingExternalAgentImportFailureEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexHookRunEvent(ctx context.Context, event CodexHookRunEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexAcceptedLineFingerprintsEvent(ctx context.Context, event CodexAcceptedLineFingerprintsEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexCommandExecutionEvent(ctx context.Context, event CodexCommandExecutionEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexFileChangeEvent(ctx context.Context, event CodexFileChangeEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexReviewEvent(ctx context.Context, event CodexReviewEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexMCPToolCallEvent(ctx context.Context, event CodexMCPToolCallEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexDynamicToolCallEvent(ctx context.Context, event CodexDynamicToolCallEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexCollabAgentToolCallEvent(ctx context.Context, event CodexCollabAgentToolCallEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexWebSearchEvent(ctx context.Context, event CodexWebSearchEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackCodexImageGenerationEvent(ctx context.Context, event CodexImageGenerationEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) TrackSkillInvocationEvent(ctx context.Context, event SkillInvocationEventRequest) {
	if e == nil {
		return
	}
	if err := e.SendTrackEvents(ctx, []any{event}); err != nil {
		slog.Warn("failed to send analytics events request", "error", err)
	}
}

func (e *HTTPAnalyticsExporter) SendTrackEvents(ctx context.Context, events []any) error {
	if e == nil || strings.TrimSpace(e.url) == "" || len(events) == 0 {
		return nil
	}
	for _, batch := range trackEventRequestBatches(events) {
		if err := e.sendTrackEventsBatch(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}

func (e *HTTPAnalyticsExporter) sendTrackEventsBatch(ctx context.Context, events []any) error {
	if e == nil || strings.TrimSpace(e.url) == "" || len(events) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx := ctx
	if _, ok := ctx.Deadline(); !ok && e.timeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	request, err := e.newRequest(requestCtx, events)
	if err != nil || request == nil {
		return err
	}
	response, err := e.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(response.Body)
	return fmt.Errorf("analytics events request failed with status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
}

func trackEventRequestBatches(events []any) [][]any {
	batches := [][]any{}
	currentBatch := []any{}
	for _, event := range events {
		if event == nil {
			continue
		}
		if analyticsEventShouldSendIsolated(event) {
			if len(currentBatch) > 0 {
				batches = append(batches, currentBatch)
				currentBatch = []any{}
			}
			batches = append(batches, []any{event})
			continue
		}
		currentBatch = append(currentBatch, event)
	}
	if len(currentBatch) > 0 {
		batches = append(batches, currentBatch)
	}
	return batches
}

func analyticsEventShouldSendIsolated(event any) bool {
	switch event.(type) {
	case CodexAcceptedLineFingerprintsEventRequest, *CodexAcceptedLineFingerprintsEventRequest:
		return true
	default:
		return false
	}
}

func (e *HTTPAnalyticsExporter) newRequest(ctx context.Context, events []any) (*http.Request, error) {
	rawEvents, err := marshalTrackEvents(events)
	if err != nil {
		return nil, err
	}
	if len(rawEvents) == 0 {
		return nil, nil
	}
	payload := TrackEventsRequest{Events: rawEvents}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	addHTTPHeaders(request.Header, e.authHeaders)
	request.Header.Set("Content-Type", "application/json")
	if e.authorizeRequest != nil {
		ok, err := e.authorizeRequest(ctx, request, body)
		if err != nil || !ok {
			return nil, err
		}
		if strings.TrimSpace(request.Header.Get("Content-Type")) == "" {
			request.Header.Set("Content-Type", "application/json")
		}
	}
	return request, nil
}

func marshalTrackEvents(events []any) ([]json.RawMessage, error) {
	rawEvents := make([]json.RawMessage, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		data, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		rawEvents = append(rawEvents, json.RawMessage(data))
	}
	return rawEvents, nil
}

func cloneHTTPHeader(headers http.Header) http.Header {
	if headers == nil {
		return nil
	}
	clone := make(http.Header, len(headers))
	for key, values := range headers {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}

func addHTTPHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
