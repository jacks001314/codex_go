package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"codex_go/tool"
)

// Rust #51355 (`c0c230e673`): `SessionTelemetry::record_multi_agent_spawn_failure`
// labels the spawn-failure counter with the reason plus two bounded
// classifications - the annotated failure origin (`detail`, `unknown` when the
// error carries none) and the error's semantic kind (`error_kind`) - and emits a
// trace-safe event carrying the same classifications with the call and turn
// ids. Messages and correlation ids stay out of the label set, so distinct
// private ids aggregate into one bounded series. Rust's
// `spawn_failure_metrics_preserve_reason_and_bound_diagnostic_labels`
// (otel/tests/suite/manager_metrics.rs) asserts the same aggregation; the first
// annotation winning is Rust's `AgentErrorContext` test case in core/agent.
func TestMultiAgentSpawnFailureBoundsLabelsLikeRust(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		body := map[string]any{}
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("span batch json error = %v payload=%s", err, payload)
		}
		bodies = append(bodies, body)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	traces := NewTracesClient(TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
		Now:            func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if !traces.Enabled() {
		t.Fatal("traces client is disabled")
	}
	tracer := traces.Tracer()
	if tracer == nil {
		t.Fatal("tracer is nil")
	}
	// The span carries no thread id: a thread-tagged span joins the package's
	// live-span index, and this test only needs the span the context carries.
	sessionSpan := tracer.StartSpan("session_loop", nil)
	if sessionSpan == nil {
		t.Fatal("session span is nil")
	}
	session := NewSessionTelemetry(SessionTelemetryMetadata{
		ConversationID: "thread-1",
		AppVersion:     "test",
		Originator:     "codex_app_server",
	})
	session.Tracer = tracer
	sink := &recordingTurnMetricSink{}

	for _, testCase := range []struct {
		origin string
		detail string
	}{
		{origin: string(tool.AgentErrorContextChildStartup), detail: "child_startup"},
		{origin: "", detail: AgentSpawnFailureDetailUnknown},
	} {
		for _, id := range []string{"private-first", "private-second"} {
			err := errors.New("spawn failed for " + id)
			if testCase.origin != "" {
				// The first annotation wins, so the outer stage in Rust's
				// `spawn_failure_context_survives_manager_and_fork_errors` cannot
				// replace the inner, more precise origin.
				err = tool.WithAgentErrorContext(err, tool.AgentErrorContext(testCase.origin))
				err = tool.WithAgentErrorContext(err, tool.AgentErrorContextForkHistory)
			}
			EmitMultiAgentSpawnFailure(WithSpan(context.Background(), sessionSpan), session, sink, AgentSpawnFailure{
				Reason:     AgentSpawnFailureReasonInternal,
				Detail:     AgentSpawnFailureDetail(err),
				ErrorKind:  "io",
				CallID:     id,
				TurnID:     id,
				ForkMode:   AgentSpawnFailureForkModeAll,
				Version:    "v2",
				ProductSKU: "codex",
			})
		}
	}

	// Two bounded series, two increments each: the four samples collapse onto the
	// detail label alone.
	counts := map[string]int{}
	for _, counter := range sink.counters {
		if counter.name != MultiAgentSpawnFailureMetric {
			t.Fatalf("counter name = %q, want %q", counter.name, MultiAgentSpawnFailureMetric)
		}
		if counter.value != 1 {
			t.Fatalf("counter increment = %d, want 1", counter.value)
		}
		for _, value := range counter.tags {
			if strings.HasPrefix(value, "private-") {
				t.Fatalf("correlation id leaked into the label set: %v", counter.tags)
			}
		}
		counts[sortedTagsKey(counter.tags)]++
	}
	bounded := func(detail string) string {
		return sortedTagsKey(map[string]string{
			"reason":              AgentSpawnFailureReasonInternal,
			"detail":              detail,
			"error_kind":          "io",
			"fork_mode":           AgentSpawnFailureForkModeAll,
			"multi_agent_version": "v2",
			"product_sku":         "codex",
		})
	}
	want := map[string]int{bounded("child_startup"): 2, bounded(AgentSpawnFailureDetailUnknown): 2}
	if len(counts) != len(want) {
		t.Fatalf("bounded series = %v, want %v", counts, want)
	}
	for key, count := range want {
		if counts[key] != count {
			t.Fatalf("series %q = %d, want %d (series=%v)", key, counts[key], count, counts)
		}
	}

	sessionSpan.End()
	if err := traces.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("span batches = %d, want 1", len(bodies))
	}
	events := 0
	for _, span := range exportedSpans(t, bodies[0]) {
		entries, _ := span["events"].([]any)
		for _, entry := range entries {
			event, ok := entry.(map[string]any)
			if !ok || event["name"] != MultiAgentSpawnFailureMetric {
				continue
			}
			events++
			attributes := spanEventAttributes(t, event)
			// The trace-safe half carries the correlation ids the metric labels
			// must not.
			for key, wanted := range map[string]string{
				"target":              OtelTraceSafeTarget,
				"event.name":          MultiAgentSpawnFailureMetric,
				"conversation.id":     "thread-1",
				"reason":              AgentSpawnFailureReasonInternal,
				"error_kind":          "io",
				"fork_mode":           AgentSpawnFailureForkModeAll,
				"multi_agent_version": "v2",
				"product_sku":         "codex",
			} {
				if got := attributes[key]; got != wanted {
					t.Fatalf("event attribute %q = %q, want %q (attributes=%v)", key, got, wanted, attributes)
				}
			}
			if attributes["detail"] != "child_startup" && attributes["detail"] != AgentSpawnFailureDetailUnknown {
				t.Fatalf("event detail = %q, want a bounded classification", attributes["detail"])
			}
			// The correlation ids ride the trace event even though they must not
			// become labels.
			if callID := attributes["call_id"]; !strings.HasPrefix(callID, "private-") {
				t.Fatalf("event call_id = %q, want the recorded call id", callID)
			}
			if attributes["turn_id"] != attributes["call_id"] {
				t.Fatalf("event turn_id = %q, want it to match call_id %q", attributes["turn_id"], attributes["call_id"])
			}
		}
	}
	if events != 4 {
		t.Fatalf("spawn failure trace events = %d, want 4", events)
	}
}

