package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
	"codex_go/tool"
)

func spawnFailureCounters(metrics *state.TaskMetrics) []*state.TaskMetric {
	var out []*state.TaskMetric
	for _, record := range metrics.Records() {
		if record.Name == telemetry.MultiAgentSpawnFailureMetric {
			out = append(out, record)
		}
	}
	return out
}

// Rust #51355 (`c0c230e673`): the spawn path annotates each failure with a
// bounded origin so two failures that share an error kind and message still
// carry distinct diagnostics, and the recorded labels distinguish them without
// using the message. Rust's
// `spawn_failure_context_distinguishes_execution_and_residency_capacity`
// (core/src/agent/control_tests.rs) distinguishes capacities and
// `spawn_failure_context_distinguishes_registry_rejections` (registry_tests.rs)
// covers the registry arms; both assert the annotation preserves the error kind
// and the user-visible message.
func TestSpawnFailureContextDistinguishesCapacityAndRegistryLikeRust(t *testing.T) {
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
	traces := telemetry.NewTracesClient(telemetry.TracesClientOptions{
		ServiceName:    "codex-app-server",
		Endpoint:       server.URL + "/v1/traces",
		ExportInterval: -1,
		Now:            func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	tracer := traces.Tracer()
	if tracer == nil {
		t.Fatal("tracer is nil")
	}
	// The span carries no thread id: a thread-tagged span is registered in the
	// package's live-span index, and this test only needs the ctx the spawn path
	// reports through.
	span := tracer.StartSpan("session_loop", nil)
	if span == nil {
		t.Fatal("session span is nil")
	}
	ctx := telemetry.WithSpan(context.Background(), span)

	metrics := state.NewTaskMetrics()
	store := session.NewStore(t.TempDir())
	cwd := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
		TurnMetrics:  metrics,
	})
	controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "", "", cwd, 1, agent.VersionV2, nil).(*runtimeAgentController)
	registry := router.runtimeAgentRegistry(controller.rootID)

	// The session's only spawn slot is taken, so the next spawn is rejected for
	// capacity. Rust reports the same rejection as AgentLimitReached; the
	// annotation is what tells the two apart.
	reservation, err := registry.ReserveSpawnSlot(1)
	if err != nil {
		t.Fatalf("ReserveSpawnSlot(1) error = %v", err)
	}
	defer reservation.Cancel()
	_, capacityErr := controller.SpawnAgent(ctx, &agent.SpawnAgentArgs{TaskName: "worker"})
	if capacityErr == nil {
		t.Fatal("a taken spawn slot must reject the next spawn")
	}
	if got := capacityErr.Error(); got != "agent limit reached: max_threads=1" {
		t.Fatalf("capacity error message = %q, want the registry message unchanged", got)
	}
	if !errors.Is(capacityErr, agent.ErrAgentLimitReached) {
		t.Fatalf("capacity error = %v, want ErrAgentLimitReached preserved through the annotation", capacityErr)
	}
	if origin, ok := tool.AgentErrorContextOf(capacityErr); !ok || origin != tool.AgentErrorContextRegistryCapacity {
		t.Fatalf("capacity origin = %q (%v), want registry_capacity", origin, ok)
	}

	// A registry rejection with a different origin: the agent path is taken.
	duplicate := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "", "", cwd, 0, agent.VersionV2, nil).(*runtimeAgentController)
	taken := agent.AgentPath(runtimeCanonicalAgentPath(duplicate.scopePath, "dup"))
	registry.RegisterSpawnedThread(agent.Metadata{ThreadID: "existing", Path: taken, Nickname: "existing"})
	_, duplicateErr := duplicate.SpawnAgent(ctx, &agent.SpawnAgentArgs{TaskName: "dup"})
	if duplicateErr == nil {
		t.Fatal("a taken agent path must reject the spawn")
	}
	if !errors.Is(duplicateErr, agent.ErrAgentPathExists) {
		t.Fatalf("duplicate error = %v, want ErrAgentPathExists preserved", duplicateErr)
	}
	if origin, ok := tool.AgentErrorContextOf(duplicateErr); !ok || origin != tool.AgentErrorContextDuplicatePath {
		t.Fatalf("duplicate origin = %q (%v), want duplicate_path", origin, ok)
	}

	// A rejection that happens before the spawn call is not a spawn failure:
	// Rust returns respond-to-model for the depth limit without recording one.
	depth := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "", "", cwd, 0, agent.VersionV1, nil).(*runtimeAgentController)
	depth.maxDepth = 0
	_, depthErr := depth.SpawnAgent(ctx, &agent.SpawnAgentArgs{Message: stringPtr("go")})
	if !errors.Is(depthErr, agent.ErrAgentDepthLimitReached) {
		t.Fatalf("depth error = %v, want ErrAgentDepthLimitReached", depthErr)
	}
	if origin, ok := tool.AgentErrorContextOf(depthErr); ok {
		t.Fatalf("depth origin = %q, want no annotation before the spawn call", origin)
	}

	// Only the two spawn failures are recorded; the depth rejection above adds
	// nothing.
	counters := spawnFailureCounters(metrics)
	if len(counters) != 2 {
		t.Fatalf("spawn failure counters = %d, want 2 (the depth rejection records none)", len(counters))
	}
	wanted := []map[string]string{
		{
			"reason":              telemetry.AgentSpawnFailureReasonLimitReached,
			"detail":              string(tool.AgentErrorContextRegistryCapacity),
			"error_kind":          "agent_limit_reached",
			"fork_mode":           telemetry.AgentSpawnFailureForkModeAll,
			"multi_agent_version": string(agent.VersionV2),
		},
		{
			"reason":              telemetry.AgentSpawnFailureReasonUnsupportedOperation,
			"detail":              string(tool.AgentErrorContextDuplicatePath),
			"error_kind":          "unsupported_operation",
			"fork_mode":           telemetry.AgentSpawnFailureForkModeAll,
			"multi_agent_version": string(agent.VersionV2),
		},
	}
	for i, record := range counters {
		if record.Inc != 1 {
			t.Fatalf("counter %d increment = %d, want 1", i, record.Inc)
		}
		if !tagsEqual(record.Tags, wanted[i]) {
			t.Fatalf("counter %d tags = %v, want %v", i, record.Tags, wanted[i])
		}
	}

	// The trace-safe half carries the same classifications with the turn id.
	span.End()
	if err := traces.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("span batches = %d, want 1", len(bodies))
	}
	events := map[string]map[string]string{}
	for _, exported := range exportedSpans(t, bodies) {
		entries, _ := exported["events"].([]any)
		for _, entry := range entries {
			event, ok := entry.(map[string]any)
			if !ok || event["name"] != telemetry.MultiAgentSpawnFailureMetric {
				continue
			}
			attributes := encodedAttributes(event)
			events[attributes["detail"]] = attributes
		}
	}
	if len(events) != 2 {
		t.Fatalf("spawn failure trace events = %v, want one per bounded origin", events)
	}
	for _, detail := range []string{string(tool.AgentErrorContextRegistryCapacity), string(tool.AgentErrorContextDuplicatePath)} {
		attributes, ok := events[detail]
		if !ok {
			t.Fatalf("trace events = %v, want a %s classification", events, detail)
		}
		if attributes["turn_id"] != "parent-turn" {
			t.Fatalf("event turn_id = %q, want the spawning turn", attributes["turn_id"])
		}
		// Go's agent controller is turn-scoped rather than call-scoped, so the
		// call id Rust records has no Go value here.
		if attributes["call_id"] != "" {
			t.Fatalf("event call_id = %q, want empty (the controller is not call-scoped)", attributes["call_id"])
		}
	}
	if got := events[string(tool.AgentErrorContextRegistryCapacity)]["error_kind"]; got != "agent_limit_reached" {
		t.Fatalf("capacity event error_kind = %q, want agent_limit_reached", got)
	}
	if got := events[string(tool.AgentErrorContextDuplicatePath)]["reason"]; got != telemetry.AgentSpawnFailureReasonUnsupportedOperation {
		t.Fatalf("duplicate event reason = %q, want unsupported_operation", got)
	}
}

func tagsEqual(got map[string]string, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
