package telemetry

// Rust parity: the tag vocabulary of the multi-agent telemetry series.
//
// codex-rs/core/src/agent/control/completion.rs (#51331) tags the result-delivery
// counter with queued | failed, and
// codex-rs/core/src/tools/handlers/multi_agents_v2/wait.rs (#51332) tags the
// wait duration with the outcome the wait observed: mailbox activity, a user
// steer, or the timeout. Go's activity mailbox has no steer-only wakeup (the
// notification path is agent activity only), so WaitOutcomeSteered is part of
// the upstream vocabulary but is not reachable from the Go wait handler yet.
const (
	MultiAgentResultDeliveryOutcomeQueued = "queued"
	MultiAgentResultDeliveryOutcomeFailed = "failed"
	MultiAgentWaitOutcomeMailbox          = "mailbox"
	MultiAgentWaitOutcomeSteered          = "steered"
	MultiAgentWaitOutcomeTimedOut         = "timed_out"
)

// MultiAgentVersion values mirror Rust's protocol::MultiAgentVersion
// (`#[serde(rename_all = "snake_case")]`), which the turn analytics event
// serializes as `multi_agent_version` (Rust #51333).
const (
	MultiAgentVersionDisabled = "disabled"
	MultiAgentVersionV1       = "v1"
	MultiAgentVersionV2       = "v2"
)
