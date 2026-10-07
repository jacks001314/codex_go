package mcp

import (
	"testing"

	managedconfig "codex_go/config"
)

// TestSelectedEnvironmentsPreserveOrderAndUnavailableSelectionsLikeRust covers
// Rust #51503 (core/tests/suite/mcp_executor_context_tests.rs
// `thread_projection_preserves_executor_order_and_unavailable_selections`): a
// pending or failed primary executor must reach MCP contributors ahead of a
// ready secondary, in priority order, instead of being filtered down to the
// ready capability roots.
func TestSelectedEnvironmentsPreserveOrderAndUnavailableSelectionsLikeRust(t *testing.T) {
	ready := TurnEnvironmentSelection{EnvironmentID: "ready-executor", State: EnvironmentSelectionReady}
	pending := TurnEnvironmentSelection{EnvironmentID: "unavailable-executor", State: EnvironmentSelectionPending}
	failed := TurnEnvironmentSelection{
		EnvironmentID: "unavailable-executor",
		State:         EnvironmentSelectionFailed,
		Error:         "configuration unavailable",
	}

	for _, tc := range []struct {
		name       string
		selections []TurnEnvironmentSelection
		wantIDs    []string
	}{
		{
			name:       "unavailable primary first",
			selections: []TurnEnvironmentSelection{pending, ready},
			wantIDs:    []string{"unavailable-executor", "ready-executor"},
		},
		{
			name:       "unavailable secondary last",
			selections: []TurnEnvironmentSelection{ready, pending},
			wantIDs:    []string{"ready-executor", "unavailable-executor"},
		},
		{
			name:       "failed entry carries its owner error",
			selections: []TurnEnvironmentSelection{failed, ready},
			wantIDs:    []string{"unavailable-executor", "ready-executor"},
		},
	} {
		snapshot := NewSelectedEnvironments(tc.selections)
		if snapshot.Len() != 2 {
			t.Fatalf("%s: Len = %d, want 2 (pending and failed entries must be preserved)", tc.name, snapshot.Len())
		}
		gotIDs := snapshot.EnvironmentIDs()
		if len(gotIDs) != len(tc.wantIDs) {
			t.Fatalf("%s: EnvironmentIDs = %#v, want %#v", tc.name, gotIDs, tc.wantIDs)
		}
		for i := range tc.wantIDs {
			if gotIDs[i] != tc.wantIDs[i] {
				t.Fatalf("%s: EnvironmentIDs = %#v, want %#v (priority order)", tc.name, gotIDs, tc.wantIDs)
			}
		}
		got := snapshot.Selections()
		for i := range tc.selections {
			if got[i].EnvironmentID != tc.selections[i].EnvironmentID || got[i].State != tc.selections[i].State {
				t.Fatalf("%s: Selections[%d] = %#v, want %#v", tc.name, i, got[i], tc.selections[i])
			}
		}
		authority := snapshot.Authority()
		if authority == nil || !authority.Scoped {
			t.Fatalf("%s: authority = %#v, want a scoped authority", tc.name, authority)
		}
		if !authority.Unavailable["unavailable-executor"] {
			t.Fatalf("%s: unavailable executor not marked unavailable: %#v", tc.name, authority.Unavailable)
		}
		if !authority.Unlimited["ready-executor"] {
			t.Fatalf("%s: ready executor not unrestricted: %#v", tc.name, authority.Unlimited)
		}
	}
}

// TestSelectedEnvironmentsDistinguishAbsentFromExplicitlyEmptyLikeRust covers
// Rust #51503's `None` versus `Some(&[])` distinction: threadless discovery
// carries no selection at all, while a thread that selected no environments
// publishes an explicitly empty snapshot.
func TestSelectedEnvironmentsDistinguishAbsentFromExplicitlyEmptyLikeRust(t *testing.T) {
	var absent *SelectedEnvironments
	if absent.Len() != 0 || absent.EnvironmentIDs() != nil || absent.Selections() != nil {
		t.Fatalf("absent snapshot = len %d ids %#v", absent.Len(), absent.EnvironmentIDs())
	}
	if absent.Authority() != nil {
		t.Fatalf("absent snapshot must not produce an authority: %#v", absent.Authority())
	}
	if clone := absent.Clone(); clone != nil {
		t.Fatalf("clone of absent snapshot = %#v, want nil", clone)
	}

	empty := NewSelectedEnvironments(nil)
	if empty == nil {
		t.Fatal("explicitly empty snapshot must not be nil")
	}
	if empty.Len() != 0 || len(empty.EnvironmentIDs()) != 0 {
		t.Fatalf("explicitly empty snapshot = len %d ids %#v", empty.Len(), empty.EnvironmentIDs())
	}
	authority := empty.Authority()
	if authority == nil {
		t.Fatal("explicitly empty snapshot must still produce an authority")
	}
	if authority.Scoped {
		t.Fatalf("explicitly empty snapshot must stay unscoped: %#v", authority)
	}
	if clone := empty.Clone(); clone == nil {
		t.Fatal("clone of explicitly empty snapshot must stay non-nil")
	}
}

