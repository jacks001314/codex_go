package appserver

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
	"codex_go/telemetry"
)

type subagentLifecycleRecordingSink struct {
	counters []struct {
		name string
		tags map[string]string
	}
}

func (s *subagentLifecycleRecordingSink) Counter(name string, inc int, tags map[string]string) {
	cloned := map[string]string{}
	for key, value := range tags {
		cloned[key] = value
	}
	s.counters = append(s.counters, struct {
		name string
		tags map[string]string
	}{name: name, tags: cloned})
}

func (s *subagentLifecycleRecordingSink) Histogram(string, int, map[string]string) {}

func (s *subagentLifecycleRecordingSink) HistogramWithBounds(string, int, []float64, map[string]string) {
}

func (s *subagentLifecycleRecordingSink) RecordDuration(string, time.Duration, map[string]string) {}

func (s *subagentLifecycleRecordingSink) count(name string, match func(map[string]string) bool) int {
	total := 0
	for _, counter := range s.counters {
		if counter.name == name && match(counter.tags) {
			total++
		}
	}
	return total
}

// Mirrors Rust #62567: a child turn attempt reports one start and at most one
// outcome even when the terminal notification repeats, and spawn/stop boundaries
// count independently.
func TestSubagentLifecycleAttemptsRecordOneStartAndOutcomeLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	now := time.Now().UTC()
	store := session.NewStore(filepath.Join(home, "sessions"))
	child := &session.Record{
		ID: "child", SessionID: "root", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: cwd, Model: "gpt-life", Source: string(SessionSourceAppServer), AgentPath: "/root/worker", AgentDepth: 1},
	}
	if err := store.Create(child); err != nil {
		t.Fatalf("Create(child) error = %v", err)
	}
	sink := &subagentLifecycleRecordingSink{}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		DefaultCWD:   cwd,
		TurnMetrics:  sink,
	})
	ctx := context.Background()
	router.recordSubagentSpawn(ctx, "root", "parent-turn", "child")
	router.recordSubagentTurnStarted(ctx, "child", "turn-1")
	router.recordSubagentTurnStarted(ctx, "child", "turn-1")
	router.recordSubagentTurnOutcome(ctx, "child", "turn-1", TurnStatusCompleted, nil)
	router.recordSubagentTurnOutcome(ctx, "child", "turn-1", TurnStatusFailed, nil)
	router.recordSubagentStopAccepted(ctx, "root", "root", "child", telemetry.SubagentStopActionClose, agent.AgentMessageStatus{Kind: agent.AgentMessageStatusShutdown})

	if got := sink.count(telemetry.MultiAgentSpawnedMetric, func(map[string]string) bool { return true }); got != 1 {
		t.Fatalf("spawned counter = %d, want 1", got)
	}
	if got := sink.count(telemetry.MultiAgentTurnLifecycleMetric, func(tags map[string]string) bool {
		return tags["status"] == telemetry.SubagentTurnStatusStarted && tags["turn_kind"] == telemetry.SubagentTurnKindInitial
	}); got != 1 {
		t.Fatalf("started counter = %d, want 1", got)
	}
	if got := sink.count(telemetry.MultiAgentTurnLifecycleMetric, func(tags map[string]string) bool {
		return tags["status"] == telemetry.SubagentTurnStatusCompleted
	}); got != 1 {
		t.Fatalf("completed counter = %d, want 1", got)
	}
	if got := sink.count(telemetry.MultiAgentTurnLifecycleMetric, func(tags map[string]string) bool {
		return tags["status"] == telemetry.SubagentTurnStatusFailed
	}); got != 0 {
		t.Fatalf("duplicate outcome was recorded: %d failed counters", got)
	}
	if got := sink.count(telemetry.MultiAgentStopAcceptedMetric, func(tags map[string]string) bool {
		return tags["caller"] == telemetry.SubagentStopCallerRoot &&
			tags["action"] == telemetry.SubagentStopActionClose &&
			tags["previous_status"] == string(agent.AgentMessageStatusShutdown)
	}); got != 1 {
		t.Fatalf("stop counter = %d, want 1 with bounded labels", got)
	}

	// Later attempts are follow-ups until an explicit resume marks the next one.
	router.recordSubagentTurnStarted(ctx, "child", "turn-2")
	if got := sink.count(telemetry.MultiAgentTurnLifecycleMetric, func(tags map[string]string) bool {
		return tags["status"] == telemetry.SubagentTurnStatusStarted && tags["turn_kind"] == telemetry.SubagentTurnKindFollowUp
	}); got != 1 {
		t.Fatalf("follow-up attempt counter = %d, want 1", got)
	}
	router.noteSubagentResume("child")
	router.recordSubagentTurnStarted(ctx, "child", "turn-3")
	if got := sink.count(telemetry.MultiAgentTurnLifecycleMetric, func(tags map[string]string) bool {
		return tags["status"] == telemetry.SubagentTurnStatusStarted && tags["turn_kind"] == telemetry.SubagentTurnKindResumedThread
	}); got != 1 {
		t.Fatalf("resumed attempt counter = %d, want 1", got)
	}

	// An interrupt keeps the validated abort reason and a failure keeps the
	// CodexErrorInfo class, both as bounded counter labels.
	router.recordSubagentTurnStarted(ctx, "child", "turn-4")
	router.subagentTurns.noteAbortReason("child", "turn-4", "replaced")
	router.recordSubagentTurnOutcome(ctx, "child", "turn-4", TurnStatusInterrupted, nil)
	if got := sink.count(telemetry.MultiAgentTurnLifecycleMetric, func(tags map[string]string) bool {
		return tags["status"] == telemetry.SubagentTurnStatusInterrupted && tags["abort_reason"] == "replaced"
	}); got != 1 {
		t.Fatalf("interrupted attempt with abort reason = %d, want 1", got)
	}
	router.recordSubagentTurnStarted(ctx, "child", "turn-5")
	router.recordSubagentTurnOutcome(ctx, "child", "turn-5", TurnStatusFailed, &TurnError{CodexErrorInfo: "tooManyDenials"})
	if got := sink.count(telemetry.MultiAgentTurnLifecycleMetric, func(tags map[string]string) bool {
		return tags["status"] == telemetry.SubagentTurnStatusFailed && tags["error_kind"] == "too_many_denials"
	}); got != 1 {
		t.Fatalf("failed attempt with error kind = %d, want 1", got)
	}

	// A parent thread never reports subagent turn lifecycle.
	root := &session.Record{
		ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: cwd, Model: "gpt-life", Source: string(SessionSourceCli), AgentPath: "/root"},
	}
	if err := store.Create(root); err != nil {
		t.Fatalf("Create(root) error = %v", err)
	}
	before := len(sink.counters)
	router.recordSubagentTurnStarted(ctx, "root", "root-turn")
	if len(sink.counters) != before {
		t.Fatalf("root turn reported subagent lifecycle counters: %#v", sink.counters[before:])
	}
}
