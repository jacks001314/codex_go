package telemetry

import (
	"time"
)

// Multi-agent spawn phase names recorded after a successful spawn (Rust #47984).
const (
	AgentSpawnPhaseResidencyReservation = "residency_reservation"
	AgentSpawnPhaseForkContext          = "fork_context"
	AgentSpawnPhaseChildCreate          = "child_create"
	AgentSpawnPhaseDurabilityWait       = "durability_wait"
	AgentSpawnPhaseInputAdmission       = "input_admission"
	AgentSpawnPhaseTotal                = "total"
)

// AgentSpawnPhaseTags are the bounded dimensions shared by every phase sample.
type AgentSpawnPhaseTags struct {
	ForkMode          string
	MultiAgentVersion string
	HistoryMode       string
	ProductSKU        string
}

// EmitMultiAgentSpawnPhases records phase durations only after a spawn has
// succeeded. Messages, thread ids and other unbounded values never become
// metric labels (Rust #47984).
func EmitMultiAgentSpawnPhases(sink TurnMetricSink, tags AgentSpawnPhaseTags, phases map[string]time.Duration) {
	if sink == nil || len(phases) == 0 {
		return
	}
	order := []string{
		AgentSpawnPhaseResidencyReservation,
		AgentSpawnPhaseForkContext,
		AgentSpawnPhaseChildCreate,
		AgentSpawnPhaseDurabilityWait,
		AgentSpawnPhaseInputAdmission,
		AgentSpawnPhaseTotal,
	}
	for _, phase := range order {
		duration, ok := phases[phase]
		if !ok {
			continue
		}
		tagsForPhase := map[string]string{
			"phase":               phase,
			"fork_mode":           tags.ForkMode,
			"multi_agent_version": tags.MultiAgentVersion,
			"history_mode":        tags.HistoryMode,
		}
		if tags.ProductSKU != "" {
			tagsForPhase["product_sku"] = tags.ProductSKU
		}
		sink.RecordDuration(MultiAgentSpawnPhaseDurationMetric, duration, tagsForPhase)
	}
}
