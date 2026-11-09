package tea

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	mousemsg "codex_go/tui/mousemsg"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func runTeaCmdChain(t *testing.T, model *Model, cmd bubbletea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 6 {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(bubbletea.BatchMsg); ok {
		for _, sub := range batch {
			runTeaCmdChain(t, model, sub, depth+1)
		}
		return
	}
	_, next := model.Update(msg)
	runTeaCmdChain(t, model, next, depth+1)
}

// Rust #60319: the chooser discovers saved side chats, keeps them when a
// refresh runs, and paints the latest user prompt beneath each title.
func TestSidebarChooserDiscoveryAndPromptPreviewsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	listCalls := 0
	promptCalls := 0
	var requested []string
	model.onListSideChats = func(parent string) ([]SideChatChoice, error) {
		listCalls++
		return []SideChatChoice{
			{ThreadID: "saved-1", Name: "Auth refactor"},
			{ThreadID: "saved-2", Preview: "why does this fail"},
		}, nil
	}
	model.onReadSideChatPrompts = func(ids []string) map[string]string {
		promptCalls++
		requested = append([]string(nil), ids...)
		prompts := map[string]string{}
		for _, id := range ids {
			prompts[id] = "latest prompt for " + id
		}
		return prompts
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	if listCalls != 1 {
		t.Fatalf("saved-chat discovery calls = %d, want 1", listCalls)
	}
	if model.sidebarDiscovery != SideChatDiscoveryIdle {
		t.Fatalf("discovery = %q, want idle", model.sidebarDiscovery)
	}
	panel := model.renderSidebarChooser(80, 24)
	for _, want := range []string{"Auth refactor", "why does this fail"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("chooser missing %q:\n%s", want, panel)
		}
	}
	if promptCalls != 1 || len(requested) != 2 {
		t.Fatalf("prompt fetch calls=%d ids=%#v, want the two visible chats", promptCalls, requested)
	}
	panel = model.renderSidebarChooser(80, 24)
	for _, want := range []string{"latest prompt for saved-1", "latest prompt for saved-2"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("chooser missing preview %q:\n%s", want, panel)
		}
	}
}

// Rust #60319: a failed discovery keeps cached chats, shows the error, and
// offers an inline retry row that re-runs the refresh.
func TestSidebarChooserDiscoveryFailureRetryLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	calls := 0
	model.onListSideChats = func(parent string) ([]SideChatChoice, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("boom")
		}
		return []SideChatChoice{{ThreadID: "saved-1", Name: "Recovered"}}, nil
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	if model.sidebarDiscovery != SideChatDiscoveryFailed {
		t.Fatalf("discovery = %q, want failed", model.sidebarDiscovery)
	}
	panel := model.renderSidebarChooser(80, 24)
	for _, want := range []string{"Failed to load saved side chats: boom", sidebarRetryChatsTitle} {
		if !strings.Contains(panel, want) {
			t.Fatalf("failed chooser missing %q:\n%s", want, panel)
		}
	}
	if retry := model.sidebarChooserRetryRow(); model.sidebarChoice != retry {
		t.Fatalf("selection = %d, want the retry row %d", model.sidebarChoice, retry)
	}
	cmd := model.acceptSidebarChooser()
	if model.sidebarDiscovery != SideChatDiscoveryLoading || !model.sidebarDiscoveryRetry {
		t.Fatalf("retry did not start a loading refresh: %#v", model.sidebarDiscovery)
	}
	if panel := model.renderSidebarChooser(80, 24); !strings.Contains(panel, sidebarRetryingChatsTitle) {
		t.Fatalf("retrying chooser missing %q:\n%s", sidebarRetryingChatsTitle, panel)
	}
	runTeaCmdChain(t, model, cmd, 0)
	if calls != 2 {
		t.Fatalf("saved-chat discovery calls = %d, want 2", calls)
	}
	panel = model.renderSidebarChooser(80, 24)
	if !strings.Contains(panel, "Recovered") || strings.Contains(panel, sidebarRetryChatsTitle) {
		t.Fatalf("retried chooser =\n%s", panel)
	}
}

