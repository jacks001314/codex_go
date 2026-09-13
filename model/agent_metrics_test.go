package model

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
		context.Background(), reader, &AgentRequest{Prompt: "hello", Model: "gpt-test"}, "openai", nil, sink)
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
	recordSSEEvent(nil, "response.created", true, time.Second)
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
