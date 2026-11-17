package app

import (
	"path/filepath"
	"testing"

	"codex_go/session"
	codextea "codex_go/tui/tea"
)

// Rust side_fork_config (#60319) always forks a side conversation with
// Daybreak disabled, so a parent that saved Daybreak on cannot leak it into the
// child. The fork carries the override in its config so the server persists the
// same preference it would for a Rust side chat.
func TestSideChatForkDisablesDaybreakLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	newAttachedSideChatParent(t, store, "Release plan")
	enabled := true
	if _, err := store.UpdateMetadata(session.ThreadID("thread-parent"), &session.MetadataPatch{DaybreakEnabled: &enabled}, false); err != nil {
		t.Fatalf("UpdateMetadata(parent) error = %v", err)
	}

	coordinator := newInteractiveLocalSideCoordinator(store)
	for _, storage := range []codextea.SideChatStorage{codextea.SideChatStorageAttached, codextea.SideChatStorageTemporary} {
		response, err := coordinator.Start(codextea.SideStartParams{
			ParentThreadID:  "thread-parent",
			Storage:         storage,
			ReasoningEffort: "high",
		})
		if err != nil {
			t.Fatalf("Start(%v) error = %v", storage, err)
		}
		side, err := store.Read(session.ThreadID(response.SideThreadID), true, true)
		if err != nil {
			t.Fatalf("Read(side) error = %v", err)
		}
		if side.Metadata.DaybreakEnabled == nil || *side.Metadata.DaybreakEnabled {
			t.Fatalf("%v side daybreak = %#v, want an explicit false", storage, side.Metadata.DaybreakEnabled)
		}
		configValues, ok := side.Metadata.Extra["config"].(map[string]any)
		if !ok {
			t.Fatalf("%v side config = %#v", storage, side.Metadata.Extra["config"])
		}
		if got, ok := configValues["daybreak"]; !ok || got != false {
			t.Fatalf("%v side config daybreak = %#v, want false", storage, got)
		}
		if got := configValues["model_reasoning_effort"]; got != "high" {
			t.Fatalf("%v side reasoning effort = %#v, want high", storage, got)
		}
	}

	parent, err := store.Read(session.ThreadID("thread-parent"), true, true)
	if err != nil {
		t.Fatalf("Read(parent) error = %v", err)
	}
	if parent.Metadata.DaybreakEnabled == nil || !*parent.Metadata.DaybreakEnabled {
		t.Fatalf("parent daybreak = %#v, want it untouched", parent.Metadata.DaybreakEnabled)
	}
}
