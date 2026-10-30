package state

import (
	"context"
	"testing"
)

// Mirrors Rust #57824 `list_attached_thread_ids`: feedback uploads need every
// thread directly attached to a parent, independent of the spawned subtree.
func TestListAttachedThreadIDsLikeRust(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	for _, id := range []string{"parent", "child-a", "child-b", "grandchild"} {
		seedGuardianFeedbackThread(t, runtime, id)
	}
	for _, edge := range []struct {
		child  string
		parent string
	}{
		{"child-a", "parent"},
		{"child-b", "parent"},
		{"grandchild", "child-a"},
	} {
		if _, err := runtime.StateDB().ExecContext(ctx,
			`UPDATE threads SET attached_thread_parent_id = ? WHERE id = ?`, edge.parent, edge.child); err != nil {
			t.Fatalf("attach %s to %s: %v", edge.child, edge.parent, err)
		}
	}

	ids, err := runtime.ListAttachedThreadIDs(ctx, "parent")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("attached ids = %#v, want the two direct children", ids)
	}
	seen := map[string]bool{ids[0]: true, ids[1]: true}
	if !seen["child-a"] || !seen["child-b"] {
		t.Fatalf("attached ids = %#v", ids)
	}
	if ids, err := runtime.ListAttachedThreadIDs(ctx, "missing"); err != nil || len(ids) != 0 {
		t.Fatalf("missing parent = %#v / %v", ids, err)
	}
}
