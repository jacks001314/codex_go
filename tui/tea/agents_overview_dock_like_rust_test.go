package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	mousemsg "codex_go/tui/mousemsg"
)

func newAgentsOverviewDockModel(t *testing.T, width int) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("parent-a")
	state.AddMessage(codextui.RoleUser, "hello from the main chat")
	model := NewModel(state, Options{
		Width:  width,
		Height: 30,
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{
				Entry:    codextui.AgentThreadEntry{ThreadID: threadID, ParentThreadID: "parent-a"},
				Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "task reply"}},
				Status:   "idle",
			}, nil
		},
		OnSubmit: func(string) bubbletea.Cmd { return nil },
	})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	// Rust docks only in owned transcript mode.
	model.ownedScreen = true
	model.refreshTranscript()
	model.applyAgentsCommand()
	return model
}

// Rust #62986: on a wide terminal the Command Center docks left of the
// conversation, so the transcript and composer stay visible while browsing
// tasks.
func TestAgentsOverviewDocksBesideConversationLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 120)
	if !model.agentsOverviewDockOpen() {
		t.Fatal("wide terminal did not dock the Command Center")
	}
	if model.agentsOverviewDockFocus != agentsOverviewDockCenter {
		t.Fatalf("dock focus = %q, want the center", model.agentsOverviewDockFocus)
	}
	painted := ansi.Strip(model.viewString())
	if !strings.Contains(painted, "│") {
		t.Fatalf("dock painted no divider:\n%s", painted)
	}
	if !strings.Contains(painted, "hello from the main chat") {
		t.Fatalf("docked workspace lost the conversation:\n%s", painted)
	}
	if !strings.Contains(painted, "Group:") {
		t.Fatalf("docked workspace lost the Command Center:\n%s", painted)
	}
	// The conversation column keeps its composer.
	if !strings.Contains(painted, "Ask Codex") {
		t.Fatalf("docked conversation lost its composer:\n%s", painted)
	}
}

// A narrow terminal keeps the dashboard's full-screen path; Rust only docks
// when the split fits a 48-column conversation.
func TestAgentsOverviewNarrowStaysFullScreenLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 80)
	if model.agentsOverviewDockOpen() {
		t.Fatal("narrow terminal docked the Command Center")
	}
	painted := ansi.Strip(model.viewString())
	if strings.Contains(painted, "hello from the main chat") {
		t.Fatalf("narrow dashboard painted the conversation:\n%s", painted)
	}
	if strings.Contains(painted, "│") {
		t.Fatalf("narrow dashboard painted a dock divider:\n%s", painted)
	}
}

// Rust #62986 narrow workspace: without room for both panes the pane that
// owns input fills the screen, so a focused conversation still paints the
// transcript and keeps its keys instead of falling back to the dashboard.
func TestAgentsOverviewNarrowFocusedConversationFillsScreenLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 80)
	if !model.focusAgentsOverviewDockConversation() {
		t.Fatal("narrow workspace did not hand input to the conversation")
	}
	painted := ansi.Strip(model.viewString())
	if !strings.Contains(painted, "hello from the main chat") {
		t.Fatalf("narrow focused conversation painted no transcript:\n%s", painted)
	}
	if strings.Contains(painted, "Group:") {
		t.Fatalf("narrow focused conversation still painted the dashboard:\n%s", painted)
	}

	model.Update(runes("Hi"))
	if model.agentsOverview.Composing() {
		t.Fatal("conversation-focused rune reached the Command Center composer")
	}
	if got := model.composerText(); got != "Hi" {
		t.Fatalf("conversation composer = %q, want the typed draft", got)
	}

	if !model.focusAgentsOverviewDockCenter() {
		t.Fatal("narrow workspace did not return input to the dashboard")
	}
	painted = ansi.Strip(model.viewString())
	if !strings.Contains(painted, "Group:") || strings.Contains(painted, "hello from the main chat") {
		t.Fatalf("returning to the center did not restore the dashboard:\n%s", painted)
	}
}

// Rust #62986 workspace focus: focus_main_composer hands input to the
// conversation and the dock stays painted; focus_pane_left (or open_agents)
// gives it back to the center.
func TestAgentsOverviewDockFocusMovesLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 120)
	_ = model.viewString()

	model.Update(runes("Z"))
	if !model.agentsOverview.Composing() {
		t.Fatal("Center-focused rune did not reach the Command Center composer")
	}
	if got := model.composerText(); got != "" {
		t.Fatalf("parent composer received %q while the center owned input", got)
	}

	if !model.focusAgentsOverviewDockConversation() {
		t.Fatal("dock did not hand input to the conversation")
	}
	model.Update(runes("Hi"))
	if got := model.composerText(); got != "Hi" {
		t.Fatalf("conversation composer = %q, want the typed draft", got)
	}
	if model.agentsOverview == nil {
		t.Fatal("moving focus closed the dock")
	}

	if !model.focusAgentsOverviewDockCenter() {
		t.Fatal("dock did not take input back")
	}
	if got := model.composerText(); got != "Hi" {
		t.Fatalf("conversation draft = %q after returning to the center", got)
	}
	model.Update(runes("y"))
	if got := model.composerText(); got != "Hi" {
		t.Fatalf("center-focused rune changed the conversation draft to %q", got)
	}

	// Rust handle_workspace_key treats focus_pane_right like
	// focus_main_composer and hands the workspace to the conversation.
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlX})
	model.Update(runes("l"))
	if !model.agentsOverviewDockConversationFocused() {
		t.Fatalf("focus_pane_right left focus at %q, want the conversation", model.agentsOverviewDockFocus)
	}
}

