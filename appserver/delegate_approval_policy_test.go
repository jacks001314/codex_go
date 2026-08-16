package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

// TestPrepareTurnStartParamsForcesApprovalNeverForSubagentLikeRust is the
// dynamic verification for Rust 95aada11c4 (#38205): delegated (subagent)
// sessions always run with the `never` approval policy. A subagent thread's
// turn-start params are pinned to never regardless of the caller-supplied
// policy, while a normal thread keeps its explicit policy.
func TestPrepareTurnStartParamsForcesApprovalNeverForSubagentLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	subagent := &session.Record{ID: "subagent-thread", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{
		CWD:          t.TempDir(),
		ThreadSource: "subAgentThreadSpawn",
		Originator:   "subagent",
		Source:       "appServer",
	}}
	normal := &session.Record{ID: "normal-thread", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir()}}
	if err := store.Create(subagent); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(normal); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	defer router.Close()

	// The caller (parent session) uses on-request; the subagent must still be
	// pinned to never.
	subagentParams := &turn.TurnStartParams{ThreadID: "subagent-thread", ApprovalPolicy: sandbox.ApprovalOnRequest}
	if err := router.prepareTurnStartParams(subagentParams); err != nil {
		t.Fatalf("prepareTurnStartParams(subagent) error = %v", err)
	}
	if subagentParams.ApprovalPolicy != sandbox.ApprovalNever {
		t.Fatalf("subagent approval policy = %q, want never (#38205)", subagentParams.ApprovalPolicy)
	}
	if policy := turnApprovalPolicyForTurn(nil, subagentParams); policy != sandbox.ApprovalNever {
		t.Fatalf("subagent effective approval policy = %q, want never", policy)
	}

	// A normal thread keeps the caller-supplied policy.
	normalParams := &turn.TurnStartParams{ThreadID: "normal-thread", ApprovalPolicy: sandbox.ApprovalOnRequest}
	if err := router.prepareTurnStartParams(normalParams); err != nil {
		t.Fatalf("prepareTurnStartParams(normal) error = %v", err)
	}
	if normalParams.ApprovalPolicy != sandbox.ApprovalOnRequest {
		t.Fatalf("normal thread approval policy = %q, want on-request", normalParams.ApprovalPolicy)
	}
}

// TestRuntimeAgentControllerSpawnPinsSubagentApprovalNever verifies the
// enforcement end-to-end through the agent-tool spawn path: the subagent
// thread created by SpawnAgent carries the never approval policy on its
// prepared turn-start params (Rust run_codex_thread_interactive pins the
// delegate config to Constrained::allow_only(Never)).
func TestRuntimeAgentControllerSpawnPinsSubagentApprovalNever(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir()}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	defer router.Close()
	controller := newRuntimeAgentControllerWithVersion(router, "parent", parent.Metadata.CWD, 2, agent.VersionV2)

	prompt := "do the thing"
	result, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{TaskName: "worker", Message: &prompt})
	if err != nil {
		t.Fatalf("SpawnAgent error = %v", err)
	}
	params := &turn.TurnStartParams{ThreadID: result.AgentID}
	if err := router.prepareTurnStartParams(params); err != nil {
		t.Fatalf("prepareTurnStartParams(spawned subagent) error = %v", err)
	}
	if params.ApprovalPolicy != sandbox.ApprovalNever {
		t.Fatalf("spawned subagent approval policy = %q, want never (#38205)", params.ApprovalPolicy)
	}
	if policy := turnApprovalPolicyForTurn(nil, params); policy != sandbox.ApprovalNever {
		t.Fatalf("spawned subagent effective approval policy = %q, want never", policy)
	}
}

// TestRuntimeRecordIsSubagent pins the subagent-thread predicate used by the
// central enforcement: agent-tool spawns, the generic subagent source, and the
// subagent review/compact/other kinds are all delegates.
func TestRuntimeRecordIsSubagent(t *testing.T) {
	now := time.Now().UTC()
	base := func() *session.Record {
		return &session.Record{ID: "t", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: t.TempDir()}}
	}
	cases := []struct {
		name   string
		mutate func(*session.Metadata)
		want   bool
	}{
		{name: "normal-thread", mutate: func(_ *session.Metadata) {}, want: false},
		{name: "thread-spawn", mutate: func(m *session.Metadata) { m.ThreadSource = "subAgentThreadSpawn" }, want: true},
		{name: "originator-subagent", mutate: func(m *session.Metadata) { m.Originator = "subagent" }, want: true},
		{name: "source-subagent", mutate: func(m *session.Metadata) { m.Source = "subagent" }, want: true},
		{name: "subagent-review", mutate: func(m *session.Metadata) { m.ThreadSource = "subAgentReview" }, want: true},
		{name: "memory-consolidation", mutate: func(m *session.Metadata) { m.ThreadSource = "memoryConsolidation" }, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := base()
			tc.mutate(&record.Metadata)
			if got := runtimeRecordIsSubagent(record); got != tc.want {
				t.Fatalf("runtimeRecordIsSubagent(%#v) = %v, want %v", record.Metadata, got, tc.want)
			}
		})
	}
}
