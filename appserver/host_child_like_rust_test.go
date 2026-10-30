package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/execpolicy"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

func activateHostInvocation(t *testing.T, router *RuntimeRouter, threadID string, turnID string, callID string) (*agent.InvocationRegistry, *agent.InvocationGuard, *agent.ToolInvocationContext) {
	t.Helper()
	registry := router.invocationRegistryForThread(threadID)
	if registry == nil {
		t.Fatal("invocationRegistryForThread() returned nil")
	}
	reservation, err := registry.Reserve(turnID, callID)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	guard := reservation.Activate()
	if guard == nil {
		t.Fatal("Activate() returned no guard")
	}
	invocation, err := router.ToolInvocation(threadID, turnID, callID)
	if err != nil {
		t.Fatalf("ToolInvocation() error = %v", err)
	}
	return registry, guard, invocation
}

// Mirrors Rust #57586 HostToolInvocationLookup: the registry is per thread, and
// a lookup only resolves a published, still-running call.
func TestHostToolInvocationRegistriesArePerThreadLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	if router.invocationRegistryForThread("thread-a") == router.invocationRegistryForThread("thread-b") {
		t.Fatal("thread registries are shared")
	}
	if _, err := router.ToolInvocation("thread-a", "turn-1", "call-1"); !errors.Is(err, agent.ErrToolCallNotActive) {
		t.Fatalf("unknown thread lookup error = %v", err)
	}
	_, guard, invocation := activateHostInvocation(t, router, "thread-a", "turn-1", "call-1")
	if _, err := router.ToolInvocation("thread-b", "turn-1", "call-1"); !errors.Is(err, agent.ErrToolCallNotActive) {
		t.Fatalf("cross-thread lookup error = %v", err)
	}
	guard.Release()
	if _, err := router.ToolInvocation("thread-a", "turn-1", "call-1"); !errors.Is(err, agent.ErrToolCallNotActive) {
		t.Fatalf("released lookup error = %v", err)
	}
	if invocation.Active() {
		t.Fatal("invocation stayed active after release")
	}
}

// Mirrors Rust #58390 create_child: reserved startup always runs on the invoking
// thread's manager, so the host supplies only the child ID and request and the
// started child is idle with the reserved ID and canonical agent path.
func TestCreateChildUsesHostReservedIDLikeRust(t *testing.T) {
	router, store := newEnvironmentSubagentTestRouter(t, string(agent.VersionV2))
	_, guard, invocation := activateHostInvocation(t, router, "root", "turn-1", "spawn-call")
	defer guard.Release()
	if _, err := router.CreateChild(context.Background(), "root", invocation, agent.ThreadCreateRequest{TaskName: "worker"}, agent.HostThreadSpawnReservation{ThreadID: "root"}); err == nil || !strings.Contains(err.Error(), "reservation does not match prepared child") {
		t.Fatalf("caller reservation error = %v", err)
	}
	result, err := router.CreateChild(context.Background(), "root", invocation, agent.ThreadCreateRequest{TaskName: "worker"}, agent.HostThreadSpawnReservation{ThreadID: "reserved-child"})
	if err != nil {
		t.Fatalf("CreateChild() error = %v", err)
	}
	if result.AgentID != "reserved-child" || result.TaskName != "/root/worker" {
		t.Fatalf("spawn result = %#v", result)
	}
	record, err := store.Load(session.ThreadID("reserved-child"))
	if err != nil || record == nil {
		t.Fatalf("Load(reserved-child) = %#v, %v", record, err)
	}
	if string(record.ParentThreadID) != "root" || record.Metadata.AgentPath != "/root/worker" || record.Metadata.MultiAgentVersion != string(agent.VersionV2) {
		t.Fatalf("reserved child record = %#v", record.Metadata)
	}
	// Rust's fork_mode None starts fresh history even though Go's model-facing
	// spawn args default a nil ForkTurns to "all".
	if got := record.Metadata.Extra[session.HistoryInitializationExtraKey]; got != string(session.HistoryInitializationNew) {
		t.Fatalf("fresh child history initialization = %#v, want new", got)
	}
	if _, err := router.CreateChild(context.Background(), "root", invocation, agent.ThreadCreateRequest{TaskName: "worker"}, agent.HostThreadSpawnReservation{ThreadID: "reserved-child"}); err == nil {
		t.Fatal("reused reserved ID was accepted")
	}
	if _, err := router.CreateChild(context.Background(), "root", invocation, agent.ThreadCreateRequest{TaskName: "forked", ForkContext: true}, agent.HostThreadSpawnReservation{ThreadID: "reserved-forked"}); err != nil {
		t.Fatalf("CreateChild(fork) error = %v", err)
	}
	forkedRecord, err := store.Load(session.ThreadID("reserved-forked"))
	if err != nil || forkedRecord == nil {
		t.Fatalf("Load(reserved-forked) = %#v, %v", forkedRecord, err)
	}
	if got := forkedRecord.Metadata.Extra[session.HistoryInitializationExtraKey]; got != string(session.HistoryInitializationWarmFork) {
		t.Fatalf("forked child history initialization = %#v, want warm_fork", got)
	}
}