// Rust #62986: opening a task returns focus to the conversation and keeps the
// dock open instead of replacing the workspace with the dashboard.
func TestAgentsOverviewDockStaysAfterOpeningTaskLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 120)
	model.agentsOverviewDockFocus = agentsOverviewDockCenter
	cmd := model.openAgentsOverviewThread("thread-b")
	if cmd == nil {
		t.Fatal("opening a task returned no command")
	}
	runTeaCmd(t, model, cmd)
	if model.agentsOverview == nil {
		t.Fatal("opening a task closed the dock")
	}
	if model.agentsOverviewDockFocus != agentsOverviewDockConversation {
		t.Fatalf("dock focus after opening = %q, want the conversation", model.agentsOverviewDockFocus)
	}
	if model.State == nil || model.State.ThreadID != "thread-b" {
		t.Fatalf("active thread = %v, want the opened task", model.State)
	}
	painted := ansi.Strip(model.viewString())
	if !strings.Contains(painted, "task reply") || !strings.Contains(painted, "Group:") {
		t.Fatalf("dock after opening lost a pane:\n%s", painted)
	}
}

var _ = keymsg.KeyMsg{}

// Rust #62986 workspace mouse: a press chooses the focused pane, and dragging
// the divider resizes the dock while the conversation keeps 48 columns.
func TestAgentsOverviewDockMouseFocusAndResizeLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 120)
	_ = model.viewString()
	centerWidth, _, ok := model.agentsOverviewDockWidths()
	if !ok || centerWidth != agentsOverviewDockCenterWidth {
		t.Fatalf("dock width = %d ok=%t, want the default %d", centerWidth, ok, agentsOverviewDockCenterWidth)
	}

	model.Update(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
		X: centerWidth + agentsOverviewDockGutter + 4, Y: 3,
	})
	if !model.agentsOverviewDockConversationFocused() {
		t.Fatal("a press on the conversation column kept the dock focused")
	}

	model.Update(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: 2, Y: 3,
	})
	if model.agentsOverviewDockFocus != agentsOverviewDockCenter {
		t.Fatalf("a press on the dock left focus at %q", model.agentsOverviewDockFocus)
	}

	model.Update(mousemsg.MouseMsg{Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: centerWidth, Y: 2})
	model.Update(mousemsg.MouseMsg{Action: mousemsg.MouseActionMotion, Button: mousemsg.MouseButtonLeft, X: 50, Y: 2})
	model.Update(mousemsg.MouseMsg{Action: mousemsg.MouseActionRelease, Button: mousemsg.MouseButtonLeft, X: 50, Y: 2})
	if got, conversation, ok := model.agentsOverviewDockWidths(); !ok || got != 50 || conversation != 120-50-agentsOverviewDockGutter {
		t.Fatalf("dragged dock = center %d conversation %d ok %t", got, conversation, ok)
	}
	painted := ansi.Strip(model.viewString())
	if !strings.Contains(painted, "hello from the main chat") {
		t.Fatalf("resized dock lost the conversation:\n%s", painted)
	}
}

// Rust #62990: leader a opens the Command Center by default, and focuses the
// dock that is already beside the conversation.
func TestLeaderAOpensOrFocusesCommandCenterLikeRust(t *testing.T) {
	model := newAgentsOverviewDockModel(t, 120)
	model.closeAgentsOverview()
	if model.agentsOverview != nil {
		t.Fatal("close left the Command Center open")
	}
	actions, _, ok := model.leaderBindingsFor("ctrl-x", nil)
	if !ok || actions["a"] != "global.open_agents" {
		t.Fatalf("leader a = %q, want global.open_agents", actions["a"])
	}

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlX})
	model.Update(runes("a"))
	if model.agentsOverview == nil {
		t.Fatal("leader a did not open the Command Center")
	}
	if !model.agentsOverviewDockOpen() || model.agentsOverviewDockFocus != agentsOverviewDockCenter {
		t.Fatalf("opened dock = open %t focus %q", model.agentsOverviewDockOpen(), model.agentsOverviewDockFocus)
	}

	if !model.focusAgentsOverviewDockConversation() {
		t.Fatal("conversation did not take focus")
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyCtrlX})
	model.Update(runes("a"))
	if model.agentsOverviewDockFocus != agentsOverviewDockCenter {
		t.Fatalf("leader a left focus at %q, want the dock", model.agentsOverviewDockFocus)
	}
}
