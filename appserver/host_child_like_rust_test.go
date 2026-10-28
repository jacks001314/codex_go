package appserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/network"
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

// Mirrors Rust prepare_spawn: detached preparation captures the caller's
// settings but rejects bindings the destination manager cannot transfer.
func TestPrepareChildRejectsDetachedBindingsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	policyController := network.NewNetworkPolicyController()
	managedPolicy := policyController.Policy()
	emaValues := map[string]any{
		"mcp_enterprise_managed_auth": map[string]any{
			"idp": map[string]any{"issuer": "https://idp.example", "client_id": "idp-client"},
		},
		"mcp_servers": map[string]any{
			"enterprise": map[string]any{
				"url":     "https://resource.example/mcp",
				"auth":    config.MCPServerAuthEMAAuth,
				"enabled": true,
			},
		},
	}
	pending := []map[string]any{{
		"environmentId": "workspace",
		"cwd":           "/remote",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigPending}),
	}}
	cases := []struct {
		name   string
		cfg    *config.Config
		params *turn.TurnStartParams
		want   string
	}{
		{
			name: "managed network policy",
			cfg:  &config.Config{Values: map[string]any{}, ApplicationNetworkPolicy: &managedPolicy},
			want: "host-managed application authority",
		},
		{
			name: "ema authenticated mcp",
			cfg:  &config.Config{Values: emaValues},
			want: "EMA-authenticated MCP servers",
		},
		{
			name:   "pending environment configuration",
			cfg:    &config.Config{Values: map[string]any{}},
			params: &turn.TurnStartParams{Environments: pending},
			want:   "resolved environment configuration",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := router.detachedChildRejection(testCase.cfg, testCase.params)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("detachedChildRejection() error = %v, want %q", err, testCase.want)
			}
		})
	}
	if err := router.detachedChildRejection(&config.Config{Values: map[string]any{}}, nil); err != nil {
		t.Fatalf("unmanaged configuration error = %v", err)
	}
}

// Mirrors Rust create_child/create_prepared_child: the host supplies the child
// ID, preparation captures the caller, and the started child is idle with the
// reserved ID and canonical agent path.
func TestStartPreparedChildUsesHostReservedIDLikeRust(t *testing.T) {
	router, store := newEnvironmentSubagentTestRouter(t, string(agent.VersionV2))
	_, guard, invocation := activateHostInvocation(t, router, "root", "turn-1", "spawn-call")
	defer guard.Release()
	prepared, err := router.PrepareChild(context.Background(), "root", invocation, agent.ThreadCreateRequest{TaskName: "worker"})
	if err != nil {
		t.Fatalf("PrepareChild() error = %v", err)
	}
	if prepared.CallerThreadID != "root" || prepared.AgentPath != "/root/worker" || prepared.Request.TaskName != "worker" || prepared.Version != agent.VersionV2 {
		t.Fatalf("prepared child = %#v", prepared)
	}
	if _, err := router.StartPreparedChild(context.Background(), prepared, agent.HostThreadSpawnReservation{ThreadID: "root"}); err == nil || !strings.Contains(err.Error(), "reservation does not match prepared child") {
		t.Fatalf("caller reservation error = %v", err)
	}
	result, err := router.StartPreparedChild(context.Background(), prepared, agent.HostThreadSpawnReservation{ThreadID: "reserved-child"})
	if err != nil {
		t.Fatalf("StartPreparedChild() error = %v", err)
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
	if _, err := router.StartPreparedChild(context.Background(), prepared, agent.HostThreadSpawnReservation{ThreadID: "reserved-child"}); err == nil {
		t.Fatal("reused reserved ID was accepted")
	}
	forked, err := router.PrepareChild(context.Background(), "root", invocation, agent.ThreadCreateRequest{TaskName: "forked", ForkContext: true})
	if err != nil {
		t.Fatalf("PrepareChild(fork) error = %v", err)
	}
	if _, err := router.StartPreparedChild(context.Background(), forked, agent.HostThreadSpawnReservation{ThreadID: "reserved-forked"}); err != nil {
		t.Fatalf("StartPreparedChild(fork) error = %v", err)
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
