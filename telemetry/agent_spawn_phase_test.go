package telemetry

import (
	"testing"
	"time"
)

func TestMultiAgentSpawnPhaseMetricsUseBoundedLabelsLikeRust(t *testing.T) {
	sink := &recordingTurnMetricSink{}
	EmitMultiAgentSpawnPhases(sink, AgentSpawnPhaseTags{
		ForkMode:          AgentSpawnFailureForkModeAll,
		MultiAgentVersion: "v2",
		HistoryMode:       "paginated",
		ProductSKU:        "codex",
	}, map[string]time.Duration{
		AgentSpawnPhaseResidencyReservation: 2 * time.Millisecond,
		AgentSpawnPhaseForkContext:          3 * time.Millisecond,
		AgentSpawnPhaseChildCreate:          4 * time.Millisecond,
		AgentSpawnPhaseDurabilityWait:       5 * time.Millisecond,
		AgentSpawnPhaseInputAdmission:       6 * time.Millisecond,
		AgentSpawnPhaseTotal:                20 * time.Millisecond,
	})
	wantPhases := []string{
		AgentSpawnPhaseResidencyReservation,
		AgentSpawnPhaseForkContext,
		AgentSpawnPhaseChildCreate,
		AgentSpawnPhaseDurabilityWait,
		AgentSpawnPhaseInputAdmission,
		AgentSpawnPhaseTotal,
	}
	if len(sink.durations) != len(wantPhases) {
		t.Fatalf("duration count = %d, want %d", len(sink.durations), len(wantPhases))
	}
	for index, phase := range wantPhases {
		record := sink.durations[index]
		if record.name != MultiAgentSpawnPhaseDurationMetric || record.tags["phase"] != phase {
			t.Fatalf("duration %d = %#v, want phase %q", index, record, phase)
		}
		for key, want := range map[string]string{
			"fork_mode":           AgentSpawnFailureForkModeAll,
			"multi_agent_version": "v2",
			"history_mode":        "paginated",
			"product_sku":         "codex",
		} {
			if got := record.tags[key]; got != want {
				t.Fatalf("%s phase %s tag %s = %q, want %q", record.name, phase, key, got, want)
			}
		}
	}
}

func TestMultiAgentSpawnPhaseMetricsSkipAbsentPhases(t *testing.T) {
	sink := &recordingTurnMetricSink{}
	EmitMultiAgentSpawnPhases(sink, AgentSpawnPhaseTags{
		ForkMode:          AgentSpawnFailureForkModeNone,
		MultiAgentVersion: "v1",
		HistoryMode:       "legacy",
	}, map[string]time.Duration{
		AgentSpawnPhaseChildCreate: time.Millisecond,
		AgentSpawnPhaseTotal:       time.Millisecond,
	})
	if len(sink.durations) != 2 {
		t.Fatalf("duration count = %d, want child_create and total", len(sink.durations))
	}
	for _, record := range sink.durations {
		if _, ok := record.tags["product_sku"]; ok {
			t.Fatalf("absent product sku was emitted: %#v", record.tags)
		}
	}
}
