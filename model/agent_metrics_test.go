package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// recordingMetricsSink captures the API request metrics.
type recordingMetricsSink struct {
	counters  []recordedMetricSample
	durations []recordedMetricSample
}

type recordedMetricSample struct {
	name     string
	inc      int
	duration time.Duration
	tags     map[string]string
}

func (s *recordingMetricsSink) Counter(name string, inc int, tags map[string]string) {
	s.counters = append(s.counters, recordedMetricSample{name: name, inc: inc, tags: cloneStringTags(tags)})
}

func (s *recordingMetricsSink) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	s.durations = append(s.durations, recordedMetricSample{name: name, duration: duration, tags: cloneStringTags(tags)})
}

func cloneStringTags(tags map[string]string) map[string]string {
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}

// Mirrors SessionTelemetry::record_api_request's metric half: the counter and
// duration sample per attempt with the status ("none" for a transport failure)
// and success tags.
func TestRecordAPIRequestLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		status     int
		err        error
		wantStatus string
		wantOK     string
	}{
		{"success", http.StatusOK, nil, "200", "true"},
		{"server error", http.StatusInternalServerError, nil, "500", "false"},
		{"transport failure", 0, errors.New("offline"), "none", "false"},
	} {
		sink := &recordingMetricsSink{}
		runner := &ResponsesAgentRunner{Metrics: sink}
		runner.recordAPIRequest(testCase.status, testCase.err, 1500*time.Millisecond)
		if len(sink.counters) != 1 || len(sink.durations) != 1 {
			t.Fatalf("%s: samples = %#v / %#v", testCase.name, sink.counters, sink.durations)
		}
		counter := sink.counters[0]
		if counter.name != apiCallCountMetric || counter.inc != 1 ||
			counter.tags["status"] != testCase.wantStatus || counter.tags["success"] != testCase.wantOK {
			t.Fatalf("%s: counter = %#v", testCase.name, counter)
		}
		duration := sink.durations[0]
		if duration.name != apiCallDurationMetric || duration.duration != 1500*time.Millisecond ||
			duration.tags["status"] != testCase.wantStatus || duration.tags["success"] != testCase.wantOK {
			t.Fatalf("%s: duration = %#v", testCase.name, duration)
		}
	}

	// No sink installed: recording is skipped.
	(&ResponsesAgentRunner{}).recordAPIRequest(http.StatusOK, nil, time.Second)
}

// Every HTTP attempt records a sample, so a retried request reports both the
// failed and the successful status (Rust's RequestTelemetry::on_request fires
// per attempt).
func TestResponsesAgentRunnerRecordsAPIRequestPerAttempt(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			writer.Header().Set("Retry-After", "0")
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"resp-1",
			"model":"gpt-test",
			"output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	sink := &recordingMetricsSink{}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: server.URL + "/v1", RequestMaxRetries: 1},
		Metrics:  sink,
	})
	if _, err := runner.Run(context.Background(), &AgentRequest{Prompt: "hello", Model: "gpt-test"}); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d", attempts)
	}
	if len(sink.counters) != 2 || len(sink.durations) != 2 {
		t.Fatalf("samples = %#v / %#v", sink.counters, sink.durations)
	}
	if sink.counters[0].tags["status"] != "500" || sink.counters[0].tags["success"] != "false" {
		t.Fatalf("first attempt = %#v", sink.counters[0])
	}
	if sink.counters[1].tags["status"] != "200" || sink.counters[1].tags["success"] != "true" {
		t.Fatalf("second attempt = %#v", sink.counters[1])
	}
	// The measured duration is wall-clock and may round to zero on a fast
	// loopback request (Windows monotonic resolution), so only the tags are
	// pinned here.
	for index, duration := range sink.durations {
		if duration.name != apiCallDurationMetric || duration.duration < 0 {
			t.Fatalf("duration[%d] = %#v", index, duration)
		}
	}
	if sink.durations[0].tags["status"] != "500" || sink.durations[1].tags["status"] != "200" {
		t.Fatalf("durations = %#v", sink.durations)
	}
}

