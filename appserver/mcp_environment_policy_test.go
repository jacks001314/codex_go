package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/turn"
)

// TestOwnerSuppliedEnvironmentMCPPolicyRestrictsServersLikeRust mirrors Rust
// #39335: an owner-supplied environment mcp_policy restricts which configured
// MCP servers are available in that environment, a pending owner selection keeps
// them unavailable, and a thread-supplied selection stays unrestricted.
func TestOwnerSuppliedEnvironmentMCPPolicyRestrictsServersLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{"mcp_servers": map[string]any{
		"allowed-server": map[string]any{"url": "https://allowed.example/mcp", "enabled": true, "environment_id": "env-a"},
		"other-server":   map[string]any{"url": "https://other.example/mcp", "enabled": true, "environment_id": "env-a"},
		"local-server":   map[string]any{"url": "https://local.example/mcp", "enabled": true},
	}}}
	threadID := "thread-owner-policy"

	assertServers := func(t *testing.T, step string, allowed, other bool) {
		t.Helper()
		service := router.mcpServiceForThread(threadID, cfg)
		if _, ok := service.ServerConfigForServer("allowed-server"); ok != allowed {
			t.Fatalf("%s: allowed-server available = %v, want %v", step, ok, allowed)
		}
		if _, ok := service.ServerConfigForServer("other-server"); ok != other {
			t.Fatalf("%s: other-server available = %v, want %v", step, ok, other)
		}
		if _, ok := service.ServerConfigForServer("local-server"); !ok {
			t.Fatalf("%s: the controller-owned local server must stay available", step)
		}
	}

	register := func(t *testing.T, turnID string, selection map[string]any) {
		t.Helper()
		normalized, err := validateEnvironmentSelectionConfig(selectionEnvironmentID(selection), selection)
		if err != nil {
			t.Fatalf("validateEnvironmentSelectionConfig() error = %v", err)
		}
		// Mirrors the normalization validateTurnEnvironmentSelections applies.
		if normalized == nil {
			delete(selection, "config")
		} else {
			selection["config"] = normalized
		}
		params := &turn.TurnStartParams{ThreadID: threadID, Environments: []map[string]any{selection}}
		if err := router.registerActiveRuntimeTurn(threadID, turnID, nil, 1, params); err != nil {
			t.Fatalf("register %s: %v", turnID, err)
		}
	}

	// A ready owner config with an allowlist admits only the listed server.
	register(t, "turn-ready", map[string]any{
		"environmentId": "env-a",
		"config": map[string]any{"state": "ready", "config": map[string]any{
			"mcp_policy": map[string]any{"servers": map[string]any{
				"allowed-server": map[string]any{"identity": map[string]any{"url": "https://allowed.example/mcp"}},
			}},
		}},
	})
	assertServers(t, "owner allowlist", true, false)
	router.threads.ConsumeTurn(threadID, "turn-ready", false)

	// A pending owner selection cannot claim owner authority, so its servers are
	// unavailable until configuration arrives.
	register(t, "turn-pending", map[string]any{
		"environmentId": "env-a",
		"config":        map[string]any{"state": "pending"},
	})
	assertServers(t, "pending owner config", false, false)
	router.threads.ConsumeTurn(threadID, "turn-pending", false)

	// A failed owner selection behaves the same way.
	register(t, "turn-failed", map[string]any{
		"environmentId": "env-a",
		"config":        map[string]any{"state": "failed", "error": "provisioning failed"},
	})
	assertServers(t, "failed owner config", false, false)
	router.threads.ConsumeTurn(threadID, "turn-failed", false)

	// A thread-supplied selection is unrestricted.
	register(t, "turn-thread", map[string]any{"environmentId": "env-a"})
	assertServers(t, "thread-supplied config", true, true)
	router.threads.ConsumeTurn(threadID, "turn-thread", false)

	// The policy is re-read from the stored selection on the next turn.
	register(t, "turn-ready-again", map[string]any{
		"environmentId": "env-a",
		"config": map[string]any{"state": "ready", "config": map[string]any{
			"mcp_policy": map[string]any{"servers": map[string]any{}},
		}},
	})
	assertServers(t, "owner deny-all", false, false)
}