// TestSelectedEnvironmentsAuthorityRestrictsOwnerPolicyLikeRust keeps the
// pre-#51503 authority semantics while deriving them from the ordered snapshot:
// a ready selection with an owner mcp_policy is restricted, a ready selection
// without one is unrestricted, and any other captured state is unrestricted
// (thread-supplied configuration).
func TestSelectedEnvironmentsAuthorityRestrictsOwnerPolicyLikeRust(t *testing.T) {
	policy := &managedconfig.EnvironmentMCPPolicy{}
	snapshot := NewSelectedEnvironments([]TurnEnvironmentSelection{
		{EnvironmentID: "restricted", State: EnvironmentSelectionReady, McpPolicy: policy},
		{EnvironmentID: "unrestricted", State: EnvironmentSelectionReady},
		{EnvironmentID: "from-thread"},
	})
	authority := snapshot.Authority()
	if authority == nil {
		t.Fatal("authority must be set for a captured snapshot")
	}
	if got := authority.Restricted["restricted"]; got == nil {
		t.Fatalf("restricted selection lost its policy: %#v", authority.Restricted)
	}
	if !authority.Unlimited["unrestricted"] {
		t.Fatalf("ready selection without policy must be unrestricted: %#v", authority.Unlimited)
	}
	if !authority.Unlimited["from-thread"] {
		t.Fatalf("thread-supplied configuration must be unrestricted: %#v", authority.Unlimited)
	}
}

// TestMCPServerContributionContextCarriesSelectionsLikeRust covers Rust #51503's
// `with_selected_environments` / `selected_environments` accessors.
func TestMCPServerContributionContextCarriesSelectionsLikeRust(t *testing.T) {
	threadless := NewMCPServerContributionContext("", nil)
	if threadless.SelectedEnvironments() != nil {
		t.Fatalf("threadless context = %#v, want no selection snapshot", threadless.SelectedEnvironments())
	}

	selections := NewSelectedEnvironments([]TurnEnvironmentSelection{
		{EnvironmentID: "primary", State: EnvironmentSelectionPending},
		{EnvironmentID: "secondary", State: EnvironmentSelectionReady},
	})
	context := NewMCPServerContributionContext("thread-1", selections)
	if context.ThreadID() != "thread-1" {
		t.Fatalf("ThreadID = %q, want thread-1", context.ThreadID())
	}
	observed := context.SelectedEnvironments()
	if observed == nil || observed.Len() != 2 {
		t.Fatalf("observed selections = %#v, want the complete ordered snapshot", observed)
	}
	if ids := observed.EnvironmentIDs(); len(ids) != 2 || ids[0] != "primary" {
		t.Fatalf("observed ids = %#v, want primary first", ids)
	}
	if observed.Selections()[0].State != EnvironmentSelectionPending {
		t.Fatalf("pending selection was filtered: %#v", observed.Selections())
	}

	// The snapshot is copied, so later mutations of the caller's slice cannot
	// change what a contributor observed.
	selections.selections[0].State = EnvironmentSelectionReady
	if context.SelectedEnvironments().Selections()[0].State != EnvironmentSelectionPending {
		t.Fatal("contribution context aliased the caller's snapshot")
	}

	replaced := NewMCPServerContributionContext("thread-2", nil).WithSelectedEnvironments(selections)
	if replaced.ThreadID() != "thread-2" || replaced.SelectedEnvironments() == nil {
		t.Fatalf("WithSelectedEnvironments = %#v", replaced)
	}
}