// Mirrors SessionTelemetry's sse_event metric half: one counter and one
// duration sample per processed SSE event, tagged by the event kind.
func TestParseResponsesStreamRecordsSSEEventsLikeRust(t *testing.T) {
	sink := &recordingMetricsSink{}
	_, err := parseResponsesStreamWithMetrics(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)),
		&AgentRequest{Prompt: "hello", Model: "gpt-test"},
		"openai",
		nil,
		sink,
		nil,
	)
	if err != nil {
		t.Fatalf("parseResponsesStreamWithMetrics() error = %v", err)
	}
	want := []string{"response.created", "response.output_item.done", "response.completed"}
	if len(sink.counters) != len(want) || len(sink.durations) != len(want) {
		t.Fatalf("samples = %#v", sink.counters)
	}
	for index, kind := range want {
		counter := sink.counters[index]
		if counter.name != sseEventCountMetric || counter.inc != 1 ||
			counter.tags["kind"] != kind || counter.tags["success"] != "true" {
			t.Fatalf("counter[%d] = %#v", index, counter)
		}
		if duration := sink.durations[index]; duration.name != sseEventDurationMetric || duration.tags["kind"] != kind {
			t.Fatalf("duration[%d] = %#v", index, duration)
		}
	}
}

// A read failure after a complete event records the failed sample with the
// unknown kind (Rust's sse_event_failed with no parsed event, e.g. the idle
// timeout).
func TestParseResponsesStreamRecordsFailedSSEEventLikeRust(t *testing.T) {
	sink := &recordingMetricsSink{}
	reader := &errorAfterReader{
		data: responsesSSE(`{"type":"response.created","response":{"id":"resp-1"}}`),
		err:  errors.New("connection reset"),
	}
	_, err := parseResponsesStreamWithMetrics(
		context.Background(), reader, &AgentRequest{Prompt: "hello", Model: "gpt-test"}, "openai", nil, sink, nil)
	if err == nil {
		t.Fatal("parseResponsesStreamWithMetrics() error = nil")
	}
	if len(sink.counters) != 2 {
		t.Fatalf("samples = %#v", sink.counters)
	}
	if sink.counters[0].tags["kind"] != "response.created" || sink.counters[0].tags["success"] != "true" {
		t.Fatalf("first sample = %#v", sink.counters[0])
	}
	failure := sink.counters[1]
	if failure.tags["kind"] != sseUnknownKind || failure.tags["success"] != "false" {
		t.Fatalf("failure sample = %#v", failure)
	}
}

// The kind tag falls back from the SSE event name to the JSON type and finally
// to "unknown"; a nil sink records nothing.
func TestSSEEventKindFallbackLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		event *responsesSSEEvent
		want  string
	}{
		{"event name", &responsesSSEEvent{Event: "response.created"}, "response.created"},
		{"json type fallback", &responsesSSEEvent{Data: []byte(`{"type":"response.completed"}`)}, "response.completed"},
		{"unknown", &responsesSSEEvent{}, sseUnknownKind},
		{"nil", nil, sseUnknownKind},
	} {
		if got := sseEventKind(testCase.event); got != testCase.want {
			t.Fatalf("%s: kind = %q, want %q", testCase.name, got, testCase.want)
		}
	}
	recordSSEEvent(nil, nil, context.Background(), sseEventTelemetry{Kind: "response.created", KindKnown: true, Success: true, Duration: time.Second})
}

// errorAfterReader serves its data and then fails, so the SSE parser surfaces a
// read error after the complete events.
type errorAfterReader struct {
	data   string
	err    error
	offset int
}

func (r *errorAfterReader) Read(target []byte) (int, error) {
	if r.offset < len(r.data) {
		read := copy(target, r.data[r.offset:])
		r.offset += read
		return read, nil
	}
	return 0, r.err
}