// sortedTagsKey renders one label set as a stable key, so a test can compare
// bounded series the way Rust's BTreeMap of attribute maps does.
func sortedTagsKey(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+tags[key])
	}
	return strings.Join(parts, ",")
}

// Rust #51355: the classification is side-effect free. A spawn failure recorded
// without a session or a metric sink must not panic, and a trace event with no
// enclosing span is dropped the way Rust's tracing layer drops it.
func TestMultiAgentSpawnFailureWithoutSinksLikeRust(t *testing.T) {
	sink := &recordingTurnMetricSink{}
	EmitMultiAgentSpawnFailure(context.Background(), nil, nil, AgentSpawnFailure{
		Reason: AgentSpawnFailureReasonInternal,
		Detail: AgentSpawnFailureDetailUnknown,
	})
	EmitMultiAgentSpawnFailure(context.Background(), NewSessionTelemetry(SessionTelemetryMetadata{}), sink, AgentSpawnFailure{
		Reason: AgentSpawnFailureReasonLimitReached,
		Detail: string(tool.AgentErrorContextRegistryCapacity),
	})
	if len(sink.counters) != 1 {
		t.Fatalf("counters = %d, want 1", len(sink.counters))
	}
	if got := sink.counters[0].tags["detail"]; got != string(tool.AgentErrorContextRegistryCapacity) {
		t.Fatalf("detail label = %q, want %q", got, tool.AgentErrorContextRegistryCapacity)
	}
}
