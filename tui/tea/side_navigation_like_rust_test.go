package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

func newSideNavigationModel(
	t *testing.T,
	switchAgent func(string) (AgentThreadSwitchResponse, error),
) (*Model, *int, *[]SideCloseParams) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("parent-a")
	state.AddMessage(codextui.RoleUser, "main question")
	startCalls := 0
	var closed []SideCloseParams
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			startCalls++
			return SideStartResponse{
				ParentThreadID: params.ParentThreadID,
				SideThreadID:   "side-" + params.ParentThreadID,
			}, nil
		},
		OnCloseSide: func(params SideCloseParams) (SideCloseResponse, error) {
			closed = append(closed, params)
			return SideCloseResponse{}, nil
		},
		OnSwitchAgent: switchAgent,
	})
	return model, &startCalls, &closed
}

func openSideForTest(t *testing.T, model *Model) {
	t.Helper()
	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
}

func resumeThreadForTest(model *Model, threadID string, messages ...codextui.Message) {
	model.applyResumeResponse(threadID, SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: threadID},
		Messages: messages,
	})
}

// Rust #52184: leaving a side conversation must not delete it. Returning to
// the parent and invoking /side reopens the same side thread and transcript.
func TestRetainedSideConversationReopensAfterThreadNavigationLikeRust(t *testing.T) {
	model, startCalls, closed := newSideNavigationModel(t, nil)

	openSideForTest(t, model)
	model.State.AddMessage(codextui.RoleUser, "side question")

	resumeThreadForTest(model, "thread-other")
	if model.activeSide != nil {
		t.Fatal("thread navigation left the side conversation visible")
	}
	if len(model.sideConversations) != 1 {
		t.Fatalf("retained sides = %d, want 1", len(model.sideConversations))
	}

	resumeThreadForTest(model, "parent-a")
	openSideForTest(t, model)

	if *startCalls != 1 {
		t.Fatalf("side starter calls = %d, want the retained side to be reused", *startCalls)
	}
	if len(*closed) != 0 {
		t.Fatalf("navigation closed a side conversation: %#v", *closed)
	}
	if model.State.ThreadID != "side-parent-a" {
		t.Fatalf("reopened thread = %q, want side-parent-a", model.State.ThreadID)
	}
	if !messagesContainText(model.State.Messages, "side question") {
		t.Fatalf("reopened side lost its transcript: %#v", model.State.Messages)
	}
}

// A retained side keeps consuming thread-scoped events while the user is
// viewing another conversation. Reopening it must show those events rather
// than requiring a new side fork.
func TestRetainedSideTracksBackgroundEventsLikeRust(t *testing.T) {
	model, _, _ := newSideNavigationModel(t, nil)

	openSideForTest(t, model)
	resumeThreadForTest(model, "thread-other")
	model.Update(ThreadScopedEventMsg{
		ThreadID: "side-parent-a",
		Event:    protocol.AgentMessageDelta("side-item", "side background answer"),
	})
	model.Update(ThreadScopedEventMsg{
		ThreadID: "parent-a",
		Event:    protocol.AgentMessageDelta("parent-item", "parent background answer"),
	})

	side := model.retainedSideConversationForParent("parent-a")
	if side == nil {
		t.Fatal("side conversation was not retained")
	}
	if !messagesContainText(side.SideMessages, "side background answer") {
		t.Fatalf("side snapshot missed background delta: %#v", side.SideMessages)
	}
	if !messagesContainText(side.ParentMessages, "parent background answer") {
		t.Fatalf("parent snapshot missed background delta: %#v", side.ParentMessages)
	}

	resumeThreadForTest(model, "parent-a")
	openSideForTest(t, model)
	if view := model.View(); !strings.Contains(view, "side background answer") {
		t.Fatalf("reopened side did not render its background event:\n%s", view)
	}
	runTeaCmd(t, model, model.toggleSideConversation())
	if view := model.View(); !strings.Contains(view, "parent background answer") {
		t.Fatalf("returning to the parent did not render its background event:\n%s", view)
	}
}

// Rust #52184's discard path belongs only to the active side. Closing side A
// must leave side B available when the user later returns to parent B.
func TestExplicitSideReturnDiscardsOnlyTheActiveSideLikeRust(t *testing.T) {
	model, startCalls, closed := newSideNavigationModel(t, nil)

	openSideForTest(t, model)
	resumeThreadForTest(model, "parent-b")
	openSideForTest(t, model)
	if *startCalls != 2 {
		t.Fatalf("side starter calls = %d, want two parents to start independent sides", *startCalls)
	}

	resumeThreadForTest(model, "parent-a")
	openSideForTest(t, model)
	_, cmd := model.Update(key(bubbletea.KeyCtrlC))
	runTeaCmd(t, model, cmd)
	if len(*closed) != 1 || (*closed)[0].SideThreadID != "side-parent-a" {
		t.Fatalf("closed sides = %#v, want only side-parent-a", *closed)
	}

	resumeThreadForTest(model, "parent-b")
	openSideForTest(t, model)
	if *startCalls != 2 {
		t.Fatalf("side starter calls = %d after reopening parent B", *startCalls)
	}
	if model.State.ThreadID != "side-parent-b" {
		t.Fatalf("thread after reopening parent B = %q", model.State.ThreadID)
	}
}

// Switching to a subagent is thread navigation, not an explicit side-return
// action, so it must preserve the side listener and snapshots.
func TestAgentSwitchRetainsSideConversationLikeRust(t *testing.T) {
	switchCalls := 0
	model, startCalls, closed := newSideNavigationModel(t, func(threadID string) (AgentThreadSwitchResponse, error) {
		switchCalls++
		return AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: threadID, AgentNickname: "worker"},
			Status: "idle",
		}, nil
	})

	openSideForTest(t, model)
	model.State.AddMessage(codextui.RoleUser, "keep this side")
	runTeaCmd(t, model, model.applyAgentModalOption("thread-worker"))

	if switchCalls != 1 {
		t.Fatalf("agent switch calls = %d, want 1", switchCalls)
	}
	if len(*closed) != 0 {
		t.Fatalf("agent switch closed the side conversation: %#v", *closed)
	}
	if model.activeSide != nil || model.retainedSideConversationForParent("parent-a") == nil {
		t.Fatal("agent switch did not retain the side conversation")
	}

	resumeThreadForTest(model, "parent-a")
	openSideForTest(t, model)
	if *startCalls != 1 {
		t.Fatalf("side starter calls = %d, want retained side", *startCalls)
	}
	if !messagesContainText(model.State.Messages, "keep this side") {
		t.Fatalf("retained side lost its transcript: %#v", model.State.Messages)
	}
}

func messagesContainText(messages []codextui.Message, want string) bool {
	for _, message := range messages {
		if strings.Contains(message.Text, want) {
			return true
		}
	}
	return false
}