// Rust #60319: preview rows count toward paging, so a page moves by fewer
// entries when each chat paints its prompt.
func TestSidebarChooserPagingAccountsForPreviewRowsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	choices := make([]SideChatChoice, 0, 10)
	for index := 1; index <= 10; index++ {
		choices = append(choices, SideChatChoice{ThreadID: fmt.Sprintf("saved-%d", index), Name: fmt.Sprintf("Chat %d", index)})
	}
	model.onListSideChats = func(string) ([]SideChatChoice, error) { return choices, nil }
	model.onReadSideChatPrompts = func(ids []string) map[string]string {
		prompts := map[string]string{}
		for _, id := range ids {
			prompts[id] = "preview " + id
		}
		return prompts
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	model.transcript.Height = 20
	model.sidebarChoice = 0
	withPreviews := len(model.sidebarChooserWindow(20))
	if withPreviews < 1 || withPreviews >= len(choices)+1 {
		t.Fatalf("visible rows with previews = %d, want a window smaller than %d rows", withPreviews, len(choices)+1)
	}
	if handled, _ := model.updateSidebarChooserKey(key(keymsg.KeyCtrlF)); !handled || model.sidebarChoice != withPreviews {
		t.Fatalf("page_down handled=%t selection=%d, want %d preview rows", handled, model.sidebarChoice, withPreviews)
	}
}

// Rust #60319: before the first main-chat message the chooser explains why a
// side chat is unavailable and consumes activation.
func TestSidebarChooserWaitsForFirstMainMessageLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("parent-a")
	var started []SideStartParams
	model := NewModel(state, Options{
		Width:  120,
		Height: 30,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			started = append(started, params)
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "side-1"}, nil
		},
	})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.refreshTranscript()
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	panel := model.renderSidebarChooser(80, 24)
	if !strings.Contains(panel, sidebarWaitingSideChats) {
		t.Fatalf("chooser missing waiting note:\n%s", panel)
	}
	_, cmd := model.Update(key(keymsg.KeyEnter))
	if cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if len(started) != 0 {
		t.Fatalf("Enter started a side chat before the first main message: %#v", started)
	}
	if !model.sidebarChooserVisible() {
		t.Fatal("Enter dismissed the waiting chooser")
	}
}

// Rust #60319: Ctrl+C dismisses the chooser and returns to the main composer
// with its draft intact.
func TestSidebarChooserCtrlCPreservesMainDraftLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	typeText(t, model, "main draft")
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	_, cmd := model.Update(key(keymsg.KeyCtrlC))
	if cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if model.sidebarChooserVisible() {
		t.Fatal("Ctrl+C left the chooser open")
	}
	if got := model.composer.Value(); got != "main draft" {
		t.Fatalf("Ctrl+C changed the main draft to %q", got)
	}
}

// Rust #60319: a refreshed list keeps the selected chat selected even when the
// server returns a different order.
func TestSidebarChooserKeepsSelectionAcrossRefreshLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	choices := []SideChatChoice{{ThreadID: "saved-1", Name: "First"}, {ThreadID: "saved-2", Name: "Second"}}
	model.onListSideChats = func(string) ([]SideChatChoice, error) { return choices, nil }
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	model.sidebarChoice = 2
	next := model.applySideChatsLoaded(SideChatsLoadedMsg{
		ParentThreadID: model.sidebarChooserParent,
		RequestID:      model.sidebarDiscoveryRequest,
		Choices:        []SideChatChoice{{ThreadID: "saved-2", Name: "Second"}, {ThreadID: "saved-1", Name: "First"}},
	})
	runTeaCmdChain(t, model, next, 0)
	if choice, _ := model.sidebarChooserChat(model.sidebarChoice); choice.ThreadID != "saved-2" {
		t.Fatalf("selection moved to %#v, want saved-2", choice)
	}
}

