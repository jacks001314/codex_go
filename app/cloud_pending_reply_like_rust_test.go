package app

import (
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// TestCloudPendingReplyReservesPositionLikeRust mirrors Rust #62403: a Cloud
// reply restored from an in-progress turn without visible text keeps a
// transcript slot so the later completion fills it in place.
func TestCloudPendingReplyReservesPositionLikeRust(t *testing.T) {
	thread := &appserver.Thread{
		CWD: t.TempDir(),
		Turns: []appserver.Turn{{
			ID:     "turn-1",
			Status: appserver.TurnStatusInProgress,
			Items: []appserver.ThreadItem{
				{ID: "user-1", Type: "user_message", Role: "user", Text: "hello"},
				{ID: "msg-pending", Type: "agent_message", Role: "assistant"},
				{ID: "msg-later", Type: "agent_message", Role: "assistant", Text: "later reply"},
			},
		}},
	}
	messages := remoteTUIThreadMessagesFromThread(thread, reasoningProjectionChatWidget, false)
	var pending []int
	for index, message := range messages {
		if message.ItemID == "msg-pending" {
			pending = append(pending, index)
		}
	}
	if len(pending) != 1 {
		t.Fatalf("pending replies = %v, want one reserved position in %#v", pending, messages)
	}
	reserved := messages[pending[0]]
	if reserved.Role != codextui.RoleAssistant || reserved.Text != "" {
		t.Fatalf("reserved reply = %#v, want an invisible assistant slot", reserved)
	}
	later := -1
	for index, message := range messages {
		if message.ItemID == "msg-later" {
			later = index
		}
	}
	if later < 0 || later < pending[0] {
		t.Fatalf("later reply index = %d, pending index = %d", later, pending[0])
	}
}

// TestCloudFinishedEmptyReplyStaysHiddenLikeRust covers the other half of the
// finished check: a turn that has completed owns an empty reply, so no
// placeholder is kept for it.
func TestCloudFinishedEmptyReplyStaysHiddenLikeRust(t *testing.T) {
	thread := &appserver.Thread{
		CWD: t.TempDir(),
		Turns: []appserver.Turn{{
			ID:     "turn-2",
			Status: appserver.TurnStatusCompleted,
			Items: []appserver.ThreadItem{
				{ID: "user-1", Type: "user_message", Role: "user", Text: "hello"},
				{ID: "msg-empty", Type: "agent_message", Role: "assistant"},
			},
		}},
	}
	messages := remoteTUIThreadMessagesFromThread(thread, reasoningProjectionChatWidget, false)
	for _, message := range messages {
		if message.ItemID == "msg-empty" {
			t.Fatalf("finished empty reply kept a placeholder: %#v", message)
		}
	}
}
