package appserver

import (
	"codex_go/telemetry"
)

// recordMultiAgentResultDelivery counts one terminal sub-agent result handed to
// its parent thread, tagged by the delivery outcome. Rust #51331
// (`41acdad246`, core/src/agent/control/completion.rs) increments
// `codex.multi_agent.result_delivery` with `outcome=queued` when the parent
// accepts the result and `outcome=failed` when delivery errors.
//
// Go's delivery has no reachable failure after its guard clauses: the mailbox
// enqueue only rejects a nil mailbox or empty thread/turn ids, and
// deliverRuntimeAgentCompletion returns early without them. The failed arm is
// kept so the series matches the upstream shape.
func (r *RuntimeRouter) recordMultiAgentResultDelivery(err error) {
	if r == nil || r.services.TurnMetrics == nil {
		return
	}
	outcome := telemetry.MultiAgentResultDeliveryOutcomeQueued
	if err != nil {
		outcome = telemetry.MultiAgentResultDeliveryOutcomeFailed
	}
	r.services.TurnMetrics.Counter(telemetry.MultiAgentResultDeliveryMetric, 1, map[string]string{"outcome": outcome})
}