// Rust #60319: a saved chat that is not retained in memory is attached and
// opened in the sidebar.
func TestSidebarChooserOpensSavedChatLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.onListSideChats = func(string) ([]SideChatChoice, error) {
		return []SideChatChoice{{ThreadID: "saved-1", Name: "Saved chat"}}, nil
	}
	model.onOpenSideChat = func(parentThreadID, sideThreadID string) (AgentThreadSwitchResponse, error) {
		if parentThreadID != "parent-a" || sideThreadID != "saved-1" {
			t.Fatalf("open side chat = %q/%q", parentThreadID, sideThreadID)
		}
		return AgentThreadSwitchResponse{
			Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "remember me"}},
			Status:   "idle",
		}, nil
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	model.sidebarChoice = 1
	cmd := model.acceptSidebarChooser()
	if cmd == nil {
		t.Fatal("opening a saved chat returned no command")
	}
	runTeaCmdChain(t, model, cmd, 0)
	if model.activeSide == nil || model.activeSide.SideThreadID != "saved-1" {
		t.Fatalf("active side = %#v", model.activeSide)
	}
	if model.State.ThreadID != "saved-1" {
		t.Fatalf("thread = %q, want saved-1", model.State.ThreadID)
	}
	if model.sidebarChooserVisible() {
		t.Fatal("chooser stayed open after opening the saved chat")
	}
}

// Rust #60319: clicking a painted chooser row opens that chat, with prompt
// preview rows counted in the hit test.
func TestSidebarChooserClickActivatesPreviewRowLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.onListSideChats = func(string) ([]SideChatChoice, error) {
		return []SideChatChoice{
			{ThreadID: "saved-1", Name: "First chat"},
			{ThreadID: "saved-2", Name: "Second chat"},
		}, nil
	}
	model.onReadSideChatPrompts = func(ids []string) map[string]string {
		prompts := map[string]string{}
		for _, id := range ids {
			prompts[id] = "preview for " + id
		}
		return prompts
	}
	opened := ""
	model.onOpenSideChat = func(parentThreadID, sideThreadID string) (AgentThreadSwitchResponse, error) {
		opened = sideThreadID
		return AgentThreadSwitchResponse{Status: "idle"}, nil
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	model.transcript.Height = 24
	model.sidebarTabStripTop = 0
	// Rows: 0 strip, 1 blank, 2 title, 3 status, 4 new chat, 5 group header,
	// 6 first title, 7 first preview, 8 second title, 9 second preview.
	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("split layout unavailable")
	}
	x := left + ansi.StringWidth(sideWorkspaceDivider)
	handled, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft, X: x, Y: 8,
	})
	if !handled {
		t.Fatal("click on the second chat row was not handled")
	}
	runTeaCmdChain(t, model, cmd, 0)
	if opened != "saved-2" {
		t.Fatalf("click opened %q, want saved-2", opened)
	}
	if model.State.ThreadID != "saved-2" {
		t.Fatalf("thread = %q, want saved-2", model.State.ThreadID)
	}
}

// Rust #60319 guard_chooser_key: holding Enter after the chooser activates a
// chat must not submit anything in the newly opened view.
func TestSidebarChooserActivationGuardSwallowsHeldEnterLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.onListSideChats = func(string) ([]SideChatChoice, error) {
		return []SideChatChoice{{ThreadID: "saved-1", Name: "Saved chat"}}, nil
	}
	model.onOpenSideChat = func(parentThreadID, sideThreadID string) (AgentThreadSwitchResponse, error) {
		return AgentThreadSwitchResponse{Status: "idle"}, nil
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	model.sidebarChoice = 1
	_, cmd := model.Update(key(keymsg.KeyEnter))
	runTeaCmdChain(t, model, cmd, 0)
	if model.State.ThreadID != "saved-1" {
		t.Fatalf("thread = %q, want saved-1", model.State.ThreadID)
	}
	if _, repeat := model.Update(key(keymsg.KeyEnter)); repeat != nil {
		t.Fatal("held Enter reached the newly opened view after activation")
	}
}
