package tea

import (
	"errors"
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

func TestStaleActiveSideThreadReturnsToParentAndRestoresDraft(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("side-1")
	state.SetStatus("running")
	state.AddUserPromptMessage("我让你干什么了吗", "我让你干什么了吗", nil, nil, nil)

	model := NewModel(state, Options{Width: 80, Height: 24})
	side := &activeSideConversation{
		ParentThreadID:    "parent",
		SideThreadID:      "side-1",
		ParentMessages:    []codextui.Message{{Role: codextui.RoleUser, Text: "parent question"}},
		ParentStatus:      "idle",
		ParentPlaceholder: "Ask Codex in main thread",
		SideMessages:      cloneSideMessages(state.Messages),
		ShowingSide:       true,
	}
	model.activeSide = side
	model.retainSideConversation(side)
	model.submitRequests = []SubmitRequest{{Prompt: "我让你干什么了吗"}}

	// The transport emits the error event before TurnCompletedMsg. The event
	// clears the missing thread id, so recovery must identify the side by its
	// retained SideThreadID rather than the current State.ThreadID.
	model.Update(ThreadEventMsg{Event: protocol.ErrorEvent("thread not found: side-1")})
	model.Update(TurnCompletedMsg{Err: errors.New("thread not found: side-1")})

	if model.activeSide != nil {
		t.Fatalf("active side = %#v, want cleared", model.activeSide)
	}
	if state.ThreadID != "parent" {
		t.Fatalf("ThreadID = %q, want parent", state.ThreadID)
	}
	if _, retained := model.sideConversations["parent"]; retained {
		t.Fatal("expired side thread remained retained")
	}
	if len(state.Messages) != 1 || state.Messages[0].Text != "parent question" {
		t.Fatalf("parent transcript = %#v", state.Messages)
	}
	if !strings.Contains(model.notice, "Side conversation expired") || !strings.Contains(model.notice, "back in the composer") {
		t.Fatalf("notice = %q", model.notice)
	}
	if got := model.ComposerValue(); got != "我让你干什么了吗" {
		t.Fatalf("composer = %q, want failed side prompt", got)
	}
	if view := model.View(); strings.Contains(view, "thread not found: side-1") {
		t.Fatalf("stale side error leaked into the parent transcript:\n%s", view)
	}
}

func TestStaleSideThreadFailureIgnoresAnotherThread(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("side-1")
	state.AddUserPromptMessage("side question", "side question", nil, nil, nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	side := &activeSideConversation{
		ParentThreadID: "parent",
		SideThreadID:   "side-1",
		SideMessages:   cloneSideMessages(state.Messages),
		ShowingSide:    true,
	}
	model.activeSide = side
	model.retainSideConversation(side)

	model.Update(TurnCompletedMsg{Err: errors.New("thread not found: side-10")})

	if model.activeSide != side || !model.activeSide.ShowingSide {
		t.Fatal("unrelated thread failure closed the active side")
	}
	if got := model.ComposerValue(); got != "" {
		t.Fatalf("unrelated side failure restored a draft %q", got)
	}
}