// The websocket path records one codex.websocket.event sample per received
// message and the six responses_api_* durations carried by a
// responsesapi.websocket_timing message (Rust's log_websocket_event +
// record_responses_websocket_timing_metrics).
func TestRunWebSocketRecordsEventsAndTimingMetricsLikeRust(t *testing.T) {
	timing := `{"type":"responsesapi.websocket_timing","timing_metrics":{
		"responses_duration_excl_engine_and_client_tool_time_ms":120,
		"engine_service_total_ms":340,
		"engine_iapi_ttft_total_ms":50,
		"engine_service_ttft_total_ms":60,
		"engine_iapi_tbt_across_engine_calls_ms":7.5,
		"engine_service_tbt_across_engine_calls_ms":8.25
	}}`
	completed := `{"type":"response.completed","response":{"id":"timing-1","output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_, _, _ = conn.Read(request.Context())
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(timing))
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(completed))
	}))
	defer server.Close()

	sink := &recordingMetricsSink{}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider:           &APIProvider{BaseURL: server.URL},
		SupportsWebsockets: true,
		Metrics:            sink,
	})
	if _, err := runner.RunWebSocket(context.Background(), &AgentRequest{Model: "gpt-test", Prompt: "hi"}); err != nil {
		t.Fatalf("RunWebSocket() error = %v", err)
	}

	events := map[string]string{}
	requests := 0
	for _, counter := range sink.counters {
		if counter.name == websocketRequestCountMetric {
			requests++
			if counter.tags["success"] != "true" {
				t.Fatalf("websocket request counter = %#v", counter)
			}
			continue
		}
		if counter.name != websocketEventCountMetric {
			t.Fatalf("unexpected counter %#v", counter)
		}
		events[counter.tags["kind"]] = counter.tags["success"]
	}
	if requests != 1 {
		t.Fatalf("websocket request counters = %d (all %#v)", requests, sink.counters)
	}
	for _, kind := range []string{"responsesapi.websocket_timing", "response.completed"} {
		if success, ok := events[kind]; !ok || success != "true" {
			t.Fatalf("websocket event %q missing: %#v", kind, events)
		}
	}

	durations := map[string]time.Duration{}
	requestDurations := 0
	for _, duration := range sink.durations {
		if duration.name == websocketEventDurationMetric {
			continue
		}
		if duration.name == websocketRequestDurationMetric {
			requestDurations++
			if duration.tags["success"] != "true" {
				t.Fatalf("websocket request duration = %#v", duration)
			}
			continue
		}
		durations[duration.name] = duration.duration
	}
	if requestDurations != 1 {
		t.Fatalf("websocket request durations = %d", requestDurations)
	}
	want := map[string]time.Duration{
		responsesAPIOverheadDurationMetric:          120 * time.Millisecond,
		responsesAPIInferenceTimeDurationMetric:     340 * time.Millisecond,
		responsesAPIEngineIAPITTFTDurationMetric:    50 * time.Millisecond,
		responsesAPIEngineServiceTTFTDurationMetric: 60 * time.Millisecond,
		responsesAPIEngineIAPITBTDurationMetric:     time.Duration(7.5 * float64(time.Millisecond)),
		responsesAPIEngineServiceTBTDurationMetric:  time.Duration(8.25 * float64(time.Millisecond)),
	}
	for name, wantDuration := range want {
		if durations[name] != wantDuration {
			t.Fatalf("%s = %v, want %v (all %#v)", name, durations[name], wantDuration, durations)
		}
	}
}

// The timing helper skips absent fields and tolerates the JSON number shapes.
func TestRecordResponsesTimingMetricsSkipsAbsentFields(t *testing.T) {
	// A failed websocket request send reports success=false and clamps the
	// measured duration; a runner without a sink records nothing.
	failureSink := &recordingMetricsSink{}
	(&ResponsesAgentRunner{Metrics: failureSink}).recordWebsocketRequest(errors.New("send failed"), -5*time.Second)
	if len(failureSink.counters) != 1 || failureSink.counters[0].tags["success"] != "false" ||
		failureSink.counters[0].name != websocketRequestCountMetric {
		t.Fatalf("counters = %#v", failureSink.counters)
	}
	if len(failureSink.durations) != 1 || failureSink.durations[0].duration != 0 ||
		failureSink.durations[0].name != websocketRequestDurationMetric {
		t.Fatalf("durations = %#v", failureSink.durations)
	}
	(&ResponsesAgentRunner{}).recordWebsocketRequest(nil, time.Second)

	sink := &recordingMetricsSink{}
	recordResponsesTimingMetrics(sink, []byte(`{"type":"responsesapi.websocket_timing","timing_metrics":{"engine_service_total_ms":12}}`))
	if len(sink.durations) != 1 || sink.durations[0].name != responsesAPIInferenceTimeDurationMetric ||
		sink.durations[0].duration != 12*time.Millisecond {
		t.Fatalf("durations = %#v", sink.durations)
	}
	recordResponsesTimingMetrics(nil, []byte(`{"type":"responsesapi.websocket_timing","timing_metrics":{"engine_service_total_ms":12}}`))
	recordWebsocketEvent(nil, "response.completed", true, time.Second)

	// A numeric string is not a Rust-set value; nothing is recorded. json.Number
	// values are accepted.
	recordResponsesTimingMetrics(sink, []byte(`{"type":"responsesapi.websocket_timing","timing_metrics":{"engine_iapi_ttft_total_ms":"nope"}}`))
	if len(sink.durations) != 1 {
		t.Fatalf("durations = %#v", sink.durations)
	}
	decoder := json.NewDecoder(strings.NewReader(`{"engine_service_total_ms":5}`))
	decoder.UseNumber()
	payload := map[string]any{}
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if milliseconds, ok := timingMetricMilliseconds(payload, "engine_service_total_ms"); !ok || milliseconds != 5 {
		t.Fatalf("json.Number milliseconds = %v ok = %v", milliseconds, ok)
	}
}

// The SSE loop emits Rust's diagnostic records beside the metrics: a successful
// event logs `codex.sse_event` with its kind and duration, and a failure logs
// the record and records a trace-safe event (with the unknown kind when the
// event never parsed).
func TestParseResponsesStreamRecordsSSEDiagnosticsLikeRust(t *testing.T) {
	sink := &recordingTelemetrySink{}
	_, err := parseResponsesStreamWithMetrics(
		context.Background(),
		strings.NewReader(responsesSSE(`{"type":"response.created","response":{"id":"resp-1"}}`)),
		&AgentRequest{Prompt: "hello", Model: "gpt-test"},
		"openai",
		nil,
		nil,
		sink,
	)
	if err != nil && err.Error() != "stream closed before response.completed" {
		t.Fatalf("parseResponsesStreamWithMetrics() error = %v", err)
	}
	if len(sink.logged) != 1 {
		t.Fatalf("records = %#v", sink.logged)
	}
	record := sink.logged[0]
	if record.name != "codex.sse_event" || record.fields["event.kind"] != "response.created" {
		t.Fatalf("record = %#v", record)
	}
	if _, ok := record.fields["duration_ms"]; !ok {
		t.Fatalf("record = %#v", record)
	}
	if len(sink.traced) != 0 {
		t.Fatalf("a successful event must not record a trace event: %#v", sink.traced)
	}

	failed := &recordingTelemetrySink{}
	_, err = parseResponsesStreamWithMetrics(
		context.Background(),
		&errorAfterReader{
			data: responsesSSE(`{"type":"response.created","response":{"id":"resp-1"}}`),
			err:  errors.New("connection reset"),
		},
		&AgentRequest{Prompt: "hello", Model: "gpt-test"},
		"openai",
		nil,
		nil,
		failed,
	)
	if err == nil {
		t.Fatal("parseResponsesStreamWithMetrics() error = nil")
	}
	if len(failed.logged) != 2 || len(failed.traced) != 1 {
		t.Fatalf("records = %#v traced = %#v", failed.logged, failed.traced)
	}
	failure := failed.logged[1]
	if _, ok := failure.fields["event.kind"]; ok {
		t.Fatalf("an unparsed event must not report a kind: %#v", failure)
	}
	if failure.fields["error.message"] != "connection reset" {
		t.Fatalf("failure record = %#v", failure)
	}
	if failed.traced[0].fields["event.kind"] != sseUnknownKind ||
		failed.traced[0].fields["error.message"] != "connection reset" {
		t.Fatalf("trace record = %#v", failed.traced[0])
	}
	// Rust's streaming consumer also reports the failure on the completed event
	// kind, once per stream.
	if len(failed.sseCompletedFailed) != 1 || failed.sseCompletedFailed[0] != "connection reset" {
		t.Fatalf("completed-failed records = %#v", failed.sseCompletedFailed)
	}
}

// recordingTelemetrySink captures the diagnostic records the client emits.
type recordingTelemetrySink struct {
	logged             []telemetryRecord
	traced             []telemetryRecord
	spans              []*recordingTelemetrySpan
	apiRequests        []APIRequestRecord
	websocketRequests  []WebsocketRequestRecord
	sseCompleted       []SSECompletedRecord
	sseCompletedFailed []string
	websocketConnects  []WebsocketConnectRecord
	authRecoveries     []AuthRecoveryRecord
}

// recordingTelemetrySpan captures one span's lifecycle, so the tests can assert
// the client's span tree without an exporter.
type recordingTelemetrySpan struct {
	name        string
	attributes  map[string]string
	recorded    map[string]string
	parent      *recordingTelemetrySpan
	ended       bool
	traceparent string
	tracestate  string
}

func (s *recordingTelemetrySpan) End() { s.ended = true }

func (s *recordingTelemetrySpan) Record(attributes map[string]string) {
	if s.recorded == nil {
		s.recorded = map[string]string{}
	}
	for key, value := range attributes {
		s.recorded[key] = value
	}
}

func (s *recordingTelemetrySpan) SetName(name string) { s.name = name }

func (s *recordingTelemetrySpan) TraceContext() (string, string, bool) {
	return s.traceparent, s.tracestate, s.traceparent != ""
}

func (s *recordingTelemetrySink) StartSpan(_ context.Context, parent TelemetrySpan, name string, attributes map[string]string) (context.Context, TelemetrySpan) {
	span := &recordingTelemetrySpan{name: name, attributes: attributes}
	if concrete, ok := parent.(*recordingTelemetrySpan); ok {
		span.parent = concrete
	}
	s.spans = append(s.spans, span)
	return context.Background(), span
}

func (s *recordingTelemetrySink) RecordAPIRequest(_ context.Context, record APIRequestRecord) {
	s.apiRequests = append(s.apiRequests, record)
}

func (s *recordingTelemetrySink) RecordWebsocketRequest(_ context.Context, record WebsocketRequestRecord) {
	s.websocketRequests = append(s.websocketRequests, record)
}

func (s *recordingTelemetrySink) RecordSSEEventCompleted(_ context.Context, record SSECompletedRecord) {
	s.sseCompleted = append(s.sseCompleted, record)
}

func (s *recordingTelemetrySink) RecordSSEEventCompletedFailed(_ context.Context, errorMessage string) {
	s.sseCompletedFailed = append(s.sseCompletedFailed, errorMessage)
}

func (s *recordingTelemetrySink) RecordWebsocketConnect(_ context.Context, record WebsocketConnectRecord) {
	s.websocketConnects = append(s.websocketConnects, record)
}

func (s *recordingTelemetrySink) RecordAuthRecovery(_ context.Context, record AuthRecoveryRecord) {
	s.authRecoveries = append(s.authRecoveries, record)
}

type telemetryRecord struct {
	name   string
	fields map[string]string
	only   map[string]string
}

// A completed streamed response reports its usage and time to first token: the
// record Rust emits from the streaming consumer (SessionTelemetry::sse_event_completed).
func TestParseResponsesStreamRecordsCompletedUsageLikeRust(t *testing.T) {
	sink := &recordingTelemetrySink{}
	_, err := parseResponsesStreamWithMetrics(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.output_item.added","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-1","output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":10}}}`,
		)),
		&AgentRequest{Prompt: "hello", Model: "gpt-test", ServiceTier: "priority", ReasoningEffort: "high"},
		"openai",
		nil,
		nil,
		sink,
	)
	if err != nil {
		t.Fatalf("parseResponsesStreamWithMetrics() error = %v", err)
	}
	if len(sink.sseCompleted) != 1 {
		t.Fatalf("completed records = %#v", sink.sseCompleted)
	}
	record := sink.sseCompleted[0]
	if record.Usage.InputTokens != 7 || record.Usage.OutputTokens != 3 || record.Usage.TotalTokens != 10 {
		t.Fatalf("record = %#v", record)
	}
	if record.Usage.CachedInputTokens != 2 || record.Usage.ReasoningOutputTokens != 1 {
		t.Fatalf("record = %#v", record)
	}
	if record.TTFTMillis == nil {
		t.Fatalf("record has no time to first token: %#v", record)
	}
	if record.ServiceTier != "priority" || record.ReasoningEffort != "high" {
		t.Fatalf("record = %#v", record)
	}

	// A completion without usage reports nothing (Rust only calls the emitter for
	// a response that carries usage).
	empty := &recordingTelemetrySink{}
	if _, err := parseResponsesStreamWithMetrics(
		context.Background(),
		strings.NewReader(responsesSSE(`{"type":"response.completed","response":{"id":"resp-1","output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`)),
		&AgentRequest{Prompt: "hello", Model: "gpt-test"},
		"openai",
		nil,
		nil,
		empty,
	); err != nil {
		t.Fatalf("parseResponsesStreamWithMetrics() error = %v", err)
	}
	if len(empty.sseCompleted) != 0 {
		t.Fatalf("completed records = %#v", empty.sseCompleted)
	}
}

