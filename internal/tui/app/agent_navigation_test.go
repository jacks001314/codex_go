package app

import "testing"

func populatedAgentNavigationState() (*AgentNavigationState, string, string, string) {
	state := NewAgentNavigationState()
	mainThreadID := "00000000-0000-0000-0000-000000000101"
	firstAgentID := "00000000-0000-0000-0000-000000000102"
	secondAgentID := "00000000-0000-0000-0000-000000000103"

	state.Upsert(mainThreadID, "", "", false)
	state.Upsert(firstAgentID, "Robie", "explorer", false)
	state.Upsert(secondAgentID, "Bob", "worker", false)

	return state, mainThreadID, firstAgentID, secondAgentID
}

func TestAgentNavigationUpsertPreservesFirstSeenOrderMatchRust(t *testing.T) {
	state, mainThreadID, firstAgentID, secondAgentID := populatedAgentNavigationState()

	state.Upsert(firstAgentID, "Robie", "worker", true)

	want := []string{mainThreadID, firstAgentID, secondAgentID}
	got := state.TrackedThreadIDs()
	if len(got) != len(want) {
		t.Fatalf("order len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %#v, want %#v", got, want)
		}
	}
	entry, ok := state.Get(firstAgentID)
	if !ok || !entry.IsClosed || entry.IsRunning {
		t.Fatalf("updated closed entry = %#v ok=%v", entry, ok)
	}
}

func TestAgentNavigationAdjacentThreadIDWrapsInSpawnOrderMatchRust(t *testing.T) {
	state, mainThreadID, firstAgentID, secondAgentID := populatedAgentNavigationState()

	if got, ok := state.AdjacentThreadID(secondAgentID, AgentNavigationNext); !ok || got != mainThreadID {
		t.Fatalf("next from second = %q ok=%v, want main", got, ok)
	}
	if got, ok := state.AdjacentThreadID(secondAgentID, AgentNavigationPrevious); !ok || got != firstAgentID {
		t.Fatalf("previous from second = %q ok=%v, want first", got, ok)
	}
	if got, ok := state.AdjacentThreadID(mainThreadID, AgentNavigationPrevious); !ok || got != secondAgentID {
		t.Fatalf("previous from main = %q ok=%v, want second", got, ok)
	}
	if got, ok := state.AdjacentThreadID("missing", AgentNavigationNext); ok || got != "" {
		t.Fatalf("missing adjacent = %q ok=%v", got, ok)
	}
}

func TestAgentNavigationActiveAgentLabelMatchRust(t *testing.T) {
	state, mainThreadID, firstAgentID, _ := populatedAgentNavigationState()

	if got, ok := state.ActiveAgentLabel(firstAgentID, mainThreadID); !ok || got != "Robie [explorer]" {
		t.Fatalf("first label = %q ok=%v", got, ok)
	}
	if got, ok := state.ActiveAgentLabel(mainThreadID, mainThreadID); !ok || got != "Main [default]" {
		t.Fatalf("main label = %q ok=%v", got, ok)
	}
	if got, ok := state.ActiveAgentLabel("missing", mainThreadID); !ok || got != "Agent" {
		t.Fatalf("missing label = %q ok=%v", got, ok)
	}
}

func TestAgentNavigationRecordActivityPathBackedRemoveAndSubtitle(t *testing.T) {
	state, mainThreadID, firstAgentID, _ := populatedAgentNavigationState()
	state.RecordSubAgentActivity(SubAgentActivityDisplay{
		ThreadID:      firstAgentID,
		AgentPath:     "agents/reviewer.md",
		IsRunningHint: true,
	})

	if got, ok := state.ActiveAgentLabel(firstAgentID, mainThreadID); !ok || got != "`agents/reviewer.md`" {
		t.Fatalf("path label = %q ok=%v", got, ok)
	}
	pathBacked := state.OrderedPathBackedSubagentThreads(mainThreadID)
	if len(pathBacked) != 1 || pathBacked[0].ThreadID != firstAgentID {
		t.Fatalf("path backed = %#v", pathBacked)
	}
	if !state.HasNonPrimaryThread(mainThreadID) {
		t.Fatal("expected non-primary thread")
	}
	state.Remove(firstAgentID)
	if _, ok := state.Get(firstAgentID); ok {
		t.Fatalf("removed thread still present")
	}
	if got := AgentNavigationPickerSubtitle(); got != "Select an agent to watch. Alt+Left previous, Alt+Right next." {
		t.Fatalf("subtitle = %q", got)
	}
}

func TestAgentNavigationPickerItemsMatchRust(t *testing.T) {
	state, mainThreadID, firstAgentID, _ := populatedAgentNavigationState()
	state.MarkClosed(firstAgentID)

	items, selected := state.PickerItems(firstAgentID, mainThreadID)
	if selected != 1 {
		t.Fatalf("selected = %d, want 1", selected)
	}
	if len(items) != 3 {
		t.Fatalf("items len = %d, want 3: %#v", len(items), items)
	}
	if items[0].Name != "Main [default]" || !items[0].IsPrimary || items[0].Description != mainThreadID {
		t.Fatalf("main item = %#v", items[0])
	}
	if items[1].Name != "Robie [explorer]" || !items[1].IsCurrent || !items[1].IsClosed || items[1].SearchValue != "Robie [explorer] "+firstAgentID {
		t.Fatalf("agent item = %#v", items[1])
	}

	state.Clear()
	if len(state.OrderedThreadIDs()) != 0 || !state.IsEmpty() {
		t.Fatalf("clear did not reset state: %#v", state.OrderedThreadIDs())
	}
	state.MarkClosed("thread-closed")
	entry, ok := state.Get("thread-closed")
	if !ok || !entry.IsClosed || entry.IsRunning {
		t.Fatalf("mark closed missing entry = %#v ok=%v", entry, ok)
	}
}