// create_child requires a live invocation from the same runtime.
func TestCreateChildRequiresLiveInvocationLikeRust(t *testing.T) {
	router, _ := newEnvironmentSubagentTestRouter(t, string(agent.VersionV2))
	_, guard, invocation := activateHostInvocation(t, router, "root", "turn-1", "spawn-call")
	defer guard.Release()
	if _, err := router.CreateChild(context.Background(), "child-a", invocation, agent.ThreadCreateRequest{TaskName: "worker"}, agent.HostThreadSpawnReservation{ThreadID: "reserved-child"}); err == nil || !strings.Contains(err.Error(), "different runtime") {
		t.Fatalf("cross-runtime CreateChild() error = %v", err)
	}
	orphan := &agent.ToolInvocationContext{ThreadID: "root", TurnID: "turn-1", CallID: "spawn-call"}
	if _, err := router.CreateChild(context.Background(), "root", orphan, agent.ThreadCreateRequest{TaskName: "worker"}, agent.HostThreadSpawnReservation{ThreadID: "reserved-child"}); err == nil || !strings.Contains(err.Error(), "spawning tool call finished") {
		t.Fatalf("inactive CreateChild() error = %v", err)
	}
	result, err := router.CreateChild(context.Background(), "root", invocation, agent.ThreadCreateRequest{TaskName: "worker"}, agent.HostThreadSpawnReservation{ThreadID: "reserved-child"})
	if err != nil {
		t.Fatalf("CreateChild() error = %v", err)
	}
	if result.AgentID != "reserved-child" {
		t.Fatalf("CreateChild() agent = %q", result.AgentID)
	}
}

