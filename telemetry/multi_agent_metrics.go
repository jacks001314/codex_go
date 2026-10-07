package telemetry

// Rust parity: the tag vocabulary of the multi-agent telemetry series.
//
// codex-rs/core/src/tools/handlers/multi_agents_v2/wait.rs (#51332) tags the
// wait duration with the outcome the wait observed: mailbox activity, a user
// steer, or the timeout. Go's activity mailbox has no steer-only wakeup (the
// notification path is agent activity only), so WaitOutcomeSteered is part of
// the upstream vocabulary but is not reachable from the Go wait handler yet.
const (
	MultiAgentWaitOutcomeMailbox  = "mailbox"
	MultiAgentWaitOutcomeSteered  = "steered"
	MultiAgentWaitOutcomeTimedOut = "timed_out"
)
