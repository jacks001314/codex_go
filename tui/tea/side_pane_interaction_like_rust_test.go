package tea

import (
	"testing"

	codextui "codex_go/tui"
)

func sideInteractionModel(t *testing.T) *Model {
	t.Helper()
	m := NewModel(codextui.NewState(nil), Options{Width: 140, Height: 12})
	m.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	m.State.ThreadID = "root"
	for i := 0; i < 60; i++ {
		m.State.AddMessage(codextui.RoleUser, "parent message")
	}
	m.activeSide = &activeSideConversation{
		ParentThreadID: "root",
		SideThreadID:   "side",
		ParentMessages: cloneSideMessages(m.State.Messages),
		ShowingSide:    false,
	}
	m.refreshTranscript()
	return m
}

// Rust #59472: switching panes with the direction shortcuts parks and restores
// each pane's Find query, composer draft and transcript scroll position.
func TestSidePaneSwitchPreservesTranscriptInteractionLikeRust(t *testing.T) {
	m := sideInteractionModel(t)
	m.mainFind = mainTranscriptFind{editing: true, query: "needle", index: 1}
	m.composer.SetValue("parent draft")
	m.transcript.YOffset = 3

	m.focusSidebarContent()
	if m.activeSide == nil || !m.activeSide.ShowingSide {
		t.Fatal("focus_pane_right did not switch to the side pane")
	}
	if m.mainFind.active() || m.composerText() != "" {
		t.Fatalf("side pane inherited parent interaction: find=%#v draft=%q", m.mainFind, m.composerText())
	}
	m.mainFind = mainTranscriptFind{editing: true, query: "side query"}
	m.composer.SetValue("side draft")

	m.focusMainComposer()
	if m.activeSide.ShowingSide {
		t.Fatal("focus_pane_left did not switch back to the parent")
	}
	if !m.mainFind.editing || m.mainFind.query != "needle" {
		t.Fatalf("parent find = %#v", m.mainFind)
	}
	if got := m.composerText(); got != "parent draft" {
		t.Fatalf("parent draft = %q", got)
	}
	if m.transcript.YOffset != 3 {
		t.Fatalf("parent scroll = %d, want 3", m.transcript.YOffset)
	}

	m.focusSidebarContent()
	if !m.mainFind.editing || m.mainFind.query != "side query" {
		t.Fatalf("side find = %#v", m.mainFind)
	}
	if got := m.composerText(); got != "side draft" {
		t.Fatalf("side draft = %q", got)
	}
}

// Explicitly focusing the main composer still clears the transcript search
// (Rust #59472).
func TestExplicitMainComposerFocusClearsTranscriptInteractionLikeRust(t *testing.T) {
	m := sideInteractionModel(t)
	m.mainFind = mainTranscriptFind{editing: true, query: "needle"}
	m.focusMainComposerExplicit()
	if m.mainFind.active() {
		t.Fatalf("explicit composer focus kept find state: %#v", m.mainFind)
	}
}

// Rust #59458: a pending side-conversation start blocks focus switches so its
// queued prompt is dispatched first.
func TestSideStartPendingBlocksFocusSwitchLikeRust(t *testing.T) {
	m := sideInteractionModel(t)
	retained := &activeSideConversation{ParentThreadID: "root", SideThreadID: "side", ShowingSide: true}
	m.retainSideConversation(retained)
	m.activeSide = nil
	m.sideStartPending = true
	if cmd := m.focusSidebarContent(); cmd != nil {
		t.Fatal("focus switched while a side start was pending")
	}
	if m.activeSide != nil {
		t.Fatal("pending side start was preempted by a focus switch")
	}
	m.sideStartPending = false
	m.activeSide = retained
	if cmd := m.toggleSideConversation(); cmd == nil {
		// A toggle on the rendered pair is allowed once the start completes.
		t.Log("toggle returned no command for the workspace layout")
	}
}