// TestTurnEnvironmentSelectionsPreserveExecutorOrderAndUnavailableEntriesLikeRust
// mirrors Rust #51503
// (`thread_projection_preserves_executor_order_and_unavailable_selections`): the
// captured executor selection keeps priority order and pending or failed entries
// instead of collapsing to the ready capability roots.
func TestTurnEnvironmentSelectionsPreserveExecutorOrderAndUnavailableEntriesLikeRust(t *testing.T) {
	ready := map[string]any{"environmentId": "ready-executor", "cwd": "/remote"}
	pending := map[string]any{"environmentId": "unavailable-executor", "cwd": "/remote", "config": map[string]any{"state": "pending"}}
	failed := map[string]any{"environmentId": "unavailable-executor", "cwd": "/remote", "config": map[string]any{"state": "failed", "error": "configuration unavailable"}}

	for _, tc := range []struct {
		name       string
		selections []map[string]any
		wantIDs    []string
		wantStates []mcp.EnvironmentSelectionState
	}{
		{
			name:       "pending primary before ready secondary",
			selections: []map[string]any{pending, ready},
			wantIDs:    []string{"unavailable-executor", "ready-executor"},
			wantStates: []mcp.EnvironmentSelectionState{mcp.EnvironmentSelectionPending, mcp.EnvironmentSelectionReady},
		},
		{
			name:       "ready primary before pending secondary",
			selections: []map[string]any{ready, pending},
			wantIDs:    []string{"ready-executor", "unavailable-executor"},
			wantStates: []mcp.EnvironmentSelectionState{mcp.EnvironmentSelectionReady, mcp.EnvironmentSelectionPending},
		},
		{
			name:       "failed secondary keeps its owner error",
			selections: []map[string]any{ready, failed},
			wantIDs:    []string{"ready-executor", "unavailable-executor"},
			wantStates: []mcp.EnvironmentSelectionState{mcp.EnvironmentSelectionReady, mcp.EnvironmentSelectionFailed},
		},
	} {
		snapshot := turnEnvironmentSelections(&turn.TurnStartParams{Environments: tc.selections})
		if snapshot == nil {
			t.Fatalf("%s: snapshot is nil, want the captured selections", tc.name)
		}
		if snapshot.Len() != len(tc.wantIDs) {
			t.Fatalf("%s: Len = %d, want %d (unavailable entries must be preserved)", tc.name, snapshot.Len(), len(tc.wantIDs))
		}
		gotIDs := snapshot.EnvironmentIDs()
		for i := range tc.wantIDs {
			if gotIDs[i] != tc.wantIDs[i] {
				t.Fatalf("%s: EnvironmentIDs = %#v, want %#v", tc.name, gotIDs, tc.wantIDs)
			}
		}
		got := snapshot.Selections()
		for i := range tc.wantStates {
			if got[i].State != tc.wantStates[i] {
				t.Fatalf("%s: Selections[%d].State = %q, want %q", tc.name, i, got[i].State, tc.wantStates[i])
			}
		}
		if tc.name == "failed secondary keeps its owner error" && got[1].Error != "configuration unavailable" {
			t.Fatalf("%s: failed selection error = %q", tc.name, got[1].Error)
		}
		// The availability authority is derived from the same ordered snapshot.
		authority := mcpEnvironmentAuthorityForTurn(&turn.TurnStartParams{Environments: tc.selections})
		if authority == nil || !authority.Unavailable["unavailable-executor"] {
			t.Fatalf("%s: authority = %#v, want the unavailable executor marked", tc.name, authority)
		}
	}

	// An unparseable owner selection stays observable as a failed entry instead
	// of being dropped from the snapshot.
	malformed := turnEnvironmentSelections(&turn.TurnStartParams{Environments: []map[string]any{{
		"environmentId": "broken-executor",
		"config":        map[string]any{"state": "not-a-state"},
	}}})
	if malformed == nil || malformed.Len() != 1 {
		t.Fatalf("malformed selection snapshot = %#v, want one failed entry", malformed)
	}
	if selection := malformed.Selections()[0]; selection.State != mcp.EnvironmentSelectionFailed || selection.Error == "" {
		t.Fatalf("malformed selection = %#v, want a failed entry with its parse error", selection)
	}
	if authority := mcpEnvironmentAuthorityForTurn(&turn.TurnStartParams{Environments: []map[string]any{{
		"environmentId": "broken-executor",
		"config":        map[string]any{"state": "not-a-state"},
	}}}); authority == nil || !authority.Unavailable["broken-executor"] {
		t.Fatalf("malformed selection authority = %#v, want unavailable", authority)
	}
}