// Mirrors Rust #58390 prepare_child: a live invocation captures the parent's
// instructions, environments, permissions, approval settings, execution-policy
// snapshot with its managed requirements, inheritable dynamic tools and
// disabled plugins without requiring a child request.
func TestPrepareChildCapturesParentContextLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("sandbox_mode = \"workspace-write\"\n\n[features]\nmulti_agent_v2_dynamic_tools = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	cwd := t.TempDir()
	dynamicTools := []json.RawMessage{json.RawMessage(`{"type":"function","name":"echo"}`)}
	if err := store.Create(&session.Record{
		ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			CWD:               cwd,
			AgentPath:         "/root",
			MultiAgentVersion: string(agent.VersionV2),
			Instructions:      "parent instructions",
			DynamicTools:      dynamicTools,
			Extra:             map[string]any{"disabled_plugin_ids": []any{"calendar@market"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	configService := config.NewConfigService(home)
	managed := mustRequirementsPolicy(t, `prefix_rule(pattern=["managed-command"], decision="forbidden")`)
	configService.SetRequirements(&config.ConfigRequirements{Rules: managed})
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: configService, DefaultCWD: cwd})
	reviewer := "auto_review"
	environments := []map[string]any{{"environmentId": "env-1", "cwd": cwd}}
	params := &turn.TurnStartParams{
		ThreadID:          "root",
		CWD:               cwd,
		Originator:        "codex_app_server",
		ApprovalPolicy:    sandbox.ApprovalOnRequest,
		ApprovalsReviewer: &reviewer,
		Environments:      environments,
	}
	if err := router.threads.RegisterTurn("root", "turn-1", nil, 0, params); err != nil {
		t.Fatal(err)
	}
	_, guard, invocation := activateHostInvocation(t, router, "root", "turn-1", "spawn-call")
	defer guard.Release()

	prepared, err := router.PrepareChild(context.Background(), "root", invocation)
	if err != nil {
		t.Fatalf("PrepareChild() error = %v", err)
	}
	if prepared.Originator != "codex_app_server" || prepared.UserInstructions != "parent instructions" || prepared.CWD != cwd {
		t.Fatalf("prepared context = %#v", prepared)
	}
	if !reflect.DeepEqual(prepared.Environments, environments) {
		t.Fatalf("prepared environments = %#v, want %#v", prepared.Environments, environments)
	}
	if prepared.ResolvedPermissionProfile == nil {
		t.Fatal("prepared permission profile is nil")
	}
	if prepared.ApprovalPolicy != string(sandbox.ApprovalOnRequest) || prepared.ApprovalsReviewer != reviewer {
		t.Fatalf("prepared approvals = %q/%q", prepared.ApprovalPolicy, prepared.ApprovalsReviewer)
	}
	if !reflect.DeepEqual(prepared.DisabledPluginIDs, []string{"calendar@market"}) {
		t.Fatalf("prepared disabled plugins = %#v", prepared.DisabledPluginIDs)
	}
	if len(prepared.DynamicTools) != 1 || !strings.Contains(string(prepared.DynamicTools[0]), `"echo"`) {
		t.Fatalf("prepared dynamic tools = %#v", prepared.DynamicTools)
	}
	if prepared.ExecPolicy == nil || prepared.ExecPolicySourceRequirements == nil {
		t.Fatalf("prepared exec policy = %#v / %#v", prepared.ExecPolicy, prepared.ExecPolicySourceRequirements)
	}
	if !reflect.DeepEqual(prepared.ExecPolicySourceRequirements.Fingerprint(), managed.Fingerprint()) {
		t.Fatalf("source requirements = %v, want %v", prepared.ExecPolicySourceRequirements.Fingerprint(), managed.Fingerprint())
	}
	if evaluation := prepared.ExecPolicy.Policy().EvaluateCommand([]string{"managed-command"}); evaluation.Decision == nil || *evaluation.Decision != execpolicy.DecisionForbidden {
		t.Fatalf("managed command decision = %#v", evaluation)
	}

	// A configuration refresh rebuilds the turn without the managed requirement,
	// but the captured source requirements and policy must survive.
	if _, ok := router.threads.ConsumeTurn("root", "turn-1", false); !ok {
		t.Fatal("ConsumeTurn() did not consume the active turn")
	}
	refreshed := *params
	refreshed.Config = map[string]any{"refreshed": true}
	if err := router.threads.RegisterTurn("root", "turn-2", nil, 0, &refreshed); err != nil {
		t.Fatal(err)
	}
	refreshedContext, err := router.PrepareChild(context.Background(), "root", invocation)
	if err != nil {
		t.Fatalf("PrepareChild(refreshed) error = %v", err)
	}
	if refreshedContext.ExecPolicySourceRequirements == nil ||
		!reflect.DeepEqual(refreshedContext.ExecPolicySourceRequirements.Fingerprint(), managed.Fingerprint()) {
		t.Fatalf("refreshed source requirements = %#v", refreshedContext.ExecPolicySourceRequirements)
	}
	if evaluation := refreshedContext.ExecPolicy.Policy().EvaluateCommand([]string{"managed-command"}); evaluation.Decision == nil || *evaluation.Decision != execpolicy.DecisionForbidden {
		t.Fatalf("refreshed managed command decision = %#v", evaluation)
	}
}
