package exec

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"codex_go/session"
)

// Rust #51329 removed partial-history subagent forks from the whole control
// path, so the exec agent controller hands the child the parent's full history
// for `all` and for the legacy positive-integer spellings, and no parent
// history for `none`. `codex exec` has no last-N fork option of its own.
func TestExecAgentControllerForkTurnsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	parent := &session.Record{
		ID: "thread-parent", SessionID: "thread-parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir()},
		Items: []session.Item{
			{ID: "item-1", Type: "message", Role: "user", Text: "first", CreatedAt: now},
			{ID: "item-2", Type: "message", Role: "assistant", Text: "second", CreatedAt: now},
			{ID: "item-3", Type: "message", Role: "user", Text: "third", CreatedAt: now},
		},
	}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	controller := newExecAgentController(NewLocalRunner(home), context.Background(), &Request{}, "thread-parent", 4).(*execAgentController)
	t.Cleanup(controller.shutdown)

	all := controller.parentInputItems(execStringPointer("all"))
	if len(all) == 0 {
		t.Fatal("parentInputItems(all) inherited no parent history")
	}
	for _, legacy := range []string{"1", "3"} {
		inherited := controller.parentInputItems(execStringPointer(legacy))
		if len(inherited) != len(all) {
			t.Fatalf("parentInputItems(%q) inherited %d items, want the full history (%d)", legacy, len(inherited), len(all))
		}
	}
	if none := controller.parentInputItems(execStringPointer("none")); len(none) != 0 {
		t.Fatalf("parentInputItems(none) = %#v, want no inherited history", none)
	}
	for _, invalid := range []string{"0", "banana"} {
		if got := controller.parentInputItems(execStringPointer(invalid)); len(got) != 0 {
			t.Fatalf("parentInputItems(%q) = %#v, want no inherited history", invalid, got)
		}
	}
}