// TestTurnEnvironmentSelectionsDistinguishAbsentFromExplicitlyEmptyLikeRust
// mirrors Rust #51503's `None` versus `Some(&[])`: threadless/no capture leaves
// the snapshot nil, while a thread that explicitly selected no environments
// still captures a non-nil empty snapshot.
func TestTurnEnvironmentSelectionsDistinguishAbsentFromExplicitlyEmptyLikeRust(t *testing.T) {
	if snapshot := turnEnvironmentSelections(nil); snapshot != nil {
		t.Fatalf("nil params snapshot = %#v, want nil", snapshot)
	}
	if snapshot := turnEnvironmentSelections(&turn.TurnStartParams{}); snapshot != nil {
		t.Fatalf("absent selection snapshot = %#v, want nil (Rust None)", snapshot)
	}
	if authority := mcpEnvironmentAuthorityForTurn(&turn.TurnStartParams{}); authority != nil {
		t.Fatalf("absent selection authority = %#v, want nil (no attachment scoping)", authority)
	}

	explicit := turnEnvironmentSelections(&turn.TurnStartParams{Environments: []map[string]any{}})
	if explicit == nil {
		t.Fatal("explicitly empty selection must produce a non-nil snapshot (Rust Some(&[]))")
	}
	if explicit.Len() != 0 || len(explicit.EnvironmentIDs()) != 0 {
		t.Fatalf("explicitly empty snapshot = len %d ids %#v", explicit.Len(), explicit.EnvironmentIDs())
	}
	if authority := mcpEnvironmentAuthorityForTurn(&turn.TurnStartParams{Environments: []map[string]any{}}); authority == nil || authority.Scoped {
		t.Fatalf("explicitly empty selection authority = %#v, want an unscoped authority", authority)
	}
}

// TestRuntimeMCPConfigPublishesSelectedEnvironmentSelectionsLikeRust covers the
// rest of Rust #51503's plumbing for Go: the ordered executor snapshot captured
// for a thread reaches the MCP projection (Rust
// McpServerContributionContext::with_selected_environments), including pending
// entries, and the availability list keeps the same priority order.
func TestRuntimeMCPConfigPublishesSelectedEnvironmentSelectionsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{"mcp_servers": map[string]any{
		"primary-server":   map[string]any{"url": "https://primary.example/mcp", "enabled": true, "environment_id": "primary-executor"},
		"secondary-server": map[string]any{"url": "https://secondary.example/mcp", "enabled": true, "environment_id": "secondary-executor"},
	}}}
	threadID := "thread-selected-environments"

	params := &turn.TurnStartParams{ThreadID: threadID, Environments: []map[string]any{
		{"environmentId": "primary-executor", "cwd": "/remote", "config": map[string]any{"state": "pending"}},
		{"environmentId": "secondary-executor", "cwd": "/remote"},
	}}
	register := func(t *testing.T, turnID string) {
		t.Helper()
		if err := router.registerActiveRuntimeTurn(threadID, turnID, nil, 1, params); err != nil {
			t.Fatalf("register %s: %v", turnID, err)
		}
	}
	register(t, "turn-selected")

	service := router.mcpServiceForThread(threadID, cfg)
	snapshot := service.SelectedEnvironments()
	if snapshot == nil {
		t.Fatal("published runtime must carry the captured executor selection")
	}
	if snapshot.Len() != 2 {
		t.Fatalf("published snapshot = %#v, want both entries including the pending primary", snapshot.Selections())
	}
	got := snapshot.Selections()
	if got[0].EnvironmentID != "primary-executor" || got[0].State != mcp.EnvironmentSelectionPending {
		t.Fatalf("published selections[0] = %#v, want the pending primary first", got[0])
	}
	if got[1].EnvironmentID != "secondary-executor" || got[1].State != mcp.EnvironmentSelectionReady {
		t.Fatalf("published selections[1] = %#v, want the ready secondary", got[1])
	}
	// The unavailable primary keeps its own server unavailable instead of
	// silently resolving the ready secondary for it.
	if _, ok := service.ServerConfigForServer("primary-server"); ok {
		t.Fatal("server bound to a pending primary executor must stay unavailable")
	}
	if _, ok := service.ServerConfigForServer("secondary-server"); !ok {
		t.Fatal("server bound to the ready secondary executor must stay available")
	}

	// Dropping the capture removes the snapshot again (threadless discovery).
	router.threads.ConsumeTurn(threadID, "turn-selected", false)
	service = router.mcpServiceForThread(threadID, cfg)
	if snapshot := service.SelectedEnvironments(); snapshot != nil && snapshot.Len() != 0 {
		t.Fatalf("snapshot after the turn ended = %#v, want none", snapshot.Selections())
	}
}