// The client's streaming loop opens Rust's span tree: one receiving_stream per
// stream, one handle_responses per event (named after the event, carrying the
// recorded from/tool_name/usage fields) with one receiving child each.
func TestParseResponsesStreamOpensClientSpansLikeRust(t *testing.T) {
	sink := &recordingTelemetrySink{}
	_, err := parseResponsesStreamWithMetrics(
		context.Background(),
		strings.NewReader(responsesSSE(
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.output_item.done","item":{"id":"call-1","type":"function_call","name":"shell","call_id":"call-1","arguments":"{}"}}`,
			`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"total_tokens":10}}}`,
		)),
		&AgentRequest{Prompt: "hello", Model: "gpt-test", ReasoningEffort: "high"},
		"openai",
		nil,
		nil,
		sink,
	)
	if err != nil {
		t.Fatalf("parseResponsesStreamWithMetrics() error = %v", err)
	}
	// One stream span, then a handle_responses + receiving pair per event.
	if len(sink.spans) != 1+2*3 {
		t.Fatalf("spans = %#v", sink.spans)
	}
	stream := sink.spans[0]
	if stream.name != ReceivingStreamSpanName || stream.parent != nil || !stream.ended {
		t.Fatalf("stream span = %#v", stream)
	}
	for index, want := range []struct {
		name     string
		from     string
		toolName string
	}{
		{name: "created"},
		{name: "function_call", from: "output_item_done", toolName: "shell"},
		{name: "completed"},
	} {
		handleResponses := sink.spans[1+index*2]
		receiving := sink.spans[2+index*2]
		if handleResponses.name != want.name || handleResponses.parent != stream {
			t.Fatalf("handle_responses[%d] = %#v", index, handleResponses)
		}
		if handleResponses.attributes["codex.request.reasoning_effort"] != "high" {
			t.Fatalf("handle_responses[%d] attributes = %#v", index, handleResponses.attributes)
		}
		if handleResponses.recorded["from"] != want.from || handleResponses.recorded["tool_name"] != want.toolName {
			t.Fatalf("handle_responses[%d] recorded = %#v", index, handleResponses.recorded)
		}
		if receiving.name != ReceivingSpanName || receiving.parent != handleResponses || !receiving.ended {
			t.Fatalf("receiving[%d] = %#v", index, receiving)
		}
	}
	// The completed event records the usage fields Rust reports.
	completed := sink.spans[5]
	if completed.recorded["gen_ai.usage.input_tokens"] != "7" ||
		completed.recorded["gen_ai.usage.cache_read.input_tokens"] != "2" ||
		completed.recorded["gen_ai.usage.output_tokens"] != "3" ||
		completed.recorded["codex.usage.total_tokens"] != "10" {
		t.Fatalf("completed recorded = %#v", completed.recorded)
	}
}

func (s *recordingTelemetrySink) LogEvent(_ context.Context, name string, fields map[string]string, logOnly map[string]string) {
	s.logged = append(s.logged, telemetryRecord{name: name, fields: fields, only: logOnly})
}

func (s *recordingTelemetrySink) TraceEvent(_ context.Context, name string, fields map[string]string, traceOnly map[string]string) {
	s.traced = append(s.traced, telemetryRecord{name: name, fields: fields, only: traceOnly})
}

func (s *recordingTelemetrySink) LogAndTraceEvent(ctx context.Context, name string, fields map[string]string, logOnly map[string]string, traceOnly map[string]string) {
	s.LogEvent(ctx, name, fields, logOnly)
	s.TraceEvent(ctx, name, fields, traceOnly)
}
