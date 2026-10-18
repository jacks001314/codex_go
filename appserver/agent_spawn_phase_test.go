package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
	"codex_go/state"
	"codex_go/telemetry"
)

func TestRuntimeAgentControllerRecordsSuccessfulSpawnPhasesLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID:        "parent",
		SessionID: "parent",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: "/primary", AgentPath: "/root"},
	}); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	metrics := state.NewTaskMetrics()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
		TurnMetrics:  metrics,
	})
	controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "", "", "/primary", 4, agent.VersionV2, nil).(*runtimeAgentController)
	noFork := "none"
	if _, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker", ForkTurns: &noFork}); err != nil {
		t.Fatalf("SpawnAgent() error = %v", err)
	}

	phases := map[string]map[string]string{}
	for _, record := range metrics.Records() {
		if record.Name == telemetry.MultiAgentSpawnPhaseDurationMetric {
			phases[record.Tags["phase"]] = record.Tags
		}
	}
	for _, phase := range []string{
		telemetry.AgentSpawnPhaseResidencyReservation,
		telemetry.AgentSpawnPhaseChildCreate,
		telemetry.AgentSpawnPhaseDurabilityWait,
		telemetry.AgentSpawnPhaseInputAdmission,
		telemetry.AgentSpawnPhaseTotal,
	} {
		tags, ok := phases[phase]
		if !ok {
			t.Fatalf("missing %s phase in %#v", phase, phases)
		}
		if tags["multi_agent_version"] != string(agent.VersionV2) ||
			tags["fork_mode"] != telemetry.AgentSpawnFailureForkModeNone ||
			tags["history_mode"] != string(ThreadHistoryLegacy) {
			t.Fatalf("%s tags = %#v", phase, tags)
		}
	}
	if _, ok := phases[telemetry.AgentSpawnPhaseForkContext]; ok {
		t.Fatal("fresh spawn emitted a fork_context phase")
	}

	// A failed spawn must not emit partial phase timings.
	before := len(phases)
	if _, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker", ForkTurns: &noFork}); err == nil {
		t.Fatal("duplicate agent path succeeded")
	}
	after := 0
	for _, record := range metrics.Records() {
		if record.Name == telemetry.MultiAgentSpawnPhaseDurationMetric {
			after++
		}
	}
	if after != before {
		t.Fatalf("failed spawn emitted phase timings: before=%d after=%d", before, after)
	}
}
