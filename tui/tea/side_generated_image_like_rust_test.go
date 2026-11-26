package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestGeneratedImageFollowsItsSideConversationLikeRust pins Rust
// app/side_companion.rs: each display keeps its own PendingGeneratedImages and
// takes it on that display's completion, so a side turn that finishes while the
// parent conversation is in front still shows its image with its own answer.
func TestGeneratedImageFollowsItsSideConversationLikeRust(t *testing.T) {
	m := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, LocalSession: true})
	m.ownedScreen = true
	path := writeTranscriptTestPNG(t, 32, 32)

	const parentThread = "thread-parent"
	const sideThread = "thread-side"
	const sideTurn = "turn-side-1"
	m.switchThreadID(sideThread)
	m.activeSide = &activeSideConversation{
		ParentThreadID: parentThread,
		SideThreadID:   sideThread,
		SideMessages:   []codextui.Message{},
		ShowingSide:    true,
	}

	m.Update(ThreadEventMsg{Event: protocol.TurnStartedWithID(sideTurn)})
	m.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ImageGenerationItem("img", "completed", "prompt", path))})
	for _, message := range m.State.Messages {
		if message.ImagePath != "" {
			t.Fatal("image rendered before its turn completed")
		}
	}

	// The reader switches back to the parent conversation before the side turn
	// finishes, which is what stranded the image before this change.
	m.activeSide.SideMessages = cloneSideMessages(m.State.Messages)
	m.activeSide.ShowingSide = false
	m.switchThreadID(parentThread)
	m.State.Messages = cloneSideMessages(m.activeSide.ParentMessages)
	parentRows := len(m.State.Messages)

	m.Update(ThreadEventMsg{Event: protocol.TurnCompletedWithID(protocol.Usage{}, sideTurn)})

	if len(m.pendingGeneratedImages) != 0 {
		t.Fatalf("pending images after completion = %d, want 0", len(m.pendingGeneratedImages))
	}
	if len(m.State.Messages) != parentRows {
		t.Fatalf("parent transcript grew from %d to %d rows; the side image leaked", parentRows, len(m.State.Messages))
	}
	found := 0
	for _, message := range m.activeSide.SideMessages {
		if message.ImagePath == "" {
			continue
		}
		found++
		if !message.ImageGenerated {
			t.Fatalf("side image lost its generated marker: %#v", message)
		}
		if !strings.Contains(message.Text, "prompt") {
			t.Fatalf("side image lost its label: %q", message.Text)
		}
	}
	if found != 1 {
		t.Fatalf("side transcript image rows = %d, want 1", found)
	}

	// Showing the side conversation again renders the retained row.
	m.activeSide.ShowingSide = true
	m.switchThreadID(sideThread)
	m.State.Messages = cloneSideMessages(m.activeSide.SideMessages)
	rendered := 0
	for _, message := range m.State.Messages {
		if message.ImagePath != "" {
			rendered++
		}
	}
	if rendered != 1 {
		t.Fatalf("visible side transcript image rows = %d, want 1", rendered)
	}
}
