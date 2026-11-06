package appserver

import (
	"context"
	"strings"
)

// Rust parity: codex-rs/core/src/agent/host/completion.rs (#59725).

// AgentTurnOutcome is the terminal state a host may need for result delivery and
// retries. Unlike the native parent notification it is reported for every
// observed thread, including turns a parent never spawned.
type AgentTurnOutcome struct {
	ThreadID            string
	TurnID              string
	Status              CollabAgentStatus
	ParentTurnID        string
	InitiatingAgentPath string
	ErrorMessage        string
}

// AgentTurnOutcomeReporter is installed by an embedding host. It is awaited
// after the terminal turn event is recorded; the callback must hand off
// promptly because graceful shutdown waits for outstanding reports.
type AgentTurnOutcomeReporter interface {
	ReportAgentTurnOutcome(ctx context.Context, outcome AgentTurnOutcome)
}

// reportAgentTurnOutcome hands a terminal outcome to the host reporter without
// blocking the turn, while keeping shutdown able to wait for in-flight reports.
func (r *RuntimeRouter) reportAgentTurnOutcome(outcome AgentTurnOutcome) {
	if r == nil || r.services.AgentTurnOutcomeReporter == nil {
		return
	}
	outcome.ThreadID = strings.TrimSpace(outcome.ThreadID)
	outcome.TurnID = strings.TrimSpace(outcome.TurnID)
	if outcome.ThreadID == "" || outcome.TurnID == "" {
		return
	}
	reporter := r.services.AgentTurnOutcomeReporter
	r.agentOutcomeWG.Add(1)
	go func() {
		defer r.agentOutcomeWG.Done()
		reporter.ReportAgentTurnOutcome(context.Background(), outcome)
	}()
}

// waitAgentTurnOutcomes blocks until every handoff finishes; Close calls it
// before tearing services down so a host can still deliver results.
func (r *RuntimeRouter) waitAgentTurnOutcomes() {
	if r == nil {
		return
	}
	r.agentOutcomeWG.Wait()
}
