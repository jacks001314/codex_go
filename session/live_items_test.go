package session

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLiveThreadItemsLifecycleAndMissingItems(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store := NewStore(root)
	live := OpenLiveThreadItems(store, "thread-live", "turn-2")
	if live == nil {
		t.Fatal("OpenLiveThreadItems() = nil")
	}
	live.Append(
		Item{ID: "assistant-2", Type: "agent_message", Role: "assistant", Text: "Checking the repo."},
		Item{ID: "output-1", Type: "tool_output", CallID: "call-1", Text: "clean"},
	)

	// The registry is keyed by store root, so a second Store instance over the
	// same sessions directory sees the process-owned overlay.
	other := NewStore(root)
	items := other.LiveItems("thread-live").Snapshot()
	if len(items) != 2 || items[0].ID != "assistant-2" || items[1].CallID != "call-1" {
		t.Fatalf("Snapshot() = %#v", items)
	}

	missing := MissingItems([]Item{{ID: "output-final", Type: "tool_output", CallID: "call-1", Text: "clean"}}, items)
	if len(missing) != 1 || missing[0].ID != "assistant-2" {
		t.Fatalf("MissingItems() = %#v, want only the assistant item", missing)
	}

	live.Close()
	if other.LiveItems("thread-live") != nil {
		t.Fatal("LiveItems() still returns a closed overlay")
	}
}

func TestLiveThreadItemsRebaseDropsRunningSuffix(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "sessions"))
	live := OpenLiveThreadItems(store, "thread-live", "turn-2")
	defer live.Close()
	live.Append(Item{ID: "assistant-2", Type: "agent_message", Role: "assistant", Text: "before compaction"})

	saveErr := errors.New("save failed")
	if err := live.Rebase(func() error { return saveErr }); !errors.Is(err, saveErr) {
		t.Fatalf("Rebase() error = %v, want %v", err, saveErr)
	}
	if items := live.Snapshot(); len(items) != 1 {
		t.Fatalf("Snapshot() after failed rebase = %#v, want the suffix retained", items)
	}
	if err := live.Rebase(nil); err != nil {
		t.Fatalf("Rebase(nil) error = %v", err)
	}
	if items := live.Snapshot(); len(items) != 0 {
		t.Fatalf("Snapshot() after rebase = %#v, want empty", items)
	}
}
