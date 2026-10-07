package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// Mirrors Rust #46494: a side conversation is a Session-sourced fork, so the
// start params forward the active session's server-resolved runtime workspace
// roots (the session state tracks what the server reported).
func TestSideCommandForwardsSessionWorkspaceRootsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	var captured SideStartParams
	model := NewModel(state, Options{
		Width: 80, Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			captured = params
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
	})
	model.applyThreadSettingsValues(appserver.Settings{RuntimeWorkspaceRoots: []string{"/repo", "/repo/lib"}})

	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)

	if len(captured.RuntimeWorkspaceRoots) != 2 || captured.RuntimeWorkspaceRoots[0] != "/repo" || captured.RuntimeWorkspaceRoots[1] != "/repo/lib" {
		t.Fatalf("side workspace roots = %#v, want [/repo /repo/lib]", captured.RuntimeWorkspaceRoots)
	}
}

func TestSideTogglePreservesParentAndSideSnapshots(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	state.AddMessage(codextui.RoleUser, "main question")
	model := NewModel(state, Options{
		Width: 80, Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{
				ParentThreadID: params.ParentThreadID,
				SideThreadID:   "thread-side",
			}, nil
		},
	})

	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	state.AddMessage(codextui.RoleUser, "side question")

	runTeaCmd(t, model, model.toggleSideConversation())
	if state.ThreadID != "thread-parent" ||
		len(state.Messages) != 1 ||
		state.Messages[0].Text != "main question" {
		t.Fatalf("parent snapshot = thread %q messages %#v", state.ThreadID, state.Messages)
	}
	state.AddMessage(codextui.RoleAssistant, "main answer")

	runTeaCmd(t, model, model.toggleSideConversation())
	if state.ThreadID != "thread-side" ||
		len(state.Messages) != 1 ||
		state.Messages[0].Text != "side question" {
		t.Fatalf("side snapshot = thread %q messages %#v", state.ThreadID, state.Messages)
	}
	if model.activeSide == nil {
		t.Fatal("toggle closed the side conversation")
	}
}

// Rust #49804 (d2f2c40095, codex-rs/tui/src/app/side.rs side_context_label):
// the side-conversation context label builds its close hint from the shared
// key-label table, so Linux shows ^c while macOS shows the control glyph.
// Mirrors the Rust `Side from main thread · ⌃/ to switch · ⌃c to close`
// expectations in codex-rs/tui/src/app/tests.rs.
func TestSideContextLabelUsesPlatformControlLabelLikeRust(t *testing.T) {
	ctrl := codextui.ModifierLabelPrefix(codextui.ControlKeyLabel())
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	model.activeSide = &activeSideConversation{ShowingSide: true}

	got := model.sideContextLabel()
	if !strings.Contains(got, "ctrl+/ to switch") || !strings.Contains(got, ctrl+"c to close") {
		t.Fatalf("side context label = %q, want close hint %q", got, ctrl+"c to close")
	}
	if ctrl != "ctrl+" && strings.Contains(got, "ctrl+c to close") {
		t.Fatalf("side context label still hard-codes ctrl+c: %q", got)
	}

	model.activeSide.ShowingSide = false
	if got := model.sideContextLabel(); got != "ctrl+/ for side" {
		t.Fatalf("hidden side label = %q", got)
	}
}
