package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust chatwidget.rs keeps SIDE_PLACEHOLDER ("Ask a follow-up question")
// beside the normal PLACEHOLDER ("Ask Codex to do anything") and swaps them in
// chatwidget::side.rs set_side_conversation_active, restoring the main
// placeholder once the side conversation stops owning the composer.
func TestSideConversationPlaceholderLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	model.State.ThreadID = "parent-placeholder"
	model.activeSide = &activeSideConversation{
		ParentThreadID: "parent-placeholder",
		SideThreadID:   "side-placeholder",
		Storage:        SideChatStorageTemporary,
	}

	mainView := utils.StripANSI(model.viewString())
	if !strings.Contains(mainView, "Ask Codex to do anything") {
		t.Fatalf("main thread placeholder missing:\n%s", mainView)
	}

	model.activeSide.ShowingSide = true
	sideView := utils.StripANSI(model.viewString())
	if strings.Contains(sideView, "Ask Codex to do anything") {
		t.Fatalf("side conversation kept the main placeholder:\n%s", sideView)
	}
	if !strings.Contains(sideView, SideComposerPlaceholder) {
		t.Fatalf("side conversation is missing %q:\n%s", SideComposerPlaceholder, sideView)
	}

	model.activeSide.ShowingSide = false
	restored := utils.StripANSI(model.viewString())
	if !strings.Contains(restored, "Ask Codex to do anything") {
		t.Fatalf("returning to the main thread did not restore the placeholder:\n%s", restored)
	}
	if strings.Contains(restored, SideComposerPlaceholder) {
		t.Fatalf("main thread kept the side placeholder:\n%s", restored)
	}
}

// A prompt suggestion still replaces the base placeholder while the side
// conversation owns the composer, matching the main-thread behavior.
func TestSideConversationKeepsPromptSuggestionOverrideLikeRust(t *testing.T) {
	model := promptSuggestionTestModel(t, true)
	model.State.SetThreadID("thread-1")
	model.activeSide = &activeSideConversation{
		ParentThreadID: "thread-1",
		SideThreadID:   "side-suggestion",
		Storage:        SideChatStorageTemporary,
		ShowingSide:    true,
	}
	if got := model.activeComposerPlaceholder(); got != SideComposerPlaceholder {
		t.Fatalf("active placeholder = %q, want %q", got, SideComposerPlaceholder)
	}

	applyPromptSuggestionText(t, model, promptSuggestionTestRequest(), "Ask about the diff")
	if view := plainView(model.viewString()); !strings.Contains(view, "Ask about the diff") {
		t.Fatalf("side view missing the prompt suggestion:\n%s", view)
	}
}

// The parked pane keeps the main thread's placeholder because the main
// composer owns the draft while the side chat is hidden.
func TestParkedSideConversationUsesMainPlaceholderLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	model.State.ThreadID = "parent-parked"
	model.activeSide = &activeSideConversation{
		ParentThreadID: "parent-parked",
		SideThreadID:   "side-parked",
		Storage:        SideChatStorageAttached,
	}
	if got := model.activeComposerPlaceholder(); got != "Ask Codex to do anything" {
		t.Fatalf("parked side placeholder = %q, want the main placeholder", got)
	}
}
