package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// Rust app/side.rs `side_context_label` composes the composer context label
// from " · "-joined parts: the parent identity, the parent's actionable
// status, the toggle binding, and the close action. A saved side chat in a
// workspace session archives on ctrl-c, so the hint has to say "archive".
// Mirrors codex-rs/tui/src/chatwidget/tests/side.rs:302/323/344 and
// app/tests.rs:8274-8276.
func sideContextLabelModel(t *testing.T) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	model.State.ThreadID = "parent-24"
	model.activeSide = &activeSideConversation{
		ParentThreadID: "parent-24",
		SideThreadID:   "child-24",
		Storage:        SideChatStorageTemporary,
		ShowingSide:    true,
	}
	return model
}

func TestSideContextLabelJoinsRustPartsLikeRust(t *testing.T) {
	closeHint := codextui.ModifierLabelPrefix(codextui.ControlKeyLabel()) + "c to close"
	model := sideContextLabelModel(t)
	want := "Side from main thread \u00b7 ctrl+/ to switch \u00b7 " + closeHint
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("side context label = %q, want %q", got, want)
	}

	model.activeSide.ParentSideStatus = SideParentStatusNeedsInput
	want = "Side from main thread \u00b7 main needs input \u00b7 ctrl+/ to switch \u00b7 " + closeHint
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("side context label with parent status = %q, want %q", got, want)
	}

	model.activeSide.ParentSideStatus = SideParentStatusNeedsApproval
	want = "Side from main thread \u00b7 main needs approval \u00b7 ctrl+/ to switch \u00b7 " + closeHint
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("side context label with approval status = %q, want %q", got, want)
	}
}

func TestSideContextLabelArchivesSavedWorkspaceSideLikeRust(t *testing.T) {
	closeHint := codextui.ModifierLabelPrefix(codextui.ControlKeyLabel()) + "c to archive"
	model := sideContextLabelModel(t)
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.activeSide.Storage = SideChatStorageAttached
	want := "Side from main thread \u00b7 ctrl+/ to switch \u00b7 " + closeHint
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("saved workspace side label = %q, want %q", got, want)
	}

	// The legacy temporary fork is deleted, and Rust keeps "close" when the
	// workspace feature is off even for a saved side chat.
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: false}
	model.activeSide.Storage = SideChatStorageAttached
	want = "Side from main thread \u00b7 ctrl+/ to switch \u00b7 " +
		codextui.ModifierLabelPrefix(codextui.ControlKeyLabel()) + "c to close"
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("saved side label without workspace = %q, want %q", got, want)
	}
}

func TestSideContextLabelNamesNonPrimaryParentLikeRust(t *testing.T) {
	model := sideContextLabelModel(t)
	model.agentItems = []codextui.AgentThreadEntry{{
		ThreadID:  "parent-24",
		AgentPath: "agents/reviewer",
	}}
	want := "Side from parent thread (agents/reviewer) \u00b7 ctrl+/ to switch \u00b7 " +
		codextui.ModifierLabelPrefix(codextui.ControlKeyLabel()) + "c to close"
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("side context label for a subagent parent = %q, want %q", got, want)
	}
}

func TestSideContextLabelHidesToggleWithoutBindingLikeRust(t *testing.T) {
	model := sideContextLabelModel(t)
	model.keymapConfig = codextui.NewKeymapConfig()
	if err := model.keymapConfig.Set("global", "toggle_side_conversation", nil); err != nil {
		t.Fatalf("clear toggle binding: %v", err)
	}
	if got := model.sideContextLabel(); got != "Side from main thread \u00b7 ctrl+c to close" {
		t.Fatalf("side context label without a toggle binding = %q", got)
	}

	model.activeSide.ShowingSide = false
	if got := model.sideContextLabel(); got != "" {
		t.Fatalf("hidden side label without a toggle binding = %q, want empty", got)
	}
}
