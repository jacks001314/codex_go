package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/session"
)

// Rust #51493
// (`environment_selection_snapshot_tests::capability_roots_follow_attachments_through_configuration_changes`):
// splitting the thread's roots across environments and collecting them again
// must restore the original order, so executor skill aliases stay stable.
func TestEnvironmentCapabilityRootsPreserveOriginalOrderLikeRust(t *testing.T) {
	roots := []SelectedCapabilityRoot{
		provisionedRoot("first", "other"),
		provisionedRoot("second", "local"),
		provisionedRoot("third", "other"),
	}
	local := CapabilityRootsForEnvironment("local", roots)
	other := CapabilityRootsForEnvironment("other", roots)

	if got := CollectEnvironmentCapabilityRoots([]EnvironmentCapabilityRoots{local, other}); !sameRootIDs(got, roots) {
		t.Fatalf("collect([local, other]) = %v, want %v", rootIDs(got), rootIDs(roots))
	}
	// Environment order must not move roots, because aliases follow this order.
	if got := CollectEnvironmentCapabilityRoots([]EnvironmentCapabilityRoots{other, local}); !sameRootIDs(got, roots) {
		t.Fatalf("collect([other, local]) = %v, want %v", rootIDs(got), rootIDs(roots))
	}
	// A thread that only selected `local` sees only that environment's roots.
	if got := CollectEnvironmentCapabilityRoots([]EnvironmentCapabilityRoots{local}); !sameRootIDs(got, roots[1:2]) {
		t.Fatalf("collect([local]) = %v, want [second]", rootIDs(got))
	}
	if got := CollectEnvironmentCapabilityRoots([]EnvironmentCapabilityRoots{other}); !sameRootIDs(got, []SelectedCapabilityRoot{roots[0], roots[2]}) {
		t.Fatalf("collect([other]) = %v, want [first third]", rootIDs(got))
	}
	// Roots with a non-environment location never bind to an environment.
	nonEnvironment := []SelectedCapabilityRoot{{ID: "standalone", Location: CapabilityRootLocation{Type: "standalone"}}}
	if got := CapabilityRootsForEnvironment("local", nonEnvironment); len(got.entries) != 0 {
		t.Fatalf("standalone roots bound to an environment: %v", got.entries)
	}
}

// Rust #51493 (`TurnEnvironmentSnapshot::selected_capability_roots`): capability
// discovery stays within the environments captured by the current turn, and the
// retained root list lets a reselection restore them.
func TestRestrictCapabilityRootsToSelectionsLikeRust(t *testing.T) {
	roots := []SelectedCapabilityRoot{
		provisionedRoot("first", "other"),
		provisionedRoot("second", "local"),
		provisionedRoot("third", "other"),
	}
	local := map[string]any{"environmentId": "local", "cwd": "/tmp/local"}
	other := map[string]any{"environmentId": "other", "cwd": "/tmp/other"}

	if got := restrictCapabilityRootsToSelections(roots, []map[string]any{local, other}); !sameRootIDs(got, roots) {
		t.Fatalf("both selections = %v, want %v", rootIDs(got), rootIDs(roots))
	}
	// Reversed selection order still yields the thread's original root order.
	if got := restrictCapabilityRootsToSelections(roots, []map[string]any{other, local}); !sameRootIDs(got, roots) {
		t.Fatalf("reversed selections = %v, want %v", rootIDs(got), rootIDs(roots))
	}
	// Deselecting `other` hides its roots without forgetting them.
	selected := restrictCapabilityRootsToSelections(roots, []map[string]any{local})
	if !sameRootIDs(selected, roots[1:2]) {
		t.Fatalf("deselected = %v, want [second]", rootIDs(selected))
	}
	// Reselecting restores them from the retained list.
	if got := restrictCapabilityRootsToSelections(roots, []map[string]any{local, other}); !sameRootIDs(got, roots) {
		t.Fatalf("reselected = %v, want %v", rootIDs(got), rootIDs(roots))
	}
	// A thread without a recorded selection keeps its full root list.
	if got := restrictCapabilityRootsToSelections(roots, nil); !sameRootIDs(got, roots) {
		t.Fatalf("no selections = %v, want %v", rootIDs(got), rootIDs(roots))
	}
}

// Rust #51493 (`remote_env_capability_roots_tests`): the discovery view follows
// the thread's persisted/active environment selections end to end.
func TestInspectSelectedCapabilityRootsFollowsEnvironmentDeselectionLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	rawRoots := make([]json.RawMessage, 0, 3)
	for _, root := range []SelectedCapabilityRoot{
		provisionedRoot("first", "other"),
		provisionedRoot("second", "local"),
		provisionedRoot("third", "other"),
	} {
		raw, err := json.Marshal(root)
		if err != nil {
			t.Fatalf("marshal root: %v", err)
		}
		rawRoots = append(rawRoots, raw)
	}
	record := &session.Record{
		ID: session.ThreadID("thread-env-roots"),
		Metadata: session.Metadata{
			CWD:                     t.TempDir(),
			SelectedCapabilityRoots: rawRoots,
			Extra: map[string]any{
				runtimeEnvironmentSelectionsExtraKey: []map[string]any{
					{"environmentId": "local", "cwd": "/tmp/local"},
					{"environmentId": "other", "cwd": "/tmp/other"},
				},
			},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("save record: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	assertReadyRootIDs(t, router.inspectSelectedCapabilityRootsForThread(record), "first", "second", "third")

	// Deselecting `other` removes its roots from discovery.
	if err := router.persistThreadEnvironmentSelections(string(record.ID), []map[string]any{
		{"environmentId": "local", "cwd": "/tmp/local"},
	}); err != nil {
		t.Fatalf("persist local-only selection: %v", err)
	}
	stored, err := store.Load(record.ID)
	if err != nil || stored == nil {
		t.Fatalf("load record: %v", err)
	}
	assertReadyRootIDs(t, router.inspectSelectedCapabilityRootsForThread(stored), "second")

	// Reselecting `other` restores its roots in the original order.
	if err := router.persistThreadEnvironmentSelections(string(record.ID), []map[string]any{
		{"environmentId": "local", "cwd": "/tmp/local"},
		{"environmentId": "other", "cwd": "/tmp/other"},
	}); err != nil {
		t.Fatalf("persist reselected environments: %v", err)
	}
	stored, err = store.Load(record.ID)
	if err != nil || stored == nil {
		t.Fatalf("load record: %v", err)
	}
	assertReadyRootIDs(t, router.inspectSelectedCapabilityRootsForThread(stored), "first", "second", "third")
}

func rootIDs(roots []SelectedCapabilityRoot) []string {
	ids := make([]string, 0, len(roots))
	for _, root := range roots {
		ids = append(ids, root.ID)
	}
	return ids
}

func sameRootIDs(got []SelectedCapabilityRoot, want []SelectedCapabilityRoot) bool {
	gotIDs, wantIDs := rootIDs(got), rootIDs(want)
	if len(gotIDs) != len(wantIDs) {
		return false
	}
	for i := range gotIDs {
		if gotIDs[i] != wantIDs[i] {
			return false
		}
	}
	return true
}

func assertReadyRootIDs(t *testing.T, status SelectedCapabilityRootsStatus, want ...string) {
	t.Helper()
	got := rootIDs(status.ReadyRoots)
	if len(got) != len(want) {
		t.Fatalf("ReadyRoots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ReadyRoots = %v, want %v", got, want)
		}
	}
}
